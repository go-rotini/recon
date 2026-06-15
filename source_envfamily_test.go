package recon

import (
	"reflect"
	"testing"
)

func TestEnvFamilySource_Get(t *testing.T) {
	t.Setenv("ACME_HTTP__RETRY__MAX", "9")
	t.Setenv("ACME_HTTP__TIMEOUT", "30")
	s := NewEnvFamilySource("envfamily", EnvFamily{
		Target: MakePath("http"), Base: "ACME_HTTP", Separator: "__",
	})

	v, ok, err := s.Get(MakePath("http"))
	if err != nil || !ok {
		t.Fatalf("Get(http) ok=%v err=%v", ok, err)
	}
	m, err := v.AsMap()
	if err != nil {
		t.Fatalf("AsMap: %v", err)
	}
	if _, has := m["retry"]; !has {
		t.Fatalf("family map missing 'retry': %v", m)
	}
	if _, has := m["timeout"]; !has {
		t.Fatalf("family map missing 'timeout': %v", m)
	}

	keys := s.Keys()
	if len(keys) != 1 || !keys[0].Equal(MakePath("http")) {
		t.Fatalf("Keys()=%v, want [http]", keys)
	}
}

func TestEnvFamilySource_Empty(t *testing.T) {
	s := NewEnvFamilySource("envfamily", EnvFamily{
		Target: MakePath("http"), Base: "NOPE_HTTP", Separator: "__",
	})
	if _, ok, _ := s.Get(MakePath("http")); ok {
		t.Fatal("an empty family should not resolve")
	}
	if keys := s.Keys(); len(keys) != 0 {
		t.Fatalf("Keys()=%v, want empty for an unset family", keys)
	}
}

// TestEnvFamilySource_BindsNestedMapField is the end-to-end check: a
// map[string]any field binds the whole family through the normal Bind path,
// with nested segments expanded.
func TestEnvFamilySource_BindsNestedMapField(t *testing.T) {
	t.Setenv("ACME_HTTP__RETRY__MAX", "9")
	t.Setenv("ACME_HTTP__TIMEOUT", "30")
	reg, err := New(WithSources(NewEnvFamilySource("envfamily", EnvFamily{
		Target: MakePath("http"), Base: "ACME_HTTP", Separator: "__",
	})))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var cfg struct {
		HTTP map[string]any `recon:"http"`
	}
	if err := reg.Bind(&cfg); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	retry, ok := cfg.HTTP["retry"].(map[string]any)
	if !ok {
		t.Fatalf("http.retry not a nested map: %#v", cfg.HTTP)
	}
	if retry["max"] != "9" {
		t.Fatalf("http.retry.max=%v, want 9", retry["max"])
	}
	if cfg.HTTP["timeout"] != "30" {
		t.Fatalf("http.timeout=%v, want 30", cfg.HTTP["timeout"])
	}
}

// TestCoerceInterface covers the empty-interface coercion that lets a family's
// MapKind value (and any-typed fields generally) bind: scalars take their Go
// form, while MapKind / SliceKind recurse into nested map[string]any / []any.
func TestCoerceInterface(t *testing.T) {
	cases := []struct {
		name string
		v    Value
		want any
	}{
		{"scalar", NewValue("x"), "x"},
		{"slice", NewValue([]any{"a", "b"}), []any{"a", "b"}},
		{"nestedMap", NewValue(map[string]any{"k": map[string]any{"n": "v"}}),
			map[string]any{"k": map[string]any{"n": "v"}}},
		{"null", NewValue(nil), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var dst any
			if err := coerceInterface(c.v, reflect.ValueOf(&dst).Elem()); err != nil {
				t.Fatalf("coerceInterface: %v", err)
			}
			if !reflect.DeepEqual(dst, c.want) {
				t.Fatalf("got %#v, want %#v", dst, c.want)
			}
		})
	}
}
