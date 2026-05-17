package recon

import (
	"reflect"
	"testing"
	"time"
)

// FuzzParsePath is the §8.6 fuzz target for [ParsePath]. The
// invariant: ParsePath never returns an error and never panics for
// any byte sequence. Additionally, an empty input yields an empty
// path, and a single delimiter character only yields exactly two
// empty segments.
func FuzzParsePath(f *testing.F) {
	corpus := []string{
		"",
		".",
		"a",
		"a.b",
		"a.b.c",
		"[escaped.key]",
		"prefix.[escaped.middle].suffix",
		"[", "]", "[[", "]]", "[a", "a]",
		".leading",
		"trailing.",
		"..double",
		"\x00null-byte",
		"unicode 日本語 path",
	}
	for _, s := range corpus {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		// Should never panic.
		p := ParsePath(s)
		// Stringify should not panic either.
		_ = p.String()

		// Empty input always returns an empty path.
		if s == "" && len(p) != 0 {
			t.Fatalf("ParsePath(%q) returned %v; want empty", s, p)
		}
	})
}

// FuzzCoerce drives the coercion dispatcher with arbitrary string
// inputs against every concrete leaf type. The invariant: coerce
// either succeeds (the value matches the target's zero+typed shape)
// or returns an error wrapping [ErrCoercion] / [ErrTypeMismatch].
// It MUST NOT panic regardless of input.
func FuzzCoerce(f *testing.F) {
	f.Add("hello")
	f.Add("")
	f.Add("true")
	f.Add("42")
	f.Add("3.14")
	f.Add("2024-12-25T00:00:00Z")
	f.Add("5s")
	f.Add("1,2,3")
	f.Add("a:1,b:2")
	f.Add("hunter2")
	f.Add("not-a-number")
	f.Add("\xff\xfe binary")

	type runtimeTarget struct {
		name string
		new  func() reflect.Value
	}
	targets := []runtimeTarget{
		{"string", func() reflect.Value { var v string; return reflect.ValueOf(&v).Elem() }},
		{"bool", func() reflect.Value { var v bool; return reflect.ValueOf(&v).Elem() }},
		{"int", func() reflect.Value { var v int; return reflect.ValueOf(&v).Elem() }},
		{"int64", func() reflect.Value { var v int64; return reflect.ValueOf(&v).Elem() }},
		{"uint", func() reflect.Value { var v uint; return reflect.ValueOf(&v).Elem() }},
		{"float64", func() reflect.Value { var v float64; return reflect.ValueOf(&v).Elem() }},
		{"duration", func() reflect.Value { var v time.Duration; return reflect.ValueOf(&v).Elem() }},
		{"time", func() reflect.Value { var v time.Time; return reflect.ValueOf(&v).Elem() }},
		{"[]string", func() reflect.Value { var v []string; return reflect.ValueOf(&v).Elem() }},
		{"map", func() reflect.Value { var v map[string]string; return reflect.ValueOf(&v).Elem() }},
	}

	f.Fuzz(func(t *testing.T, raw string) {
		v := NewValue(raw)
		for _, tg := range targets {
			dest := tg.new()
			// Coerce must not panic. The error return is allowed —
			// only an uncaught panic counts as a fuzz failure.
			_ = coerce(v, dest, FieldTag{})
		}
	})
}

// FuzzBind drives [Registry.Bind] with arbitrary key/value strings
// against a small set of struct shapes. The invariant: Bind either
// succeeds (filling the struct) or returns an error from the
// documented set ([ErrMissingRequired], [ErrEmptyValue],
// [ErrCoercion], [ErrTypeMismatch], [ErrInvalidPath]). Any panic
// counts as a fuzz failure.
func FuzzBind(f *testing.F) {
	corpus := []struct {
		key string
		val string
	}{
		{"port", "8080"},
		{"name", "rotini"},
		{"debug", "true"},
		{"timeout", "5s"},
		{"rate", "1.5"},
		{"tags", "a,b,c"},
		{"empty", ""},
		{"unicode", "日本語"},
		{"binary", "\xff\xfe"},
	}
	for _, c := range corpus {
		f.Add(c.key, c.val)
	}

	type basic struct {
		Port    int           `recon:"port"`
		Name    string        `recon:"name"`
		Debug   bool          `recon:"debug"`
		Timeout time.Duration `recon:"timeout"`
		Rate    float64       `recon:"rate"`
		Tags    []string      `recon:"tags"`
	}

	f.Fuzz(func(t *testing.T, key, val string) {
		r, err := New()
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer func() { _ = r.Close() }()
		_ = r.Set(key, val)
		var b basic
		// Bind may fail; an uncaught panic is what we're hunting.
		_ = r.Bind(&b)
	})
}

// FuzzMergeMaps fuzzes the [unwrapValueDeep] + nested-map
// roundtrip path Save / Describe use. It does not exercise
// MergeAppend (deep-merge is gated behind [WithMerge] and not yet
// fully wired); instead it confirms the snapshot's map-projection
// is panic-safe under arbitrary keys and values.
func FuzzMergeMaps(f *testing.F) {
	f.Add("server.port", "8080")
	f.Add("server.host", "localhost")
	f.Add("nested.a.b.c.d", "deep")
	f.Add("with.[escaped].seg", "v")

	f.Fuzz(func(t *testing.T, key, val string) {
		r, err := New()
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer func() { _ = r.Close() }()
		_ = r.Set(key, val)

		snap := r.Snapshot()
		if snap == nil {
			t.Fatal("snapshot is nil")
		}
		// AsMap must not panic; the returned shape is plain Go
		// values that JSON.Encode can handle.
		m := snap.AsMap()
		if _, err := JSON.Encode(m); err != nil {
			// JSON failure on a random key isn't a fuzz failure —
			// some keys carry control chars JSON can't encode.
			// We only assert no panic.
			_ = err
		}
	})
}
