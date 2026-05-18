package recon

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// coerce converts v into a value assignable to dest. dest must be
// addressable and settable. tag supplies per-field options
// (Base64 / Hex, Separator / KVSeparator, time.Time Layout).
//
// Struct walking is the bind walker's job; coerce handles leaf types
// only.
func coerce(v Value, dest reflect.Value, tag FieldTag) error {
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
		return coerce(v, dest.Elem(), tag)
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
		return coerceSlice(v, dest, tag)
	case reflect.Map:
		return coerceMap(v, dest, tag)
	case reflect.Struct:
		return coerceStruct(v, dest, tag)
	default:
		return fmt.Errorf("%w: cannot coerce %s into %s",
			ErrTypeMismatch, v.Kind(), dest.Type())
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

// valueAsString returns the string projection of v: a [StringKind]
// value passes through, anything else goes through fmt.Sprint.
func valueAsString(v Value) (string, error) {
	if v.Kind() == StringKind {
		return v.AsString()
	}
	return fmt.Sprint(v.Any()), nil
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

// coerceString fills a string destination. Non-string kinds flatten
// through fmt.Sprint so a numeric or bool value still populates a
// string field.
func coerceString(v Value, dest reflect.Value) error {
	switch v.Kind() {
	case StringKind:
		s, err := v.AsString()
		if err != nil {
			return err
		}
		dest.SetString(s)
	default:
		dest.SetString(fmt.Sprint(v.Any()))
	}
	return nil
}

// coerceBool accepts [BoolKind] directly and [StringKind] via
// strconv.ParseBool.
func coerceBool(v Value, dest reflect.Value) error {
	switch v.Kind() {
	case BoolKind:
		b, err := v.AsBool()
		if err != nil {
			return err
		}
		dest.SetBool(b)
		return nil
	case StringKind:
		s := mustAsString(v)
		b, err := strconv.ParseBool(s)
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
func coerceUint(v Value, dest reflect.Value) error {
	i, err := valueAsInt64(v)
	if err != nil {
		return err
	}
	if i < 0 {
		return fmt.Errorf("%w: %d is negative; cannot fit %s",
			ErrCoercion, i, dest.Type())
	}
	u := uint64(i)
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
func coerceSlice(v Value, dest reflect.Value, tag FieldTag) error {
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
		if err := coerce(el, out.Index(i), FieldTag{Separator: tag.Separator}); err != nil {
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

// coerceMap fills a map[string]V destination. [MapKind] recurses
// per-value; [StringKind] is split as "k=v,k=v" using tag.Separator
// (default ",") and tag.KVSeparator (default "=").
func coerceMap(v Value, dest reflect.Value, tag FieldTag) error {
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
		if err := coerce(el, ev, FieldTag{}); err != nil {
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
func coerceStruct(v Value, dest reflect.Value, tag FieldTag) error {
	if isTimeTime(dest.Type()) {
		return coerceTime(v, dest, tag)
	}
	if v.Kind() == MapKind {
		return coerceStructFromMap(v, dest)
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
// to RFC3339Nano, DateTime, and DateOnly.
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
		t, err := time.Parse(layout, s)
		if err == nil {
			dest.Set(reflect.ValueOf(t))
			return nil
		}
		for _, alt := range []string{time.RFC3339Nano, time.DateTime, time.DateOnly} {
			if t2, e2 := time.Parse(alt, s); e2 == nil {
				dest.Set(reflect.ValueOf(t2))
				return nil
			}
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
