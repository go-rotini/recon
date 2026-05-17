package recon

import (
	"slices"
	"strings"
	"testing"
)

func TestSnapshot_NilSafe(t *testing.T) {
	var s *Snapshot
	if v, ok := s.Get(MakePath("anything")); ok {
		t.Fatalf("nil-Snapshot.Get ok=true, v=%v", v)
	}
	if got := s.Keys(); got != nil {
		t.Fatalf("nil-Snapshot.Keys=%v", got)
	}
	if got := s.SourceFor(MakePath("k")); got != nil {
		t.Fatalf("nil-Snapshot.SourceFor=%v", got)
	}
	if got := s.AsMap(); len(got) != 0 {
		t.Fatalf("nil-Snapshot.AsMap=%v", got)
	}
	if got := s.String(); got != "recon.Snapshot{}" {
		t.Fatalf("nil-Snapshot.String=%q", got)
	}
}

func TestSnapshot_BuildIncludesAllLayers(t *testing.T) {
	src := NewMapSource("src", map[string]any{
		"server": map[string]any{"port": 8080},
	})
	r := newRegistry(t, WithSource(src))
	_ = r.Set("server.host", "localhost")
	_ = r.SetDefault("server.timeout", "5s")

	snap := r.Snapshot()
	keys := snap.Keys()
	got := make([]string, len(keys))
	for i, p := range keys {
		got[i] = p.String()
	}
	want := []string{"server.host", "server.port", "server.timeout"}
	if !slices.Equal(got, want) {
		t.Fatalf("Keys=%v, want %v", got, want)
	}
}

func TestSnapshot_SourceFor_RecordsAllLayers(t *testing.T) {
	high := NewMapSource("high", map[string]any{"k": "high"})
	low := NewMapSource("low", map[string]any{"k": "low"})
	r := newRegistry(t, WithSources(high, low))
	_ = r.SetDefault("k", "fallback")

	snap := r.Snapshot()
	srcs := snap.SourceFor(MakePath("k"))
	// Default is below the source chain; it should NOT appear when sources
	// already supply the key (resolveKey skips defaults on a hit).
	want := []string{"high", "low"}
	if !slices.Equal(srcs, want) {
		t.Fatalf("SourceFor(k)=%v, want %v", srcs, want)
	}

	// Default-only key reports just "default".
	_ = r.SetDefault("only-default", 1)
	srcs = r.Snapshot().SourceFor(MakePath("only-default"))
	if !slices.Equal(srcs, []string{srcDefault}) {
		t.Fatalf("SourceFor(only-default)=%v", srcs)
	}

	// Explicit beats sources entirely; only "explicit" is reported.
	_ = r.Set("k", "override")
	srcs = r.Snapshot().SourceFor(MakePath("k"))
	if !slices.Equal(srcs, []string{srcExplicit}) {
		t.Fatalf("SourceFor(k) after explicit=%v", srcs)
	}
}

func TestSnapshot_AsMap_Nested(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("server.port", 8080)
	_ = r.Set("server.host", "localhost")
	_ = r.Set("debug", true)

	m := r.Snapshot().AsMap()
	server, ok := m["server"].(map[string]any)
	if !ok {
		t.Fatalf("server is %T, want map[string]any", m["server"])
	}
	if server["port"] != int64(8080) {
		t.Fatalf("server.port=%v", server["port"])
	}
	if server["host"] != "localhost" {
		t.Fatalf("server.host=%v", server["host"])
	}
	if m["debug"] != true {
		t.Fatalf("debug=%v", m["debug"])
	}

	// Mutating the returned map does not affect the snapshot.
	delete(m, "debug")
	if !r.Snapshot().Keys()[0].HasPrefix(MakePath("debug")) && !r.IsSet("debug") {
		t.Fatal("AsMap mutation leaked into snapshot")
	}
}

func TestSnapshot_AliasResolvesSameValue(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("server.port", 8080)
	if err := r.RegisterAlias("port", "server.port"); err != nil {
		t.Fatal(err)
	}
	snap := r.Snapshot()
	canon, ok := snap.Get(MakePath("server", "port"))
	if !ok {
		t.Fatal("canonical not found")
	}
	alias, ok := snap.Get(MakePath("port"))
	if !ok {
		t.Fatal("alias not found")
	}
	ci, _ := canon.AsInt64()
	ai, _ := alias.AsInt64()
	if ci != ai {
		t.Fatalf("canon=%d alias=%d", ci, ai)
	}
}

func TestSnapshot_StringContainsKeys(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("k", "v")
	s := r.Snapshot().String()
	if !strings.Contains(s, "k = ") {
		t.Fatalf("String() missing key: %q", s)
	}
	if !strings.Contains(s, "explicit") {
		t.Fatalf("String() missing source: %q", s)
	}
}

func TestSnapshot_Stability_NotMutatedByLaterWrites(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("k", "first")

	snap1 := r.Snapshot()
	_ = r.Set("k", "second")
	snap2 := r.Snapshot()

	v1, _ := snap1.Get(MakePath("k"))
	v2, _ := snap2.Get(MakePath("k"))
	s1, _ := v1.AsString()
	s2, _ := v2.AsString()
	if s1 != "first" {
		t.Fatalf("snap1.k=%q, want first (snapshot mutated)", s1)
	}
	if s2 != "second" {
		t.Fatalf("snap2.k=%q, want second", s2)
	}
}

func TestSnapshot_PinnedKey_OnlyPinnedSourceVisible(t *testing.T) {
	high := NewMapSource("high", map[string]any{"k": "high"})
	low := NewMapSource("low", map[string]any{"k": "low"})
	r := newRegistry(t, WithSources(high, low))
	_ = r.PinSource("k", "low")

	srcs := r.Snapshot().SourceFor(MakePath("k"))
	if !slices.Equal(srcs, []string{"low"}) {
		t.Fatalf("pinned SourceFor=%v, want [low]", srcs)
	}
}

func TestSnapshot_PinnedKey_UnknownSource_Unset(t *testing.T) {
	// Pin to a source that exists, then remove the source — the key should
	// disappear (no fallback).
	src := NewMapSource("m", map[string]any{"k": "v"})
	r := newRegistry(t, WithSource(src))
	if err := r.PinSource("k", "m"); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveSource("m"); err != nil {
		t.Fatal(err)
	}
	if r.IsSet("k") {
		t.Fatal("k still resolvable after pinned source removed")
	}
}
