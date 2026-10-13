package recon

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// ValueKind identifies the wire-form type of a [Value]: the type the
// source produced, before any conversion to a Go type.
type ValueKind int

// ValueKind constants.
const (
	// NullKind is the absence of a value. A source returning ok=true
	// with NullKind represents "key is set, but to null".
	NullKind ValueKind = iota
	// StringKind is a UTF-8 string.
	StringKind
	// IntKind is a signed integer, stored as int64.
	IntKind
	// FloatKind is a floating-point number, stored as float64.
	FloatKind
	// BoolKind is a boolean.
	BoolKind
	// TimeKind is a time.Time.
	TimeKind
	// DurationKind is a time.Duration.
	DurationKind
	// SliceKind is an ordered list of Values.
	SliceKind
	// MapKind is a map from string to Value.
	MapKind
	// RawKind is bytes plus a format hint ([RawValue]), made by
	// [NewRawValue] for a source that defers parsing.
	RawKind
)

// String returns the lowercase name of the kind.
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

// Value is a typed, source-tagged datum returned by a [Source] lookup.
// Constructors return fresh Values and the As* methods do not mutate;
// once handed to a caller, a Value should be treated as read-only.
type Value struct {
	kind   ValueKind
	raw    any
	source string

	// bigUint marks [StringKind] decimal text made from a uint64 above
	// math.MaxInt64, so encoding and validation see the number again.
	bigUint bool

	// bigInt marks [StringKind] decimal text of an integer outside the
	// int64 and uint64 ranges, which encoding and validation see as a
	// [json.Number].
	bigInt bool
}

// NewValue wraps a Go value, inferring the [ValueKind] from its dynamic
// type. Integer types convert to int64, except a uint or uint64 above
// math.MaxInt64, which becomes its decimal [StringKind] text (it
// still binds to a uint64 field, and [Registry.Save], [Snapshot.AsMap]
// and a validator see it as the uint64). A [json.Number] converts as
// the number it holds; an integer outside the int64 and uint64 ranges
// becomes its decimal [StringKind] text, which Save, AsMap and a
// validator see as the json.Number. float32 widens to float64. Slices and
// arrays (other than []byte) convert element by element to [SliceKind],
// and maps with string keys to [MapKind]. Other types become
// [StringKind] via fmt.Sprint.
func NewValue(v any) Value {
	switch x := v.(type) {
	case nil:
		return Value{kind: NullKind}
	case Value:
		return x
	case json.Number:
		return numberValue(x)
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
	if coll, ok := collectionValue(v); ok {
		return coll
	}
	return Value{kind: StringKind, raw: fmt.Sprint(v)}
}

// collectionValue converts any slice or array other than []byte to
// [SliceKind], and any map with string keys to [MapKind], through
// reflection.
func collectionValue(v any) (Value, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return Value{}, false
		}
		out := make([]Value, rv.Len())
		for i := range out {
			out[i] = NewValue(rv.Index(i).Interface())
		}
		return Value{kind: SliceKind, raw: out}, true
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return Value{}, false
		}
		out := make(map[string]Value, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = NewValue(iter.Value().Interface())
		}
		return Value{kind: MapKind, raw: out}, true
	default:
		return Value{}, false
	}
}

// numericValue handles the integer / float arms of [NewValue].
// Extracted so NewValue stays under the cyclop ceiling.
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
		return uintValue(uint64(x)), true
	case uint8:
		return Value{kind: IntKind, raw: int64(x)}, true
	case uint16:
		return Value{kind: IntKind, raw: int64(x)}, true
	case uint32:
		return Value{kind: IntKind, raw: int64(x)}, true
	case uint64:
		return uintValue(x), true
	case float32:
		return Value{kind: FloatKind, raw: float64(x)}, true
	case float64:
		return Value{kind: FloatKind, raw: x}, true
	}
	return Value{}, false
}

// uintValue wraps u as [IntKind], or as its decimal [StringKind] text
// when it exceeds math.MaxInt64, so no value wraps negative. The text
// still encodes, and reaches a validator, as the uint64.
func uintValue(u uint64) Value {
	if u > math.MaxInt64 {
		return Value{kind: StringKind, raw: strconv.FormatUint(u, 10), bigUint: true}
	}
	return Value{kind: IntKind, raw: int64(u)}
}

// numberValue is [NewValue] for a [json.Number]: an integer as
// [numericValue] or, outside the int64 and uint64 ranges, as marked
// decimal text; anything else as a float64, or as plain text when it
// is not one.
func numberValue(n json.Number) Value {
	s := n.String()
	if isIntegerText(s) {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return Value{kind: IntKind, raw: i}
		}
		if u, err := strconv.ParseUint(s, 10, 64); err == nil {
			return uintValue(u)
		}
		return Value{kind: StringKind, raw: s, bigInt: true}
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return Value{kind: FloatKind, raw: f}
	}
	return Value{kind: StringKind, raw: s}
}

// isIntegerText reports whether s is a decimal integer: an optional
// minus sign and one or more digits.
func isIntegerText(s string) bool {
	s = strings.TrimPrefix(s, "-")
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// NewRawValue wraps undecoded bytes plus a format hint in a Value.
func NewRawValue(format string, data []byte) Value {
	cp := make([]byte, len(data))
	copy(cp, data)
	return Value{kind: RawKind, raw: RawValue{Format: format, Data: cp}}
}

// withSource returns a copy of v tagged with the given source name.
func (v Value) withSource(name string) Value {
	v.source = name
	return v
}

// Kind reports the wire-form type of v.
func (v Value) Kind() ValueKind { return v.kind }

// Source returns the name of the layer that supplied v in a registry
// lookup (a source name, "explicit" or "default"), or "" for a Value
// constructed directly.
func (v Value) Source() string { return v.source }

// IsZero reports whether v carries no underlying datum. A [NullKind]
// Value is zero; an empty string, slice, or map is not.
func (v Value) IsZero() bool { return v.kind == NullKind }

// Any returns the underlying Go value. The concrete type follows the
// kind: string, int64, float64, bool, time.Time, time.Duration,
// []Value, map[string]Value, [RawValue], or nil.
func (v Value) Any() any {
	if v.kind == NullKind {
		return nil
	}
	return v.raw
}

// unwrapValueDeep converts v into the plain-Go shape codecs expect:
// scalars unchanged, [SliceKind] → []any, [MapKind] → map[string]any,
// recursively. Used by save/encode paths where [Value.Any]'s
// map[string]Value / []Value would not round-trip. The text of a
// uint64 above math.MaxInt64 becomes the uint64 again, and that of a
// larger integer a [json.Number].
func unwrapValueDeep(v Value) any {
	switch v.Kind() {
	case StringKind:
		if v.bigUint {
			if u, err := strconv.ParseUint(v.String(), 10, 64); err == nil {
				return u
			}
		}
		if v.bigInt {
			return json.Number(v.String())
		}
		return v.Any()
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

// String returns the canonical string representation of v. Strings are
// returned verbatim and null is "". A whole-number float below 1e21 is
// written without an exponent ("1000000", not "1e+06"), and a time in
// RFC 3339 with nanoseconds; other kinds use fmt.Sprint on the raw
// value. Use [Value.AsString] for the strict accessor.
func (v Value) String() string {
	switch v.kind {
	case NullKind:
		return ""
	case StringKind:
		s, _ := v.raw.(string)
		return s
	case IntKind:
		i, _ := v.raw.(int64)
		return strconv.FormatInt(i, 10)
	case FloatKind:
		f, _ := v.raw.(float64)
		return formatFloat(f)
	case TimeKind:
		t, _ := v.raw.(time.Time)
		return t.Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(v.raw)
	}
}

// formatFloat writes f as fmt.Sprint does, except that a whole number
// below 1e21 in magnitude is written in full rather than with an
// exponent.
func formatFloat(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// AsString returns the string value if v is [StringKind].
func (v Value) AsString() (string, error) {
	if v.kind != StringKind {
		return "", typeMismatchErr(v.kind, "string")
	}
	s, _ := v.raw.(string)
	return s, nil
}

// AsInt64 returns the int64 value if v is [IntKind]. Strings are not
// parsed.
func (v Value) AsInt64() (int64, error) {
	if v.kind != IntKind {
		return 0, typeMismatchErr(v.kind, "int64")
	}
	i, _ := v.raw.(int64)
	return i, nil
}

// AsFloat64 returns the float64 value if v is [FloatKind]. [IntKind]
// values are widened losslessly as a convenience.
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

// AsBool returns the bool value if v is [BoolKind].
func (v Value) AsBool() (bool, error) {
	if v.kind != BoolKind {
		return false, typeMismatchErr(v.kind, "bool")
	}
	b, _ := v.raw.(bool)
	return b, nil
}

// AsTime returns the time.Time value if v is [TimeKind]. [StringKind]
// values are parsed as RFC 3339, or else as a date-time without an
// offset ("2006-01-02T15:04:05", "T" or a space, optional fraction), a
// bare date ("2006-01-02") or a time of day ("15:04:05"), the forms
// TOML calls local date-time, local date and local time; those are
// read as UTC.
func (v Value) AsTime() (time.Time, error) {
	switch v.kind {
	case TimeKind:
		t, _ := v.raw.(time.Time)
		return t, nil
	case StringKind:
		s, _ := v.raw.(string)
		t, err := parseTimeText(s, time.RFC3339, true)
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: parse time %q: %w", ErrCoercion, s, err)
		}
		return t, nil
	default:
		return time.Time{}, typeMismatchErr(v.kind, "time.Time")
	}
}

// timeFallbackLayouts are tried, in order, when a time string does not
// match the primary layout. The last [timeOfDayLayouts] of them are a
// time of day, tried only when no layout was chosen.
var timeFallbackLayouts = [...]string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04",
	"2006-01-02 15:04",
	time.DateOnly,
	"15:04:05.999999999",
	"15:04",
}

const timeOfDayLayouts = 2

// parseTimeText parses s with layout, then with each of
// [timeFallbackLayouts], leaving out the time-of-day forms unless
// timeOfDay. The error is layout's.
func parseTimeText(s, layout string, timeOfDay bool) (time.Time, error) {
	t, err := time.Parse(layout, s)
	if err == nil {
		return t, nil
	}
	alts := timeFallbackLayouts[:]
	if !timeOfDay {
		alts = alts[:len(alts)-timeOfDayLayouts]
	}
	for _, alt := range alts {
		if t2, e2 := time.Parse(alt, s); e2 == nil {
			return t2, nil
		}
	}
	return time.Time{}, err //nolint:wrapcheck // callers wrap with the input.
}

// AsDuration returns the duration value if v is [DurationKind].
// [StringKind] values are parsed via time.ParseDuration; [IntKind]
// values are interpreted as nanoseconds.
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

// AsSlice returns the underlying []Value if v is [SliceKind]. The
// returned slice aliases the registry's storage and must not be mutated.
func (v Value) AsSlice() ([]Value, error) {
	if v.kind != SliceKind {
		return nil, typeMismatchErr(v.kind, "slice")
	}
	s, _ := v.raw.([]Value)
	return s, nil
}

// AsMap returns the underlying map[string]Value if v is [MapKind]. The
// returned map aliases the registry's storage and must not be mutated.
func (v Value) AsMap() (map[string]Value, error) {
	if v.kind != MapKind {
		return nil, typeMismatchErr(v.kind, "map")
	}
	m, _ := v.raw.(map[string]Value)
	return m, nil
}

// AsRaw returns the [RawValue] if v is [RawKind].
func (v Value) AsRaw() (RawValue, error) {
	if v.kind != RawKind {
		return RawValue{}, typeMismatchErr(v.kind, "raw")
	}
	r, _ := v.raw.(RawValue)
	return r, nil
}

func typeMismatchErr(have ValueKind, want string) error {
	return fmt.Errorf("%w: have %s, want %s", ErrTypeMismatch, have, want)
}

// RawValue holds undecoded bytes plus a format hint. Format is a codec
// name registered in the registry's codec set (e.g. "json", "yaml") or
// any string a custom codec recognizes.
type RawValue struct {
	Format string
	Data   []byte
}

// Decode parses rv.Data through the codec named by rv.Format and
// assigns the result into v. v must be a non-nil pointer:
//
//   - *map[string]any or *any receives the decoded payload directly.
//   - *Value receives the payload wrapped via [NewValue].
//   - A pointer to a struct has its fields filled from the top-level
//     keys, matched by the recon / env / json / yaml / toml tag name or
//     else the lowercased field name; tag options other than the name
//     are ignored.
//
// The codec comes from [DefaultCodecs] (see [RawValue.DecodeWithCodecs]).
// Returns wrapped [ErrUnsupportedFormat] when no codec matches
// rv.Format.
func (rv RawValue) Decode(v any) error {
	return rv.DecodeWithCodecs(DefaultCodecs(), v)
}

// DecodeWithCodecs is [RawValue.Decode] looking rv.Format up in codecs
// (nil holds none) instead of [DefaultCodecs].
func (rv RawValue) DecodeWithCodecs(codecs *Codecs, v any) error {
	if v == nil {
		return fmt.Errorf("%w: RawValue.Decode: nil target", ErrInvalidPath)
	}
	c, err := lookupCodec(codecs, rv.Format)
	if err != nil {
		return err
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
