package recon

import (
	"slices"
	"testing"
)

func TestMergeAppend_ConcatsSlices(t *testing.T) {
	high := NewMapSource("high", map[string]any{
		"tags": []any{"hi-1", "hi-2"},
	})
	low := NewMapSource("low", map[string]any{
		"tags": []any{"lo-1", "lo-2"},
	})
	r := newRegistry(t,
		WithSources(high, low),
		WithMerge(MergeAppend),
	)

	tags, _, err := r.GetStringSlice("tags")
	if err != nil {
		t.Fatalf("GetStringSlice: %v", err)
	}
	want := []string{"lo-1", "lo-2", "hi-1", "hi-2"}
	if !slices.Equal(tags, want) {
		t.Fatalf("tags=%v, want %v", tags, want)
	}
}

func TestMergeAppend_DeepMergesMaps(t *testing.T) {
	high := NewMapSource("high", map[string]any{
		"app": map[string]any{
			"port": 9090,
			"host": "hi-host",
		},
	})
	low := NewMapSource("low", map[string]any{
		"app": map[string]any{
			"port":  8080,
			"label": "lo-only",
		},
	})
	r := newRegistry(t,
		WithSources(high, low),
		WithMerge(MergeAppend),
	)

	// Map keys present in only one source survive; keys in both
	// follow MergeAppend (scalar shadow).
	if v, _, _ := r.GetString("app.host"); v != "hi-host" {
		t.Fatalf("app.host=%q, want hi-host (high-only)", v)
	}
	if v, _, _ := r.GetString("app.label"); v != "lo-only" {
		t.Fatalf("app.label=%q, want lo-only (low-only survives merge)", v)
	}
	if v, _, _ := r.GetInt("app.port"); v != 9090 {
		t.Fatalf("app.port=%d, want 9090 (scalar shadow)", v)
	}
}

func TestMergeAppend_ScalarStillShadows(t *testing.T) {
	high := NewMapSource("high", map[string]any{"port": 9090})
	low := NewMapSource("low", map[string]any{"port": 8080})
	r := newRegistry(t,
		WithSources(high, low),
		WithMerge(MergeAppend),
	)

	if v, _, _ := r.GetInt("port"); v != 9090 {
		t.Fatalf("port=%d, want 9090 (scalar shadow under MergeAppend)", v)
	}
}

func TestMergeAppend_TypeMismatchHighWins(t *testing.T) {
	high := NewMapSource("high", map[string]any{"k": "string"})
	low := NewMapSource("low", map[string]any{"k": []any{"a", "b"}})
	r := newRegistry(t,
		WithSources(high, low),
		WithMerge(MergeAppend),
	)

	v, _, _ := r.Get("k")
	if v.Kind() != StringKind {
		t.Fatalf("k.Kind=%v, want StringKind (high wins on type mismatch)", v.Kind())
	}
}

func TestMergeAppend_DefaultLayerJoinsAsLowest(t *testing.T) {
	src := NewMapSource("s", map[string]any{
		"tags": []any{"src"},
	})
	r := newRegistry(t,
		WithSource(src),
		WithMerge(MergeAppend),
	)
	_ = r.SetDefault("tags", []any{"default"})

	tags, _, _ := r.GetStringSlice("tags")
	want := []string{"default", "src"}
	if !slices.Equal(tags, want) {
		t.Fatalf("tags=%v, want %v", tags, want)
	}
}

func TestMergeAppend_ProvenanceListsEveryContributor(t *testing.T) {
	high := NewMapSource("high", map[string]any{"tags": []any{"hi"}})
	low := NewMapSource("low", map[string]any{"tags": []any{"lo"}})
	r := newRegistry(t,
		WithSources(high, low),
		WithMerge(MergeAppend),
	)

	srcs := r.Snapshot().SourceFor(MakePath("tags"))
	want := []string{"high", "low"}
	if !slices.Equal(srcs, want) {
		t.Fatalf("provenance=%v, want %v", srcs, want)
	}
	// The winner-source on the resolved Value is the highest-
	// precedence contributor.
	v, _, _ := r.Get("tags")
	if v.Source() != "high" {
		t.Fatalf("Value.Source=%q, want high", v.Source())
	}
}

func TestMergeAppend_ExplicitStillShadowsEverything(t *testing.T) {
	src := NewMapSource("s", map[string]any{
		"tags": []any{"src"},
	})
	r := newRegistry(t,
		WithSource(src),
		WithMerge(MergeAppend),
	)
	_ = r.Set("tags", []any{"explicit-only"})

	// Set is the explicit-override layer; even under MergeAppend it
	// shadows the source-chain merge.
	tags, _, _ := r.GetStringSlice("tags")
	if !slices.Equal(tags, []string{"explicit-only"}) {
		t.Fatalf("tags=%v, want [explicit-only]", tags)
	}
}

func TestMergeAppend_PinStillBypassesMerge(t *testing.T) {
	high := NewMapSource("high", map[string]any{"tags": []any{"hi"}})
	low := NewMapSource("low", map[string]any{"tags": []any{"lo"}})
	r := newRegistry(t,
		WithSources(high, low),
		WithMerge(MergeAppend),
	)
	if err := r.PinSource("tags", "low"); err != nil {
		t.Fatal(err)
	}

	tags, _, _ := r.GetStringSlice("tags")
	if !slices.Equal(tags, []string{"lo"}) {
		t.Fatalf("tags=%v, want [lo] (pin bypasses merge)", tags)
	}
}

func TestMergeAppend_NestedMapsRecurse(t *testing.T) {
	high := NewMapSource("high", map[string]any{
		"app": map[string]any{
			"server": map[string]any{
				"hosts": []any{"hi-1"},
			},
		},
	})
	low := NewMapSource("low", map[string]any{
		"app": map[string]any{
			"server": map[string]any{
				"hosts": []any{"lo-1"},
				"port":  8080,
			},
		},
	})
	r := newRegistry(t,
		WithSources(high, low),
		WithMerge(MergeAppend),
	)

	hosts, _, _ := r.GetStringSlice("app.server.hosts")
	want := []string{"lo-1", "hi-1"}
	if !slices.Equal(hosts, want) {
		t.Fatalf("app.server.hosts=%v, want %v", hosts, want)
	}
	if v, _, _ := r.GetInt("app.server.port"); v != 8080 {
		t.Fatalf("app.server.port=%d, want 8080 (low-only survives nested merge)", v)
	}
}

func TestMergeShadow_StillFirstSetWins(t *testing.T) {
	// Default MergeShadow must keep the existing behavior — assert
	// it explicitly.
	high := NewMapSource("high", map[string]any{"tags": []any{"hi"}})
	low := NewMapSource("low", map[string]any{"tags": []any{"lo"}})
	r := newRegistry(t, WithSources(high, low))

	tags, _, _ := r.GetStringSlice("tags")
	if !slices.Equal(tags, []string{"hi"}) {
		t.Fatalf("MergeShadow tags=%v, want [hi]", tags)
	}
}

func TestMergeValues_SliceMapMismatchHighWins(t *testing.T) {
	// Slice + Map is a type mismatch; default branch returns hi.
	lo := NewValue([]any{"a", "b"})
	hi := NewValue(map[string]any{"k": "v"})
	got := mergeValues(lo, hi)
	if got.Kind() != MapKind {
		t.Fatalf("kind=%v, want MapKind (hi)", got.Kind())
	}
}

func TestMergeValues_ScalarHighWins(t *testing.T) {
	lo := NewValue(int64(1))
	hi := NewValue("two")
	got := mergeValues(lo, hi)
	if got.Kind() != StringKind {
		t.Fatalf("kind=%v, want StringKind (hi)", got.Kind())
	}
}

func TestMergeValues_MapWithLoOnlyKey(t *testing.T) {
	// Keys present only in lo survive untouched.
	lo := NewValue(map[string]any{"only_lo": "kept", "shared": "lo"})
	hi := NewValue(map[string]any{"only_hi": "new", "shared": "hi"})
	got := mergeValues(lo, hi)
	m, _ := got.AsMap()
	if m["only_lo"].String() != "kept" {
		t.Fatalf("only_lo lost: %+v", m)
	}
	if m["only_hi"].String() != "new" {
		t.Fatalf("only_hi missing: %+v", m)
	}
	if m["shared"].String() != "hi" {
		t.Fatalf("shared scalar should be hi: %+v", m)
	}
}

func TestMergeValues_NullLoHiWins(t *testing.T) {
	lo := NewValue(nil)
	hi := NewValue("only-hi")
	got := mergeValues(lo, hi)
	if got.String() != "only-hi" {
		t.Fatalf("got %q, want only-hi", got.String())
	}
}
