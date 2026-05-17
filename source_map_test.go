package recon

import (
	"slices"
	"sync"
	"testing"
)

func TestMapSource_NameAndEmpty(t *testing.T) {
	s := NewMapSource("m", nil)
	if got := s.Name(); got != "m" {
		t.Fatalf("Name() = %q, want %q", got, "m")
	}
	if keys := s.Keys(); len(keys) != 0 {
		t.Fatalf("Keys() on empty source = %v, want empty", keys)
	}
	v, ok, err := s.Get(MakePath("anything"))
	if err != nil {
		t.Fatalf("Get on empty source returned err: %v", err)
	}
	if ok {
		t.Fatalf("Get on empty source ok=true, want false (value=%v)", v)
	}
}

func TestMapSource_LeafGet(t *testing.T) {
	s := NewMapSource("m", map[string]any{
		"server": map[string]any{
			"port": 8080,
			"host": "localhost",
		},
		"flag": true,
	})

	cases := []struct {
		key   string
		wantS string
		wantK ValueKind
	}{
		{"server.port", "8080", IntKind},
		{"server.host", "localhost", StringKind},
		{"flag", "true", BoolKind},
	}
	for _, tc := range cases {
		v, ok, err := s.Get(ParsePath(tc.key))
		if err != nil {
			t.Fatalf("Get(%q): err=%v", tc.key, err)
		}
		if !ok {
			t.Fatalf("Get(%q): not found", tc.key)
		}
		if v.Kind() != tc.wantK {
			t.Fatalf("Get(%q): kind=%v, want %v", tc.key, v.Kind(), tc.wantK)
		}
	}
}

func TestMapSource_GetMissingAndEmptyPath(t *testing.T) {
	s := NewMapSource("m", map[string]any{"a": map[string]any{"b": 1}})

	_, ok, err := s.Get(MakePath("a", "missing"))
	if err != nil || ok {
		t.Fatalf("missing intermediate: ok=%v err=%v", ok, err)
	}
	// Walking into a non-map mid-path should report not-found cleanly.
	_, ok, err = s.Get(MakePath("a", "b", "c"))
	if err != nil || ok {
		t.Fatalf("walk into non-map: ok=%v err=%v", ok, err)
	}
	// Empty path is a no-op.
	_, ok, err = s.Get(Path{})
	if err != nil || ok {
		t.Fatalf("empty path: ok=%v err=%v", ok, err)
	}
}

func TestMapSource_KeysSortedLeavesOnly(t *testing.T) {
	s := NewMapSource("m", map[string]any{
		"a": map[string]any{
			"b": 1,
			"c": map[string]any{"d": 2},
		},
		"z": "leaf",
	})
	keys := s.Keys()
	got := make([]string, len(keys))
	for i, k := range keys {
		got[i] = k.String()
	}
	want := []string{"a.b", "a.c.d", "z"}
	if !slices.Equal(got, want) {
		t.Fatalf("Keys() = %v, want %v", got, want)
	}
}

func TestMapSource_DeepCopyIsolatesInput(t *testing.T) {
	in := map[string]any{
		"server": map[string]any{"port": 8080},
		"list":   []any{"a", "b"},
	}
	s := NewMapSource("m", in)

	// Mutate the caller's map after construction.
	in["server"].(map[string]any)["port"] = 9999
	in["list"].([]any)[0] = "MUTATED"

	v, ok, _ := s.Get(MakePath("server", "port"))
	if !ok {
		t.Fatal("server.port not found")
	}
	i, err := v.AsInt64()
	if err != nil {
		t.Fatalf("AsInt64: %v", err)
	}
	if i != 8080 {
		t.Fatalf("server.port = %d, want 8080 (deep-copy failed)", i)
	}
}

func TestMapSource_ReplaceSwapsContents(t *testing.T) {
	s := NewMapSource("m", map[string]any{"a": 1})
	s.Replace(map[string]any{"b": 2})

	_, ok, _ := s.Get(MakePath("a"))
	if ok {
		t.Fatalf("after Replace, old key still present")
	}
	v, ok, _ := s.Get(MakePath("b"))
	if !ok {
		t.Fatalf("after Replace, new key missing")
	}
	i, _ := v.AsInt64()
	if i != 2 {
		t.Fatalf("b=%d, want 2", i)
	}
}

func TestMapSource_CloseIdempotent(t *testing.T) {
	s := NewMapSource("m", map[string]any{"a": 1})
	if err := s.Close(); err != nil {
		t.Fatalf("Close() err=%v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close() err=%v", err)
	}
}

func TestMapSource_ConcurrentRead(t *testing.T) {
	s := NewMapSource("m", map[string]any{"server": map[string]any{"port": 8080}})
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for range 100 {
				_, _, _ = s.Get(MakePath("server", "port"))
				_ = s.Keys()
			}
		})
	}
	wg.Wait()
}
