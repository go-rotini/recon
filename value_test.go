package recon

import (
	"errors"
	"testing"
	"time"
)

func TestValueKind_String(t *testing.T) {
	cases := []struct {
		k    ValueKind
		want string
	}{
		{NullKind, "null"},
		{StringKind, "string"},
		{IntKind, "int"},
		{FloatKind, "float"},
		{BoolKind, "bool"},
		{TimeKind, "time"},
		{DurationKind, "duration"},
		{SliceKind, "slice"},
		{MapKind, "map"},
		{RawKind, "raw"},
		{ValueKind(99), "unknown"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := c.k.String(); got != c.want {
				t.Errorf("kind %d → %q, want %q", c.k, got, c.want)
			}
		})
	}
}

func TestNewValue_Scalars(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want ValueKind
	}{
		{"nil", nil, NullKind},
		{"string", "hi", StringKind},
		{"bool-true", true, BoolKind},
		{"bool-false", false, BoolKind},
		{"int", 42, IntKind},
		{"int8", int8(8), IntKind},
		{"int16", int16(16), IntKind},
		{"int32", int32(32), IntKind},
		{"int64", int64(64), IntKind},
		{"uint", uint(1), IntKind},
		{"uint8", uint8(8), IntKind},
		{"uint16", uint16(16), IntKind},
		{"uint32", uint32(32), IntKind},
		{"uint64", uint64(64), IntKind},
		{"float32", float32(1.5), FloatKind},
		{"float64", 1.5, FloatKind},
		{"time", time.Now(), TimeKind},
		{"duration", time.Second, DurationKind},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := NewValue(c.in)
			if v.Kind() != c.want {
				t.Errorf("NewValue(%v).Kind() = %s, want %s", c.in, v.Kind(), c.want)
			}
		})
	}
}

func TestNewValue_FallbackString(t *testing.T) {
	// Unrecognized types fall through to StringKind via fmt.Sprint.
	type custom struct{ N int }
	v := NewValue(custom{N: 7})
	if v.Kind() != StringKind {
		t.Errorf("Kind = %s, want string fallback", v.Kind())
	}
}

func TestNewValue_PreservesValueIdentity(t *testing.T) {
	// NewValue(Value) is the identity.
	orig := NewValue("x")
	if got := NewValue(orig); got != orig {
		t.Errorf("NewValue(Value) round-trip lost identity")
	}
}

func TestNewValue_Slice(t *testing.T) {
	v := NewValue([]any{1, "two", true})
	if v.Kind() != SliceKind {
		t.Fatalf("kind = %s", v.Kind())
	}
	s, err := v.AsSlice()
	if err != nil || len(s) != 3 {
		t.Fatalf("AsSlice = %v, %v", s, err)
	}
	if s[0].Kind() != IntKind || s[1].Kind() != StringKind || s[2].Kind() != BoolKind {
		t.Errorf("slice elements lost their kinds: %v", s)
	}
}

func TestNewValue_SliceOfValues(t *testing.T) {
	// []Value is copied, not aliased.
	in := []Value{NewValue("a"), NewValue("b")}
	v := NewValue(in)
	out, _ := v.AsSlice()
	out[0] = NewValue("X")
	if got, _ := in[0].AsString(); got != "a" {
		t.Errorf("[]Value aliasing detected: in[0]=%q", got)
	}
}

func TestNewValue_Map(t *testing.T) {
	v := NewValue(map[string]any{"a": 1, "b": "two"})
	if v.Kind() != MapKind {
		t.Fatalf("kind = %s", v.Kind())
	}
	m, _ := v.AsMap()
	if m["a"].Kind() != IntKind || m["b"].Kind() != StringKind {
		t.Errorf("map element kinds lost")
	}
}

func TestNewValue_MapOfValues(t *testing.T) {
	// map[string]Value is copied, not aliased.
	in := map[string]Value{"k": NewValue("v")}
	v := NewValue(in)
	out, _ := v.AsMap()
	out["k"] = NewValue("X")
	if got, _ := in["k"].AsString(); got != "v" {
		t.Errorf("map[string]Value aliasing detected: in[k]=%q", got)
	}
}

func TestNewRawValue(t *testing.T) {
	v := NewRawValue("yaml", []byte("k: v"))
	if v.Kind() != RawKind {
		t.Fatalf("kind = %s, want raw", v.Kind())
	}
	rv, err := v.AsRaw()
	if err != nil || rv.Format != "yaml" || string(rv.Data) != "k: v" {
		t.Errorf("AsRaw = (%+v, %v)", rv, err)
	}
	// Stored bytes must be a copy (mutating the caller's slice doesn't change us).
	src := []byte("hello")
	v2 := NewRawValue("plain", src)
	src[0] = 'X'
	rv2, _ := v2.AsRaw()
	if string(rv2.Data) != "hello" {
		t.Errorf("NewRawValue aliases the source slice: %q", rv2.Data)
	}
}

func TestValue_Source(t *testing.T) {
	v := NewValue("x")
	if v.Source() != "" {
		t.Errorf("fresh value has source %q", v.Source())
	}
	v2 := v.withSource("env")
	if v2.Source() != "env" {
		t.Errorf("withSource lost the name: %q", v2.Source())
	}
	if v.Source() != "" {
		t.Errorf("withSource mutated the receiver: %q", v.Source())
	}
}

func TestValue_IsZero(t *testing.T) {
	if !NewValue(nil).IsZero() {
		t.Error("nil should be zero")
	}
	if NewValue("").IsZero() {
		t.Error("empty string is set, not zero")
	}
	if NewValue(0).IsZero() {
		t.Error("int 0 is set, not zero")
	}
}

func TestValue_Any(t *testing.T) {
	if NewValue(nil).Any() != nil {
		t.Error("null Value.Any() should be nil")
	}
	if got := NewValue(42).Any(); got != int64(42) {
		t.Errorf("Any() = %T(%v), want int64(42)", got, got)
	}
}

func TestValue_String(t *testing.T) {
	if NewValue(nil).String() != "" {
		t.Errorf("null Value.String() should be empty")
	}
	if NewValue("hi").String() != "hi" {
		t.Errorf("string Value.String() = %q", NewValue("hi").String())
	}
	if NewValue(42).String() != "42" {
		t.Errorf("int Value.String() = %q", NewValue(42).String())
	}
}

func TestValue_AsString(t *testing.T) {
	if got, err := NewValue("ok").AsString(); err != nil || got != "ok" {
		t.Errorf("AsString = (%q, %v)", got, err)
	}
	if _, err := NewValue(42).AsString(); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("AsString on int: want ErrTypeMismatch, got %v", err)
	}
}

func TestValue_AsInt64(t *testing.T) {
	if got, err := NewValue(42).AsInt64(); err != nil || got != 42 {
		t.Errorf("AsInt64 = (%d, %v)", got, err)
	}
	if _, err := NewValue("x").AsInt64(); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("AsInt64 on string: want ErrTypeMismatch, got %v", err)
	}
}

func TestValue_AsFloat64(t *testing.T) {
	if got, err := NewValue(1.5).AsFloat64(); err != nil || got != 1.5 {
		t.Errorf("AsFloat64 = (%v, %v)", got, err)
	}
	// int widens to float64.
	if got, err := NewValue(42).AsFloat64(); err != nil || got != 42.0 {
		t.Errorf("AsFloat64(int) = (%v, %v)", got, err)
	}
	if _, err := NewValue("x").AsFloat64(); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("AsFloat64 on string: want ErrTypeMismatch, got %v", err)
	}
}

func TestValue_AsBool(t *testing.T) {
	if got, err := NewValue(true).AsBool(); err != nil || !got {
		t.Errorf("AsBool = (%v, %v)", got, err)
	}
	if _, err := NewValue("yes").AsBool(); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("AsBool on string: want ErrTypeMismatch, got %v", err)
	}
}

func TestValue_AsTime(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	if got, err := NewValue(now).AsTime(); err != nil || !got.Equal(now) {
		t.Errorf("AsTime = (%v, %v)", got, err)
	}
	// RFC3339 string parses.
	s := now.Format(time.RFC3339)
	if got, err := NewValue(s).AsTime(); err != nil || !got.Equal(now) {
		t.Errorf("AsTime(RFC3339) = (%v, %v)", got, err)
	}
	// Bad string surfaces as a coercion error.
	if _, err := NewValue("not a time").AsTime(); !errors.Is(err, ErrCoercion) {
		t.Errorf("AsTime(bad): want ErrCoercion, got %v", err)
	}
	// Wrong kind errors out.
	if _, err := NewValue(42).AsTime(); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("AsTime(int): want ErrTypeMismatch, got %v", err)
	}
}

func TestValue_AsDuration(t *testing.T) {
	if got, err := NewValue(5 * time.Second).AsDuration(); err != nil || got != 5*time.Second {
		t.Errorf("AsDuration = (%v, %v)", got, err)
	}
	if got, err := NewValue("1m30s").AsDuration(); err != nil || got != 90*time.Second {
		t.Errorf("AsDuration(string) = (%v, %v)", got, err)
	}
	if got, err := NewValue(int64(1_000_000_000)).AsDuration(); err != nil || got != time.Second {
		t.Errorf("AsDuration(int) = (%v, %v)", got, err)
	}
	if _, err := NewValue("bogus").AsDuration(); !errors.Is(err, ErrCoercion) {
		t.Errorf("AsDuration(bad): want ErrCoercion, got %v", err)
	}
	if _, err := NewValue(true).AsDuration(); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("AsDuration(bool): want ErrTypeMismatch, got %v", err)
	}
}

func TestValue_AsSliceAsMap_TypeMismatch(t *testing.T) {
	if _, err := NewValue(42).AsSlice(); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("AsSlice(int): want ErrTypeMismatch, got %v", err)
	}
	if _, err := NewValue(42).AsMap(); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("AsMap(int): want ErrTypeMismatch, got %v", err)
	}
	if _, err := NewValue(42).AsRaw(); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("AsRaw(int): want ErrTypeMismatch, got %v", err)
	}
}

func TestRawValue_DecodePhase4Deferred(t *testing.T) {
	// RawValue.Decode is a stub until Phase 4; verify it returns the
	// documented sentinel so dependent code can plan around it.
	rv := RawValue{Format: "json", Data: []byte(`{}`)}
	err := rv.Decode(nil)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("Decode stub: want ErrUnsupportedFormat, got %v", err)
	}
}
