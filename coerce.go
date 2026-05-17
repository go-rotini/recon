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

// coerce converts v into a value assignable to dest. dest MUST be an
// addressable, settable [reflect.Value]; the caller is responsible for
// ensuring that (typically by Indirect()ing a pointer field). tag
// supplies per-field options that affect conversion (Base64 / Hex,
// custom Separator / KVSeparator, time.Time Layout).
//
// The function intentionally does NOT handle struct fields — that's the
// recursive responsibility of [bindWalker]. coerce is for leaf types
// only.
func coerce(v Value, dest reflect.Value, tag FieldTag) error {
	// time.Time is a leaf type recon owns end-to-end. The stdlib's
	// UnmarshalText insists on RFC3339Nano and ignores per-field
	// `layout=` tags; routing time.Time through coerceTime FIRST
	// keeps the tag honored.
	if dest.Kind() == reflect.Struct &&
		dest.Type().PkgPath() == "time" &&
		dest.Type().Name() == "Time" {
		return coerceTime(v, dest, tag)
	}

	// Recon-native Unmarshaler / UnmarshalEnv / encoding.TextUnmarshaler
	// hooks supersede the built-in coercion rules so a user-defined
	// type can take over its own decoding.
	if handled, err := tryUnmarshalerHooks(v, dest); handled {
		return err
	}

	// Pointer fields: allocate the pointee, recurse on its element.
	if dest.Kind() == reflect.Pointer {
		if dest.IsNil() {
			dest.Set(reflect.New(dest.Type().Elem()))
		}
		return coerce(v, dest.Elem(), tag)
	}

	// time.Duration is technically int64; route it through the
	// duration parser before the generic int handler swallows it.
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

// tryUnmarshalerHooks attempts to populate dest via one of the
// well-known unmarshal hooks. The dispatch order is:
//  1. *T implements [Unmarshaler] — the recon-native shape.
//  2. *T implements UnmarshalEnv(text string) error — env.Secret and
//     anything else that wants to look identical to it.
//  3. *T implements encoding.TextUnmarshaler — the stdlib convention.
//
// Returns (handled=true, err) when a hook ran; (handled=false, nil)
// when none did and the caller should fall back to the built-in rules.
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

// valueAsString is the lossy-but-uniform string projection coerce uses
// when feeding a Value into a hook that wants `string`. It mirrors
// [Value.AsString] for StringKind and otherwise calls fmt.Sprint on
// the underlying value — JSON-style.
func valueAsString(v Value) (string, error) {
	if v.Kind() == StringKind {
		return v.AsString()
	}
	return fmt.Sprint(v.Any()), nil
}

// stringFromValueOrDie returns the string carried by a StringKind
// [Value]. The caller MUST have verified the kind already; the helper
// panics on misuse so the per-coercion callsite stays one line.
func stringFromValueOrDie(v Value) string {
	s, err := v.AsString()
	if err != nil {
		panic(fmt.Errorf("recon: stringFromValueOrDie on %s: %w", v.Kind(), err))
	}
	return s
}

// coerceString fills a string-kinded dest. Booleans / numbers / time
// values flatten through fmt.Sprint so a YAML key with a numeric value
// can still populate a `string` field — Viper compatibility.
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

// coerceBool accepts bool-kinded inputs verbatim and string inputs via
// strconv.ParseBool (which handles "true"/"false"/"1"/"0"/"yes"/"no"
// case-insensitively per Go convention).
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
		s := stringFromValueOrDie(v)
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

// coerceInt accepts IntKind directly; FloatKind values must be
// integral; StringKind goes through strconv.ParseInt (auto-base via
// the 0 base). Overflow against the destination's bit-width is checked
// before the assignment.
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
// inputs are rejected because Go's reflect.Value.SetUint would silently
// wrap them.
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

// coerceFloat accepts FloatKind, widens IntKind, and parses StringKind
// through strconv.ParseFloat. Overflow is checked against the
// destination's bit-width.
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

// coerceSlice fills a slice destination from either a SliceKind value
// (element-wise recursion) or a StringKind value (split on the tag's
// Separator, defaulting to ","). The []byte special case routes
// through base64 / hex decoding when the tag opts in.
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
		s := stringFromValueOrDie(v)
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

// coerceBytes fills a []byte / [N]byte destination. The tag's Base64
// / Hex options dictate how a StringKind value is decoded; SliceKind
// is iterated as a list of byte-valued elements.
func coerceBytes(v Value, dest reflect.Value, tag FieldTag) error {
	switch v.Kind() {
	case StringKind:
		s := stringFromValueOrDie(v)
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

// coerceMap fills a map[string]V destination. MapKind inputs recurse
// per-value; StringKind inputs are split as "k=v,k=v" using the tag's
// Separator (entry delimiter, default ",") and KVSeparator (key-value
// delimiter, default "=").
//
// Non-string map keys are rejected — recon's data model uses
// string-keyed maps throughout.
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
		s := stringFromValueOrDie(v)
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
// entries are skipped; an entry without the KV delimiter contributes
// (entry, "") so callers see the bare key.
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

// coerceStruct handles the struct shapes coerce sees as leaves —
// presently just time.Time. Every other struct type is rejected here
// because the bind walker should have recursed before reaching coerce.
func coerceStruct(v Value, dest reflect.Value, tag FieldTag) error {
	if dest.Type() == reflect.TypeFor[time.Time]() {
		return coerceTime(v, dest, tag)
	}
	return fmt.Errorf("%w: nested struct %s should be walked, not coerced",
		ErrTypeMismatch, dest.Type())
}

// coerceTime fills a time.Time destination. TimeKind passes through
// untouched; StringKind parses via the tag's Layout (default
// time.RFC3339) and falls back to a small list of common layouts.
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
		s := stringFromValueOrDie(v)
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

// valueAsInt64 is the shared IntKind / FloatKind / StringKind →
// int64 projection. Used by [coerceInt] / [coerceUint] / [coerceBytes].
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
		s := stringFromValueOrDie(v)
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

// valueAsFloat64 is the shared IntKind / FloatKind / StringKind →
// float64 projection used by [coerceFloat].
func valueAsFloat64(v Value) (float64, error) {
	switch v.Kind() {
	case FloatKind:
		return v.AsFloat64()
	case IntKind:
		return v.AsFloat64() // Value already widens
	case StringKind:
		s := stringFromValueOrDie(v)
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

// coerceDuration is split out because time.Duration is technically an
// int64 type — without an early check, [coerceInt] would happily parse
// `"30s"` as the int 30 and silently lose the unit. The dispatcher in
// [coerce] routes time.Duration here before [coerceInt].
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
		s := stringFromValueOrDie(v)
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

// isDurationType reports whether t is exactly [time.Duration]. Used by
// the [coerce] dispatcher to route Duration-typed fields away from the
// generic int handler.
func isDurationType(t reflect.Type) bool {
	return t == reflect.TypeFor[time.Duration]()
}
