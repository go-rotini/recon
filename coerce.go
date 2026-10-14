package recon

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// coerce converts v into a value assignable to dest. dest must be
// addressable and settable. tag supplies per-field options
// (Base64 / Hex, Separator / KVSeparator, time.Time Layout).
//
// Struct walking is the bind walker's job; coerce handles leaf types
// only, and struct elements of slices and maps with the default
// [elemRules].
func coerce(v Value, dest reflect.Value, tag FieldTag) error {
	return coerceWith(v, dest, tag, elemRules{})
}

// elemRules is what binding a struct element of a slice or map takes
// from the enclosing bind: the primary struct tag and the resolver for
// `expand` references.
type elemRules struct {
	tagName string                       // empty means [TagName]
	expand  func(string) (string, error) // nil leaves ${refs} as written
}

// coerceWith is [coerce] with er applied to struct elements.
func coerceWith(v Value, dest reflect.Value, tag FieldTag, er elemRules) error {
	// A null value sets the field to its zero value (a nil pointer,
	// slice or map), unless the type decodes itself through
	// [Unmarshaler], which sees the null.
	if v.Kind() == NullKind {
		if dest.CanAddr() {
			if u, ok := reflect.TypeAssert[Unmarshaler](dest.Addr()); ok {
				if err := u.UnmarshalRecon(v); err != nil {
					return fmt.Errorf("recon: UnmarshalRecon: %w", err)
				}
				return nil
			}
		}
		dest.Set(reflect.Zero(dest.Type()))
		return nil
	}

	// time.Time is special-cased first: its stdlib UnmarshalText
	// hardcodes RFC3339Nano and would otherwise mask the tag's
	// `layout=` option.
	if isTimeTime(dest.Type()) {
		return coerceTime(v, dest, tag)
	}

	if handled, err := tryUnmarshalerHooks(v, dest); handled {
		return err
	}

	if dest.Kind() == reflect.Pointer {
		if dest.IsNil() {
			dest.Set(reflect.New(dest.Type().Elem()))
		}
		return coerceWith(v, dest.Elem(), tag, er)
	}

	// time.Duration is an int64 alias; route it before the generic
	// int handler swallows "30s" as a literal 30.
	if isDurationType(dest.Type()) {
		return coerceDuration(v, dest)
	}

	switch dest.Kind() {
	case reflect.String:
		return coerceString(v, dest)
	case reflect.Bool:
		return coerceBool(v, dest)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return coerceInt(v, dest)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return coerceUint(v, dest)
	case reflect.Float32, reflect.Float64:
		return coerceFloat(v, dest)
	case reflect.Slice:
		return coerceSlice(v, dest, tag, er)
	case reflect.Array:
		return coerceArray(v, dest, tag, er)
	case reflect.Map:
		return coerceMap(v, dest, tag, er)
	case reflect.Struct:
		return coerceStruct(v, dest, tag, er)
	case reflect.Interface:
		return coerceInterface(v, dest, er)
	default:
		return fmt.Errorf("%w: cannot coerce %s into %s",
			ErrTypeMismatch, v.Kind(), dest.Type())
	}
}

// coerceInterface assigns v to an empty-interface dest — an `any` field, or a
// map[string]any / []any element. MapKind / SliceKind values recurse into
// nested map[string]any / []any (so a whole subtree binds), and scalars take
// the form [Value.Any] gives them. A non-empty interface dest is
// unsupported.
func coerceInterface(v Value, dest reflect.Value, er elemRules) error {
	if dest.NumMethod() != 0 {
		return fmt.Errorf("%w: cannot coerce %s into %s",
			ErrTypeMismatch, v.Kind(), dest.Type())
	}
	switch v.Kind() {
	case MapKind:
		m := map[string]any{}
		mv := reflect.ValueOf(&m).Elem()
		if err := coerceMap(v, mv, FieldTag{}, er); err != nil {
			return err
		}
		dest.Set(mv)
		return nil
	case SliceKind:
		var s []any
		sv := reflect.ValueOf(&s).Elem()
		if err := coerceSlice(v, sv, FieldTag{}, er); err != nil {
			return err
		}
		dest.Set(sv)
		return nil
	case NullKind:
		dest.Set(reflect.Zero(dest.Type()))
		return nil
	default:
		dest.Set(reflect.ValueOf(v.Any()))
		return nil
	}
}

// tryUnmarshalerHooks tries [Unmarshaler], then UnmarshalEnv, then
// encoding.TextUnmarshaler. Returns (handled=true, err) when a hook
// ran; (false, nil) when none matched.
func tryUnmarshalerHooks(v Value, dest reflect.Value) (bool, error) {
	if !dest.CanAddr() {
		return false, nil
	}
	ptr := dest.Addr().Interface()
	if u, ok := ptr.(Unmarshaler); ok {
		if err := u.UnmarshalRecon(v); err != nil {
			return true, fmt.Errorf("recon: UnmarshalRecon: %w", err)
		}
		return true, nil
	}
	if m, ok := ptr.(interface {
		UnmarshalEnv(text string) error
	}); ok {
		s, err := valueAsString(v)
		if err != nil {
			return true, err
		}
		if err := m.UnmarshalEnv(s); err != nil {
			return true, fmt.Errorf("recon: UnmarshalEnv: %w", err)
		}
		return true, nil
	}
	if m, ok := ptr.(interface {
		UnmarshalText(text []byte) error
	}); ok {
		s, err := valueAsString(v)
		if err != nil {
			return true, err
		}
		if err := m.UnmarshalText([]byte(s)); err != nil {
			return true, fmt.Errorf("recon: UnmarshalText: %w", err)
		}
		return true, nil
	}
	return false, nil
}

// valueAsString returns the string projection of v, its [Value.String]
// form.
func valueAsString(v Value) (string, error) {
	return v.String(), nil
}

// mustAsString returns the string carried by a [StringKind] v. The
// caller must have already checked the kind; misuse panics.
func mustAsString(v Value) string {
	s, err := v.AsString()
	if err != nil {
		panic(fmt.Errorf("recon: mustAsString on %s: %w", v.Kind(), err))
	}
	return s
}

// coerceString fills a string destination with v's [Value.String]
// form, so a numeric, bool or time value still populates a string
// field.
func coerceString(v Value, dest reflect.Value) error {
	dest.SetString(v.String())
	return nil
}

// coerceBool accepts [BoolKind] directly, [StringKind] through
// [parseBool], and the integers 0 and 1, as it accepts "0" and "1".
func coerceBool(v Value, dest reflect.Value) error {
	switch v.Kind() {
	case IntKind:
		i, err := v.AsInt64()
		if err != nil {
			return err
		}
		switch i {
		case 0, 1:
			dest.SetBool(i == 1)
			return nil
		default:
			return fmt.Errorf("%w: %d is not a bool (want 0 or 1)", ErrCoercion, i)
		}
	case BoolKind:
		b, err := v.AsBool()
		if err != nil {
			return err
		}
		dest.SetBool(b)
		return nil
	case StringKind:
		s := mustAsString(v)
		b, err := parseBool(s)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrCoercion, err)
		}
		dest.SetBool(b)
		return nil
	default:
		return fmt.Errorf("%w: cannot coerce %s to bool", ErrTypeMismatch, v.Kind())
	}
}

// coerceInt fills an int-kinded dest, checking overflow against the
// destination's bit width.
func coerceInt(v Value, dest reflect.Value) error {
	i, err := valueAsInt64(v)
	if err != nil {
		return err
	}
	if dest.OverflowInt(i) {
		return fmt.Errorf("%w: %d overflows %s", ErrCoercion, i, dest.Type())
	}
	dest.SetInt(i)
	return nil
}

// coerceUint mirrors [coerceInt] for unsigned destinations. Negative
// inputs are rejected; reflect.Value.SetUint would silently wrap them.
// Strings parse with strconv.ParseUint so the full uint64 range binds.
func coerceUint(v Value, dest reflect.Value) error {
	var u uint64
	if v.Kind() == StringKind {
		var err error
		if u, err = strconv.ParseUint(mustAsString(v), 0, 64); err != nil {
			return fmt.Errorf("%w: %w", ErrCoercion, err)
		}
	} else {
		i, err := valueAsInt64(v)
		if err != nil {
			return err
		}
		if i < 0 {
			return fmt.Errorf("%w: %d is negative; cannot fit %s",
				ErrCoercion, i, dest.Type())
		}
		u = uint64(i)
	}
	if dest.OverflowUint(u) {
		return fmt.Errorf("%w: %d overflows %s", ErrCoercion, u, dest.Type())
	}
	dest.SetUint(u)
	return nil
}

// coerceFloat fills a float-kinded dest, checking overflow.
func coerceFloat(v Value, dest reflect.Value) error {
	f, err := valueAsFloat64(v)
	if err != nil {
		return err
	}
	if dest.OverflowFloat(f) {
		return fmt.Errorf("%w: %v overflows %s", ErrCoercion, f, dest.Type())
	}
	dest.SetFloat(f)
	return nil
}

// coerceSlice fills a slice destination from a [SliceKind] value
// (element-wise recursion) or a [StringKind] value (split on
// tag.Separator, default ","). []byte destinations route through
// [coerceBytes].
func coerceSlice(v Value, dest reflect.Value, tag FieldTag, er elemRules) error {
	if dest.Type().Elem().Kind() == reflect.Uint8 {
		return coerceBytes(v, dest, tag)
	}

	var elements []Value
	switch v.Kind() {
	case SliceKind:
		s, err := v.AsSlice()
		if err != nil {
			return err
		}
		elements = s
	case StringKind:
		sep := tag.Separator
		if sep == "" {
			sep = ","
		}
		s := mustAsString(v)
		if s == "" {
			dest.Set(reflect.MakeSlice(dest.Type(), 0, 0))
			return nil
		}
		parts := strings.Split(s, sep)
		elements = make([]Value, len(parts))
		for i, p := range parts {
			elements[i] = NewValue(strings.TrimSpace(p))
		}
	default:
		return fmt.Errorf("%w: cannot coerce %s to slice", ErrTypeMismatch, v.Kind())
	}

	out := reflect.MakeSlice(dest.Type(), len(elements), len(elements))
	for i, el := range elements {
		if err := coerceWith(el, out.Index(i), FieldTag{Separator: tag.Separator}, er); err != nil {
			return fmt.Errorf("element %d: %w", i, err)
		}
	}
	dest.Set(out)
	return nil
}

// coerceBytes fills a []byte / [N]byte destination. The tag's Base64 /
// Hex options dictate how a string input is decoded.
func coerceBytes(v Value, dest reflect.Value, tag FieldTag) error {
	switch v.Kind() {
	case StringKind:
		s := mustAsString(v)
		switch {
		case tag.Base64:
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return fmt.Errorf("%w: base64 decode: %w", ErrCoercion, err)
			}
			dest.SetBytes(b)
		case tag.Hex:
			b, err := hex.DecodeString(s)
			if err != nil {
				return fmt.Errorf("%w: hex decode: %w", ErrCoercion, err)
			}
			dest.SetBytes(b)
		default:
			dest.SetBytes([]byte(s))
		}
		return nil
	case SliceKind:
		els, err := v.AsSlice()
		if err != nil {
			return err
		}
		out := make([]byte, len(els))
		for i, el := range els {
			n, err := valueAsInt64(el)
			if err != nil {
				return fmt.Errorf("byte %d: %w", i, err)
			}
			if n < 0 || n > 255 {
				return fmt.Errorf("%w: byte %d out of range: %d",
					ErrCoercion, i, n)
			}
			out[i] = byte(n)
		}
		dest.SetBytes(out)
		return nil
	default:
		return fmt.Errorf("%w: cannot coerce %s to []byte",
			ErrTypeMismatch, v.Kind())
	}
}

// coerceArray fills an [N]T destination the way [coerceSlice] fills a
// []T, then requires exactly N elements (N bytes for [N]byte).
func coerceArray(v Value, dest reflect.Value, tag FieldTag, er elemRules) error {
	tmp := reflect.New(reflect.SliceOf(dest.Type().Elem())).Elem()
	if err := coerceSlice(v, tmp, tag, er); err != nil {
		return err
	}
	if tmp.Len() != dest.Len() {
		return fmt.Errorf("%w: have %d elements, %s needs %d",
			ErrCoercion, tmp.Len(), dest.Type(), dest.Len())
	}
	reflect.Copy(dest, tmp)
	return nil
}

// coerceMap fills a map[string]V destination. [MapKind] recurses
// per-value; [StringKind] is split as "k=v,k=v" using tag.Separator
// (default ",") and tag.KVSeparator (default "=").
func coerceMap(v Value, dest reflect.Value, tag FieldTag, er elemRules) error {
	if dest.Type().Key().Kind() != reflect.String {
		return fmt.Errorf("%w: map key must be string, got %s",
			ErrTypeMismatch, dest.Type().Key())
	}

	var entries map[string]Value
	switch v.Kind() {
	case MapKind:
		m, err := v.AsMap()
		if err != nil {
			return err
		}
		entries = m
	case StringKind:
		sep := tag.Separator
		if sep == "" {
			sep = ","
		}
		kv := tag.KVSeparator
		if kv == "" {
			kv = "="
		}
		s := mustAsString(v)
		entries = parseStringMap(s, sep, kv)
	default:
		return fmt.Errorf("%w: cannot coerce %s to map",
			ErrTypeMismatch, v.Kind())
	}

	out := reflect.MakeMapWithSize(dest.Type(), len(entries))
	elemType := dest.Type().Elem()
	for k, el := range entries {
		ev := reflect.New(elemType).Elem()
		if err := coerceWith(el, ev, FieldTag{}, er); err != nil {
			return fmt.Errorf("entry %q: %w", k, err)
		}
		out.SetMapIndex(reflect.ValueOf(k), ev)
	}
	dest.Set(out)
	return nil
}

// parseStringMap splits "k1=v1,k2=v2" into a map[string]Value. Empty
// entries are dropped; an entry without the KV delimiter contributes
// (entry, "").
func parseStringMap(s, sep, kv string) map[string]Value {
	out := map[string]Value{}
	for entry := range strings.SplitSeq(s, sep) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		k, val, ok := strings.Cut(entry, kv)
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if !ok {
			out[k] = NewValue("")
			continue
		}
		out[k] = NewValue(strings.TrimSpace(val))
	}
	return out
}

// coerceStruct handles the struct shapes coerce sees as leaves:
// time.Time (routed to [coerceTime]) and MapKind → struct (the
// `format=` post-decode path). Plain struct fields on a [Registry.Bind]
// target are recursed into by the walker and never reach here.
func coerceStruct(v Value, dest reflect.Value, tag FieldTag, er elemRules) error {
	if isTimeTime(dest.Type()) {
		return coerceTime(v, dest, tag)
	}
	if v.Kind() == MapKind {
		return bindStructFromMap(v, dest, er)
	}
	return fmt.Errorf("%w: nested struct %s should be walked, not coerced",
		ErrTypeMismatch, dest.Type())
}

// coerceStructFromMap populates dest from a [MapKind] value's
// underlying map. Field name resolution follows the bind walker:
// recon tag, then the env / json / yaml / toml fallback chain, then
// the lowercased Go name.
func coerceStructFromMap(v Value, dest reflect.Value) error {
	m, err := v.AsMap()
	if err != nil {
		return err
	}
	t := dest.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		key := fieldKeyFromTag(sf)
		if key == "" {
			continue
		}
		sub, ok := m[key]
		if !ok {
			continue
		}
		if err := coerce(sub, dest.Field(i), FieldTag{}); err != nil {
			return fmt.Errorf("field %s: %w", sf.Name, err)
		}
	}
	return nil
}

// bindStructFromMap populates dest from a [MapKind] value with the
// naming rules [Registry.Bind] uses for top-level fields: the primary
// tag (then env / json / yaml / toml), else the snake_case Go name,
// with a lowercased Go name accepted when the snake_case key is absent.
// `inline` and unnamed embedded structs read from the same map. The
// options that apply are the name (a dotted name reads a nested map),
// `aliases=`, `default=`, `required`, `notEmpty`, `fromFile`, `expand`,
// `transform=` and the value-format options. It serves struct elements
// of slices and maps, and `format=`-decoded struct fields.
func bindStructFromMap(v Value, dest reflect.Value, er elemRules) error {
	m, err := v.AsMap()
	if err != nil {
		return err
	}
	return bindStructFields(m, dest, nil, er)
}

// bindStructFields is [bindStructFromMap] over m. inlined lists the
// struct types already being filled from m, so a type that embeds a
// pointer to itself stops instead of recursing forever.
func bindStructFields(m map[string]Value, dest reflect.Value, inlined []reflect.Type, er elemRules) error {
	t := dest.Type()
	inlined = append(inlined, t)
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		tag, _ := fieldTag(sf, er.tagName)
		if tag.Skip {
			continue
		}
		fv := dest.Field(i)
		if tag.Inline || (sf.Anonymous && tag.Name == "") {
			if inner, ok := structTarget(fv, inlined); ok {
				if err := bindStructFields(m, inner, inlined, er); err != nil {
					return err
				}
				continue
			}
		}
		if err := bindElementField(m, fv, sf, tag, er); err != nil {
			return fmt.Errorf("field %s: %w", sf.Name, err)
		}
	}
	return nil
}

// bindElementField fills one field of a struct element from m.
func bindElementField(m map[string]Value, fv reflect.Value, sf reflect.StructField, tag FieldTag, er elemRules) error {
	sub, found := lookupElementKey(m, sf, tag)
	if found && sub.Kind() == NullKind && (tag.Required || tag.HasDefault) {
		found = false
	}
	if !found {
		switch {
		case tag.HasDefault:
			sub = NewValue(tag.DefaultValue)
		case tag.Required:
			return ErrKeyNotFound
		default:
			return nil
		}
	}
	if tag.FromFile {
		if sub.String() == "" {
			return fmt.Errorf("%w: fromFile target path is empty", ErrInvalidPath)
		}
		data, err := readFromFile(sub.String())
		if err != nil {
			return fmt.Errorf("recon: fromFile %q: %w", sub.String(), err)
		}
		sub = NewValue(string(data))
	}
	if tag.Expand && er.expand != nil && sub.Kind() == StringKind {
		s, err := er.expand(mustAsString(sub))
		if err != nil {
			return err
		}
		sub = NewValue(s)
	}
	if tag.NotEmpty && isEmptyValue(sub) {
		return ErrEmptyValue
	}
	return coerceWith(sub, fv, tag, er)
}

// fieldTag returns sf's parsed tag from the primary tag (empty means
// [TagName]), else from the first of env / json / yaml / toml, and
// whether either was present. A fallback tag's `omitempty` is left out:
// those tags mean something else by it.
func fieldTag(sf reflect.StructField, primary string) (FieldTag, bool) {
	if primary == "" {
		primary = TagName
	}
	if raw, ok := sf.Tag.Lookup(primary); ok && raw != "" {
		return ParseTag(raw), true
	}
	for _, alt := range fallbackTagNames {
		if alt == primary {
			continue
		}
		if raw, ok := sf.Tag.Lookup(alt); ok && raw != "" {
			tag := ParseTag(raw)
			tag.OmitEmpty = false
			return tag, true
		}
	}
	return FieldTag{}, false
}

// lookupElementKey finds sf's entry in m: the tag's name (transformed;
// a dotted name reads nested maps), or without one the snake_case Go
// name and then the lowercased Go name; then each `aliases=` entry.
func lookupElementKey(m map[string]Value, sf reflect.StructField, tag FieldTag) (Value, bool) {
	if tag.Name != "" {
		p := Path{tag.Name} // ParsePath of a name with no delimiter or bracket
		if strings.ContainsAny(tag.Name, DefaultDelimiter+"[") {
			p = ParsePath(tag.Name)
		}
		if v, ok := lookupElementPath(m, p, tag.Transform); ok {
			return v, true
		}
	} else {
		if v, ok := m[applyTransform(toSnakeCase(sf.Name), tag.Transform)]; ok {
			return v, true
		}
		if v, ok := m[strings.ToLower(sf.Name)]; ok {
			return v, true
		}
	}
	for _, alias := range tag.Aliases {
		if v, ok := lookupElementPath(m, ParsePath(alias), ""); ok {
			return v, true
		}
	}
	return Value{}, false
}

// lookupElementPath reads the entry at p in m, descending through
// nested maps, with each segment re-spelled by transform.
func lookupElementPath(m map[string]Value, p Path, transform string) (Value, bool) {
	var cur Value
	for i, seg := range p {
		v, ok := m[applyTransform(seg, transform)]
		if !ok {
			return Value{}, false
		}
		cur = v
		if i < len(p)-1 {
			next, err := v.AsMap()
			if err != nil {
				return Value{}, false
			}
			m = next
		}
	}
	return cur, len(p) > 0
}

// structTarget returns the struct fv holds or points to, allocating a
// nil pointer, or false when fv is not a walkable struct or its type is
// already in inlined.
func structTarget(fv reflect.Value, inlined []reflect.Type) (reflect.Value, bool) {
	if !isWalkableStructValue(fv) {
		return reflect.Value{}, false
	}
	st := fv.Type()
	if st.Kind() == reflect.Pointer {
		st = st.Elem()
	}
	if slices.Contains(inlined, st) {
		return reflect.Value{}, false
	}
	if fv.Kind() == reflect.Pointer {
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		return fv.Elem(), true
	}
	return fv, true
}

// fieldKeyFromTag returns the map-key used to look up sf's value
// during a [coerceStructFromMap] walk. Multi-segment tag names
// ("server.port") collapse to the first segment since a map-key is
// a single string.
func fieldKeyFromTag(sf reflect.StructField) string {
	for _, name := range append([]string{TagName}, fallbackTagNames[:]...) {
		raw, ok := sf.Tag.Lookup(name)
		if !ok || raw == "" {
			continue
		}
		ft := ParseTag(raw)
		if ft.Skip {
			return ""
		}
		if ft.Name != "" {
			return strings.SplitN(ft.Name, ".", 2)[0]
		}
	}
	return strings.ToLower(sf.Name)
}

// coerceTime fills a time.Time destination. [TimeKind] passes through;
// [StringKind] parses via tag.Layout (default RFC3339) and falls back
// to the forms [Value.AsTime] accepts, without the time-of-day forms
// when tag.Layout is set.
func coerceTime(v Value, dest reflect.Value, tag FieldTag) error {
	switch v.Kind() {
	case TimeKind:
		t, err := v.AsTime()
		if err != nil {
			return err
		}
		dest.Set(reflect.ValueOf(t))
		return nil
	case StringKind:
		s := mustAsString(v)
		layout := tag.Layout
		if layout == "" {
			layout = time.RFC3339
		}
		t, err := parseTimeText(s, layout, tag.Layout == "")
		if err == nil {
			dest.Set(reflect.ValueOf(t))
			return nil
		}
		return fmt.Errorf("%w: parse %q as time (layout %q): %w",
			ErrCoercion, s, layout, err)
	default:
		return fmt.Errorf("%w: cannot coerce %s to time.Time",
			ErrTypeMismatch, v.Kind())
	}
}

// valueAsInt64 projects v to int64. Used by [coerceInt], [coerceUint],
// and [coerceBytes].
func valueAsInt64(v Value) (int64, error) {
	switch v.Kind() {
	case IntKind:
		return v.AsInt64()
	case FloatKind:
		f, err := v.AsFloat64()
		if err != nil {
			return 0, err
		}
		// Converting a float outside the int64 range gives a different
		// result on each architecture, so check the range first. 2^63 is
		// exact as a float64; MaxInt64 is not.
		if f < -(1<<63) || f >= 1<<63 {
			return 0, fmt.Errorf("%w: %v overflows int64", ErrCoercion, f)
		}
		if f != float64(int64(f)) {
			return 0, fmt.Errorf("%w: %v has non-integer fractional part",
				ErrCoercion, f)
		}
		return int64(f), nil
	case StringKind:
		s := mustAsString(v)
		i, err := strconv.ParseInt(s, 0, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %w", ErrCoercion, err)
		}
		return i, nil
	case DurationKind:
		d, err := v.AsDuration()
		if err != nil {
			return 0, err
		}
		return int64(d), nil
	default:
		return 0, fmt.Errorf("%w: cannot coerce %s to int",
			ErrTypeMismatch, v.Kind())
	}
}

// valueAsFloat64 projects v to float64. Used by [coerceFloat].
func valueAsFloat64(v Value) (float64, error) {
	switch v.Kind() {
	case FloatKind:
		return v.AsFloat64()
	case IntKind:
		return v.AsFloat64() // Value widens losslessly
	case StringKind:
		s := mustAsString(v)
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %w", ErrCoercion, err)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("%w: cannot coerce %s to float",
			ErrTypeMismatch, v.Kind())
	}
}

// coerceDuration handles time.Duration destinations. Routed by the
// coerce dispatcher before [coerceInt] so "30s" is parsed as a
// duration rather than the integer 30.
func coerceDuration(v Value, dest reflect.Value) error {
	switch v.Kind() {
	case DurationKind:
		d, err := v.AsDuration()
		if err != nil {
			return err
		}
		dest.SetInt(int64(d))
		return nil
	case StringKind:
		s := mustAsString(v)
		d, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("%w: parse duration %q: %w",
				ErrCoercion, s, err)
		}
		dest.SetInt(int64(d))
		return nil
	case IntKind:
		i, err := v.AsInt64()
		if err != nil {
			return err
		}
		dest.SetInt(i)
		return nil
	default:
		return fmt.Errorf("%w: cannot coerce %s to time.Duration",
			ErrTypeMismatch, v.Kind())
	}
}

func isDurationType(t reflect.Type) bool {
	return t == reflect.TypeFor[time.Duration]()
}

func isTimeTime(t reflect.Type) bool {
	return t == reflect.TypeFor[time.Time]()
}

// parseBool accepts what [strconv.ParseBool] does, and "true", "false",
// "yes", "no", "on" and "off" in any case.
func parseBool(s string) (bool, error) {
	b, err := strconv.ParseBool(s)
	if err == nil {
		return b, nil
	}
	switch strings.ToLower(s) {
	case "true", "yes", "on":
		return true, nil
	case "false", "no", "off":
		return false, nil
	}
	return false, err
}
