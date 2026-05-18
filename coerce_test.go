package recon

import (
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
	"time"
)

// coerceInto runs coerce against a fresh addressable destination of T
// and returns (the populated T, error). The helper keeps the per-case
// boilerplate down so the test bodies stay focused on the wire-shape
// they're exercising.
func coerceInto[T any](v Value, tag FieldTag) (T, error) {
	var dest T
	rv := reflect.ValueOf(&dest).Elem()
	err := coerce(v, rv, tag)
	return dest, err
}

func TestCoerceBytes_StringDefault(t *testing.T) {
	got, err := coerceInto[[]byte](NewValue("hello"), FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestCoerceBytes_Base64(t *testing.T) {
	got, err := coerceInto[[]byte](NewValue("aGVsbG8="), FieldTag{Base64: true})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestCoerceBytes_Base64Invalid(t *testing.T) {
	_, err := coerceInto[[]byte](NewValue("not-base64-!"), FieldTag{Base64: true})
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion", err)
	}
}

func TestCoerceBytes_Hex(t *testing.T) {
	encoded := hex.EncodeToString([]byte("hello"))
	got, err := coerceInto[[]byte](NewValue(encoded), FieldTag{Hex: true})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestCoerceBytes_HexInvalid(t *testing.T) {
	_, err := coerceInto[[]byte](NewValue("zz"), FieldTag{Hex: true})
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion", err)
	}
}

func TestCoerceBytes_FromSlice(t *testing.T) {
	v := NewValue([]any{int64(104), int64(105)})
	got, err := coerceInto[[]byte](v, FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if string(got) != "hi" {
		t.Fatalf("got %q, want %q", got, "hi")
	}
}

func TestCoerceBytes_FromSliceOutOfRange(t *testing.T) {
	v := NewValue([]any{int64(300)})
	_, err := coerceInto[[]byte](v, FieldTag{})
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion (byte out of range)", err)
	}
}

func TestCoerceBytes_FromSliceNonNumericElement(t *testing.T) {
	v := NewValue([]any{"not-a-byte"})
	_, err := coerceInto[[]byte](v, FieldTag{})
	if err == nil {
		t.Fatal("expected error for non-numeric byte element")
	}
}

func TestCoerceBytes_WrongKind(t *testing.T) {
	v := NewValue(true)
	_, err := coerceInto[[]byte](v, FieldTag{})
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestCoerceDuration_Native(t *testing.T) {
	got, err := coerceInto[time.Duration](NewValue(5*time.Second), FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if got != 5*time.Second {
		t.Fatalf("got %v, want 5s", got)
	}
}

func TestCoerceDuration_FromString(t *testing.T) {
	got, err := coerceInto[time.Duration](NewValue("250ms"), FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if got != 250*time.Millisecond {
		t.Fatalf("got %v, want 250ms", got)
	}
}

func TestCoerceDuration_FromStringInvalid(t *testing.T) {
	_, err := coerceInto[time.Duration](NewValue("not-a-duration"), FieldTag{})
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion", err)
	}
}

func TestCoerceDuration_FromInt(t *testing.T) {
	// Int is interpreted as nanoseconds.
	got, err := coerceInto[time.Duration](NewValue(int64(1_500_000)), FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if got != 1500*time.Microsecond {
		t.Fatalf("got %v, want 1.5ms", got)
	}
}

func TestCoerceDuration_WrongKind(t *testing.T) {
	_, err := coerceInto[time.Duration](NewValue(true), FieldTag{})
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestCoerceTime_FallbackLayouts(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"DateTime", "2026-05-17 12:34:56"},
		{"DateOnly", "2026-05-17"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := coerceInto[time.Time](NewValue(tc.input), FieldTag{})
			if err != nil {
				t.Fatalf("coerce: %v", err)
			}
			if got.IsZero() {
				t.Fatalf("expected non-zero time for %q", tc.input)
			}
		})
	}
}

func TestCoerceTime_CustomLayout(t *testing.T) {
	got, err := coerceInto[time.Time](
		NewValue("17/05/2026"),
		FieldTag{Layout: "02/01/2006"},
	)
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if got.Year() != 2026 || got.Month() != time.May || got.Day() != 17 {
		t.Fatalf("got %v, want 2026-05-17", got)
	}
}

func TestCoerceTime_Unparseable(t *testing.T) {
	_, err := coerceInto[time.Time](NewValue("not-a-date"), FieldTag{})
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion", err)
	}
}

func TestCoerceTime_WrongKind(t *testing.T) {
	_, err := coerceInto[time.Time](NewValue(true), FieldTag{})
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestValueAsInt64_FloatWithFraction(t *testing.T) {
	_, err := valueAsInt64(NewValue(3.14))
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion", err)
	}
}

func TestValueAsInt64_StringInvalid(t *testing.T) {
	_, err := valueAsInt64(NewValue("not-an-int"))
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion", err)
	}
}

func TestValueAsInt64_FromDuration(t *testing.T) {
	got, err := valueAsInt64(NewValue(2 * time.Second))
	if err != nil {
		t.Fatalf("valueAsInt64: %v", err)
	}
	if got != int64(2*time.Second) {
		t.Fatalf("got %d, want %d", got, int64(2*time.Second))
	}
}

func TestValueAsInt64_WrongKind(t *testing.T) {
	_, err := valueAsInt64(NewValue(true))
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestValueAsFloat64_StringInvalid(t *testing.T) {
	_, err := valueAsFloat64(NewValue("not-a-float"))
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion", err)
	}
}

func TestValueAsFloat64_WrongKind(t *testing.T) {
	_, err := valueAsFloat64(NewValue(true))
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestCoerceStruct_FromMapNested(t *testing.T) {
	type Inner struct {
		Name string `recon:"name"`
	}
	v := NewValue(map[string]any{"name": "alice"})
	got, err := coerceInto[Inner](v, FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if got.Name != "alice" {
		t.Fatalf("got %+v, want Name=alice", got)
	}
}

func TestCoerceStruct_NonMapInputErrors(t *testing.T) {
	type Inner struct {
		Name string
	}
	_, err := coerceInto[Inner](NewValue("not-a-map"), FieldTag{})
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestFieldKeyFromTag_SkipReturnsEmpty(t *testing.T) {
	type S struct {
		Hidden string `recon:"-"`
	}
	rt := reflect.TypeOf(S{})
	if got := fieldKeyFromTag(rt.Field(0)); got != "" {
		t.Fatalf("got %q, want empty (skip)", got)
	}
}

func TestFieldKeyFromTag_FallbackChain(t *testing.T) {
	type S struct {
		A string `json:"a_json"`
		B string `yaml:"b_yaml"`
		C string `toml:"c_toml"`
		D string `env:"D_ENV"`
	}
	rt := reflect.TypeOf(S{})
	cases := []struct {
		i    int
		want string
	}{
		{0, "a_json"},
		{1, "b_yaml"},
		{2, "c_toml"},
		{3, "D_ENV"},
	}
	for _, tc := range cases {
		if got := fieldKeyFromTag(rt.Field(tc.i)); got != tc.want {
			t.Fatalf("field %d: got %q, want %q", tc.i, got, tc.want)
		}
	}
}

func TestFieldKeyFromTag_MultiSegmentCollapses(t *testing.T) {
	type S struct {
		X string `recon:"server.port"`
	}
	rt := reflect.TypeOf(S{})
	if got := fieldKeyFromTag(rt.Field(0)); got != "server" {
		t.Fatalf("got %q, want %q (first segment of server.port)", got, "server")
	}
}

func TestCoerceMap_NonStringKeyRejected(t *testing.T) {
	v := NewValue("a=1,b=2")
	var dest map[int]string
	rv := reflect.ValueOf(&dest).Elem()
	err := coerce(v, rv, FieldTag{})
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestCoerceMap_WrongKind(t *testing.T) {
	v := NewValue(true)
	var dest map[string]string
	rv := reflect.ValueOf(&dest).Elem()
	err := coerce(v, rv, FieldTag{})
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestCoerceMap_CustomSeparators(t *testing.T) {
	v := NewValue("a:1;b:2")
	got, err := coerceInto[map[string]string](v, FieldTag{Separator: ";", KVSeparator: ":"})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if got["a"] != "1" || got["b"] != "2" {
		t.Fatalf("got %+v, want a:1 b:2", got)
	}
}

func TestParseStringMap_BareKeyNoValue(t *testing.T) {
	got := parseStringMap("a,b=2,c", ",", "=")
	if v, ok := got["a"]; !ok || v.String() != "" {
		t.Fatalf("bare key 'a': got (%q,%v), want ('',true)", v.String(), ok)
	}
	if v := got["b"]; v.String() != "2" {
		t.Fatalf("'b': got %q, want %q", v.String(), "2")
	}
	if v, ok := got["c"]; !ok || v.String() != "" {
		t.Fatalf("bare key 'c': got (%q,%v), want ('',true)", v.String(), ok)
	}
}

func TestCoerceUint_NegativeRejected(t *testing.T) {
	_, err := coerceInto[uint32](NewValue(int64(-1)), FieldTag{})
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion", err)
	}
}

func TestCoerceUint_Overflow(t *testing.T) {
	_, err := coerceInto[uint8](NewValue(int64(300)), FieldTag{})
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion (uint8 overflow)", err)
	}
}

func TestCoerceInt_Overflow(t *testing.T) {
	_, err := coerceInto[int8](NewValue(int64(200)), FieldTag{})
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion (int8 overflow)", err)
	}
}

func TestCoerceFloat_Overflow(t *testing.T) {
	_, err := coerceInto[float32](NewValue(1e40), FieldTag{})
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion (float32 overflow)", err)
	}
}

func TestCoerceFloat_FromString(t *testing.T) {
	got, err := coerceInto[float64](NewValue("3.14"), FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if got != 3.14 {
		t.Fatalf("got %v, want 3.14", got)
	}
}

func TestCoerceFloat_StringInvalid(t *testing.T) {
	_, err := coerceInto[float64](NewValue("not-a-float"), FieldTag{})
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion", err)
	}
}

func TestCoerceBool_FromString(t *testing.T) {
	got, err := coerceInto[bool](NewValue("true"), FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if !got {
		t.Fatal("got false, want true")
	}
}

func TestCoerceBool_StringInvalid(t *testing.T) {
	_, err := coerceInto[bool](NewValue("not-a-bool"), FieldTag{})
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion", err)
	}
}

func TestCoerceBool_WrongKind(t *testing.T) {
	_, err := coerceInto[bool](NewValue(int64(1)), FieldTag{})
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestCoerceString_FromBool(t *testing.T) {
	// Non-string kinds flatten through fmt.Sprint.
	got, err := coerceInto[string](NewValue(true), FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if got != "true" {
		t.Fatalf("got %q, want %q", got, "true")
	}
}

func TestCoerceString_FromInt(t *testing.T) {
	got, err := coerceInto[string](NewValue(int64(42)), FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if got != "42" {
		t.Fatalf("got %q, want %q", got, "42")
	}
}

func TestCoerceInt_StringInvalid(t *testing.T) {
	_, err := coerceInto[int](NewValue("not-an-int"), FieldTag{})
	if !errors.Is(err, ErrCoercion) {
		t.Fatalf("err = %v, want wrapping ErrCoercion", err)
	}
}

func TestCoerceSlice_FromStringWithSeparator(t *testing.T) {
	got, err := coerceInto[[]string](NewValue("a;b;c"), FieldTag{Separator: ";"})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("got %v, want [a b c]", got)
	}
}

func TestCoerceSlice_EmptyStringYieldsEmpty(t *testing.T) {
	got, err := coerceInto[[]string](NewValue(""), FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty slice", got)
	}
}

func TestCoerceSlice_WrongKind(t *testing.T) {
	_, err := coerceInto[[]string](NewValue(true), FieldTag{})
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

// textUnmarshaler satisfies encoding.TextUnmarshaler so coerce's
// hook-dispatch fast path is exercised.
type textUnmarshaler struct{ v string }

func (t *textUnmarshaler) UnmarshalText(text []byte) error {
	t.v = "tu:" + string(text)
	return nil
}

func TestCoerce_TextUnmarshalerHook(t *testing.T) {
	got, err := coerceInto[textUnmarshaler](NewValue("hello"), FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if got.v != "tu:hello" {
		t.Fatalf("got %q, want tu:hello", got.v)
	}
}

type envUnmarshaler struct{ v string }

func (e *envUnmarshaler) UnmarshalEnv(text string) error {
	e.v = "env:" + text
	return nil
}

func TestCoerce_EnvUnmarshalerHook(t *testing.T) {
	got, err := coerceInto[envUnmarshaler](NewValue("hello"), FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if got.v != "env:hello" {
		t.Fatalf("got %q, want env:hello", got.v)
	}
}

type reconUnmarshaler struct{ v string }

func (r *reconUnmarshaler) UnmarshalRecon(v Value) error {
	r.v = "recon:" + v.String()
	return nil
}

func TestCoerce_ReconUnmarshalerHook(t *testing.T) {
	got, err := coerceInto[reconUnmarshaler](NewValue("hello"), FieldTag{})
	if err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if got.v != "recon:hello" {
		t.Fatalf("got %q, want recon:hello", got.v)
	}
}

func TestCoerce_PointerAllocation(t *testing.T) {
	type S struct{}
	var dest *int
	rv := reflect.ValueOf(&dest).Elem()
	if err := coerce(NewValue(int64(42)), rv, FieldTag{}); err != nil {
		t.Fatalf("coerce: %v", err)
	}
	if dest == nil {
		t.Fatal("pointer was not allocated")
	}
	if *dest != 42 {
		t.Fatalf("got %d, want 42", *dest)
	}
	_ = S{}
}

func TestCoerce_UnsupportedKind(t *testing.T) {
	// reflect.Chan isn't in the dispatch table.
	var dest chan int
	rv := reflect.ValueOf(&dest).Elem()
	err := coerce(NewValue("hello"), rv, FieldTag{})
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}
