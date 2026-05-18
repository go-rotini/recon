package recon

import (
	"fmt"
	"maps"
	"reflect"
	"time"
)

// ValueKind identifies the wire-form type of a [Value]. The registry preserves
// the wire type from a [Source] all the way through to coercion time, so that
// callers can ask for typed values via [Get] / [Bind] without losing
// information.
type ValueKind int

// ValueKind constants. The ordering is stable but not API-visible — callers
// should compare against the named constants.
const (
	// NullKind is the absence of a value. A source returning ok=true with a
	// NullKind value represents "the key is set, but to nil/null" (e.g., a
	// YAML key with an explicit null value).
	NullKind ValueKind = iota
	// StringKind is a UTF-8 string.
	StringKind
	// IntKind is a signed integer; canonical storage is int64.
	IntKind
	// FloatKind is a floating-point number; canonical storage is float64.
	FloatKind
	// BoolKind is a boolean.
	BoolKind
	// TimeKind is a time.Time.
	TimeKind
	// DurationKind is a time.Duration.
	DurationKind
	// SliceKind is an ordered list of Values.
	SliceKind
	// MapKind is a map from string keys to Values.
	MapKind
	// RawKind is bytes plus a format hint — used when a source defers parsing
	// (e.g., a config field that holds a JSON-encoded blob to be decoded on
	// demand).
	RawKind
)

// String returns the lowercase, dash-free name of the kind ("string", "int", …).
// Useful in error messages.
func (k ValueKind) String() string {
	switch k {
	case NullKind:
		return "null"
	case StringKind:
		return "string"
	case IntKind:
		return "int"
	case FloatKind:
		return "float"
	case BoolKind:
		return "bool"
	case TimeKind:
		return "time"
	case DurationKind:
		return "duration"
	case SliceKind:
		return "slice"
	case MapKind:
		return "map"
	case RawKind:
		return "raw"
	default:
		return "unknown"
	}
}

// Value is a typed, source-tagged scalar / container the registry hands back
// from a [Source] lookup. It preserves the original wire type so coercion can
// be performed at the call site without ambiguity.
//
// Value is immutable from the caller's perspective: the constructors return a
// fresh Value, and the As* methods do not mutate. Internally the registry may
// fill in the Source field after construction; once handed to a caller, the
// Value should be treated as read-only.
type Value struct {
	kind   ValueKind
	raw    any
	source string
}

// NewValue wraps a Go value in a Value, inferring the ValueKind from v's
// dynamic type. nil → NullKind; string → StringKind; integer types → IntKind
// (canonicalised to int64); floating types → FloatKind; bool → BoolKind;
// time.Time → TimeKind; time.Duration → DurationKind; []any → SliceKind;
// map[string]any → MapKind; otherwise StringKind via fmt.Sprint.
func NewValue(v any) Value {
	switch x := v.(type) {
	case nil:
		return Value{kind: NullKind}
	case Value:
		return x
	case string:
		return Value{kind: StringKind, raw: x}
	case bool:
		return Value{kind: BoolKind, raw: x}
	case time.Time:
		return Value{kind: TimeKind, raw: x}
	case time.Duration:
		return Value{kind: DurationKind, raw: x}
	case []any:
		out := make([]Value, len(x))
		for i, e := range x {
			out[i] = NewValue(e)
		}
		return Value{kind: SliceKind, raw: out}
	case []Value:
		// Copy to avoid aliasing the caller's slice.
		out := make([]Value, len(x))
		copy(out, x)
		return Value{kind: SliceKind, raw: out}
	case map[string]any:
		out := make(map[string]Value, len(x))
		for k, e := range x {
			out[k] = NewValue(e)
		}
		return Value{kind: MapKind, raw: out}
	case map[string]Value:
		out := make(map[string]Value, len(x))
		maps.Copy(out, x)
		return Value{kind: MapKind, raw: out}
	}
	if num, ok := numericValue(v); ok {
		return num
	}
	return Value{kind: StringKind, raw: fmt.Sprint(v)}
}

// numericValue handles the integer / unsigned-integer / floating-point arms
// of [NewValue]. Extracted so NewValue stays under the cyclop ceiling.
// Returns ok=false when v is not a numeric type recon recognizes.
func numericValue(v any) (Value, bool) {
	switch x := v.(type) {
	case int:
		return Value{kind: IntKind, raw: int64(x)}, true
	case int8:
		return Value{kind: IntKind, raw: int64(x)}, true
	case int16:
		return Value{kind: IntKind, raw: int64(x)}, true
	case int32:
		return Value{kind: IntKind, raw: int64(x)}, true
	case int64:
		return Value{kind: IntKind, raw: x}, true
	case uint:
		return Value{kind: IntKind, raw: int64(x)}, true
	case uint8:
		return Value{kind: IntKind, raw: int64(x)}, true
	case uint16:
		return Value{kind: IntKind, raw: int64(x)}, true
	case uint32:
		return Value{kind: IntKind, raw: int64(x)}, true
	case uint64:
		return Value{kind: IntKind, raw: int64(x)}, true
	case float32:
		return Value{kind: FloatKind, raw: float64(x)}, true
	case float64:
		return Value{kind: FloatKind, raw: x}, true
	}
	return Value{}, false
}

// NewRawValue wraps undecoded bytes plus a format hint in a Value. Useful when
// a config field holds a sub-document in a different format (e.g., a JSON-
// encoded blob stored inside a YAML key).
func NewRawValue(format string, data []byte) Value {
	cp := make([]byte, len(data))
	copy(cp, data)
	return Value{kind: RawKind, raw: RawValue{Format: format, Data: cp}}
}

// withSource returns a copy of v tagged with the given source name. Used
// internally by the registry to fill provenance after a source returns its
// raw value; exercised by TestValue_Source in this package.
func (v Value) withSource(name string) Value {
	v.source = name
	return v
}

// Kind reports the wire-form type of v.
func (v Value) Kind() ValueKind { return v.kind }

// Source returns the name of the [Source] that produced v, if known. Empty
// when v was constructed directly via [NewValue] or [NewRawValue] and has not
// yet been adopted by a registry.
func (v Value) Source() string { return v.source }

// IsZero reports whether v carries no underlying datum. A NullKind Value is
// zero; every other kind is non-zero (an empty string, empty slice, or empty
// map is still a present value).
func (v Value) IsZero() bool { return v.kind == NullKind }

// Any returns the underlying Go value as-is. The concrete type follows the
// kind: string, int64, float64, bool, time.Time, time.Duration, []Value,
// map[string]Value, RawValue, or nil. Callers typically prefer the typed As*
// accessors.
func (v Value) Any() any {
	if v.kind == NullKind {
		return nil
	}
	return v.raw
}

// unwrapValueDeep converts a [Value] into the plain-Go shape codecs
// and JSON encoders expect: scalars become their underlying type,
// [SliceKind] becomes []any (recursively unwrapped), [MapKind]
// becomes map[string]any (recursively unwrapped). The function
// exists because [Value.Any] returns map[string]Value / []Value for
// compound kinds — useful for further chain-of-Value processing
// but unfit for direct serialization through encoding/json or the
// bundled codecs.
func unwrapValueDeep(v Value) any {
	switch v.Kind() {
	case SliceKind:
		s, ok := v.Any().([]Value)
		if !ok {
			return v.Any()
		}
		out := make([]any, len(s))
		for i, el := range s {
			out[i] = unwrapValueDeep(el)
		}
		return out
	case MapKind:
		m, ok := v.Any().(map[string]Value)
		if !ok {
			return v.Any()
		}
		out := make(map[string]any, len(m))
		for k, el := range m {
			out[k] = unwrapValueDeep(el)
		}
		return out
	default:
		return v.Any()
	}
}

// String returns the canonical string representation of v for printing.
// Strings are returned verbatim; other kinds use fmt.Sprint on the underlying
// value. Use AsString for the strict typed accessor that errors on a
// mismatch.
func (v Value) String() string {
	switch v.kind {
	case NullKind:
		return ""
	case StringKind:
		s, _ := v.raw.(string)
		return s
	default:
		return fmt.Sprint(v.raw)
	}
}

// AsString returns the string value if v is StringKind, else an error.
func (v Value) AsString() (string, error) {
	if v.kind != StringKind {
		return "", typeMismatchErr(v.kind, "string")
	}
	s, _ := v.raw.(string)
	return s, nil
}

// AsInt64 returns the integer value if v is IntKind, else an error.
func (v Value) AsInt64() (int64, error) {
	if v.kind != IntKind {
		return 0, typeMismatchErr(v.kind, "int64")
	}
	i, _ := v.raw.(int64)
	return i, nil
}

// AsFloat64 returns the float value if v is FloatKind, else an error.
// IntKind values are widened lossless-ly to float64 as a convenience.
func (v Value) AsFloat64() (float64, error) {
	switch v.kind {
	case FloatKind:
		f, _ := v.raw.(float64)
		return f, nil
	case IntKind:
		i, _ := v.raw.(int64)
		return float64(i), nil
	default:
		return 0, typeMismatchErr(v.kind, "float64")
	}
}

// AsBool returns the boolean value if v is BoolKind, else an error.
func (v Value) AsBool() (bool, error) {
	if v.kind != BoolKind {
		return false, typeMismatchErr(v.kind, "bool")
	}
	b, _ := v.raw.(bool)
	return b, nil
}

// AsTime returns the time value if v is TimeKind. StringKind values are
// parsed as RFC 3339 as a convenience; other kinds error.
func (v Value) AsTime() (time.Time, error) {
	switch v.kind {
	case TimeKind:
		t, _ := v.raw.(time.Time)
		return t, nil
	case StringKind:
		s, _ := v.raw.(string)
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: parse time %q: %w", ErrCoercion, s, err)
		}
		return t, nil
	default:
		return time.Time{}, typeMismatchErr(v.kind, "time.Time")
	}
}

// AsDuration returns the duration value if v is DurationKind. StringKind
// values are parsed via time.ParseDuration as a convenience; other kinds
// error.
func (v Value) AsDuration() (time.Duration, error) {
	switch v.kind {
	case DurationKind:
		d, _ := v.raw.(time.Duration)
		return d, nil
	case StringKind:
		s, _ := v.raw.(string)
		d, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("%w: parse duration %q: %w", ErrCoercion, s, err)
		}
		return d, nil
	case IntKind:
		i, _ := v.raw.(int64)
		return time.Duration(i), nil
	default:
		return 0, typeMismatchErr(v.kind, "time.Duration")
	}
}

// AsSlice returns the underlying []Value if v is SliceKind, else an error.
// The returned slice is the registry's storage — callers must not mutate it.
func (v Value) AsSlice() ([]Value, error) {
	if v.kind != SliceKind {
		return nil, typeMismatchErr(v.kind, "slice")
	}
	s, _ := v.raw.([]Value)
	return s, nil
}

// AsMap returns the underlying map[string]Value if v is MapKind, else an
// error. The returned map is the registry's storage — callers must not
// mutate it.
func (v Value) AsMap() (map[string]Value, error) {
	if v.kind != MapKind {
		return nil, typeMismatchErr(v.kind, "map")
	}
	m, _ := v.raw.(map[string]Value)
	return m, nil
}

// AsRaw returns the RawValue if v is RawKind, else an error.
func (v Value) AsRaw() (RawValue, error) {
	if v.kind != RawKind {
		return RawValue{}, typeMismatchErr(v.kind, "raw")
	}
	r, _ := v.raw.(RawValue)
	return r, nil
}

// typeMismatchErr is the common shape used by the As* accessors. It wraps
// ErrTypeMismatch and includes the source and target type names for error
// formatting.
func typeMismatchErr(have ValueKind, want string) error {
	return fmt.Errorf("%w: have %s, want %s", ErrTypeMismatch, have, want)
}

// RawValue holds an un-decoded value as bytes plus a format hint. Used when a
// caller wants to defer decoding — e.g., a config field whose contents are a
// JSON-encoded blob to be parsed by the application, not the registry.
//
// The Format field is a codec name from the registry (e.g., "json", "yaml")
// or any user-supplied string a custom codec recognizes.
type RawValue struct {
	Format string
	Data   []byte
}

// Decode parses RawValue's bytes through the codec named by
// [RawValue.Format], looking the codec up in the package-level
// [DefaultCodecs] set, and assigns the result into v. v MUST be a
// non-nil pointer:
//
//   - A `*map[string]any` (or `*any`) target receives the codec's
//     decoded payload directly.
//   - A `*Value` target receives the payload wrapped via
//     [NewValue].
//   - A pointer to a struct triggers a one-shot struct walk over
//     the decoded map (same rules the bind walker's `format=` tag
//     uses).
//
// Returns a wrapped [ErrUnsupportedFormat] when no codec is
// registered under rv.Format. Codec-decode errors propagate
// untouched.
func (rv RawValue) Decode(v any) error {
	if v == nil {
		return fmt.Errorf("%w: RawValue.Decode: nil target", ErrInvalidPath)
	}
	codecs := DefaultCodecs()
	c, ok := codecs.ByName(rv.Format)
	if !ok {
		return fmt.Errorf("%w: no codec registered for format %q",
			ErrUnsupportedFormat, rv.Format)
	}
	decoded, err := c.Decode(rv.Data)
	if err != nil {
		return fmt.Errorf("recon: RawValue.Decode (%s): %w", rv.Format, err)
	}
	switch dst := v.(type) {
	case *map[string]any:
		*dst = decoded
		return nil
	case *any:
		*dst = decoded
		return nil
	case *Value:
		*dst = NewValue(decoded)
		return nil
	}
	// Struct pointer: use the same MapKind → struct walker as the
	// `format=` tag path.
	rv2 := reflect.ValueOf(v)
	if rv2.Kind() != reflect.Pointer || rv2.IsNil() {
		return fmt.Errorf("%w: RawValue.Decode: %T is not a supported target",
			ErrInvalidPath, v)
	}
	elem := rv2.Elem()
	if elem.Kind() != reflect.Struct {
		return fmt.Errorf("%w: RawValue.Decode: pointer must reference a struct, got %s",
			ErrInvalidPath, elem.Type())
	}
	return coerceStructFromMap(NewValue(decoded), elem)
}
