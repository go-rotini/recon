package recon

import (
	"reflect"
	"testing"
)

func TestNormalizeMap_AcceptsStringKeyedMap(t *testing.T) {
	in := map[string]any{"k": "v"}
	out, ok := normalizeMap(in)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if out["k"] != "v" {
		t.Fatalf("got %v, want k:v", out)
	}
}

func TestNormalizeMap_AcceptsAnyKeyedMap(t *testing.T) {
	// Some decoders return map[any]any; the helper must collapse to
	// map[string]any with keys converted via fmt.Sprint.
	in := map[any]any{
		"str_key": "v1",
		42:        "v2",
	}
	out, ok := normalizeMap(in)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if out["str_key"] != "v1" {
		t.Fatalf("str_key: got %v", out["str_key"])
	}
	if out["42"] != "v2" {
		t.Fatalf("42 → 42: got %v", out["42"])
	}
}

func TestNormalizeMap_RejectsNonMap(t *testing.T) {
	cases := []any{
		"not-a-map",
		42,
		[]any{"a", "b"},
		nil,
	}
	for _, in := range cases {
		if _, ok := normalizeMap(in); ok {
			t.Errorf("normalizeMap(%v) ok=true, want false", in)
		}
	}
}

func TestNormalizeAny_RecursesIntoMapAnyAny(t *testing.T) {
	// A nested map[any]any inside a map[string]any tree must be
	// rewritten to map[string]any.
	in := map[string]any{
		"outer": map[any]any{
			"k": "v",
		},
	}
	out := normalizeAnyMap(in)
	inner, ok := out["outer"].(map[string]any)
	if !ok {
		t.Fatalf("inner is %T, want map[string]any", out["outer"])
	}
	if inner["k"] != "v" {
		t.Fatalf("inner.k = %v", inner["k"])
	}
}

func TestNormalizeAny_RecursesIntoSliceElements(t *testing.T) {
	in := map[string]any{
		"items": []any{
			map[any]any{"id": 1},
			map[any]any{"id": 2},
		},
	}
	out := normalizeAnyMap(in)
	items, _ := out["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items len = %d", len(items))
	}
	for i, el := range items {
		m, ok := el.(map[string]any)
		if !ok {
			t.Fatalf("item %d is %T, want map[string]any", i, el)
		}
		if _, hasID := m["id"]; !hasID {
			t.Fatalf("item %d missing id: %v", i, m)
		}
	}
}

func TestNormalizeAny_LeafValuesPassThrough(t *testing.T) {
	// Scalars are returned unchanged.
	cases := []any{
		"string", int64(42), 3.14, true, nil,
	}
	for _, in := range cases {
		got := normalizeAny(in)
		if !reflect.DeepEqual(got, in) {
			t.Errorf("normalizeAny(%v) = %v", in, got)
		}
	}
}
