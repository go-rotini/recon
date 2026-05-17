package recon

import (
	"errors"
	"testing"
)

func TestPerSourceFor_ReturnsEverySourceContribution(t *testing.T) {
	high := NewMapSource("high", map[string]any{"port": 9000})
	low := NewMapSource("low", map[string]any{"port": 8080})
	r := newRegistry(t, WithSources(high, low))
	_ = r.SetDefault("port", 80)

	ps, err := PerSourceFor[int](r, "port")
	if err != nil {
		t.Fatalf("PerSourceFor: %v", err)
	}
	if len(ps.Sources) != 2 {
		t.Fatalf("got %d entries; want 2", len(ps.Sources))
	}
	if ps.Sources[0].Source != "high" || !ps.Sources[0].IsSet || ps.Sources[0].Value != 9000 {
		t.Fatalf("Sources[0]=%+v", ps.Sources[0])
	}
	if ps.Sources[1].Source != "low" || !ps.Sources[1].IsSet || ps.Sources[1].Value != 8080 {
		t.Fatalf("Sources[1]=%+v", ps.Sources[1])
	}
	if !ps.Default.IsSet || ps.Default.Value != 80 {
		t.Fatalf("Default=%+v", ps.Default)
	}
	if !ps.Resolved.IsSet || ps.Resolved.Value != 9000 || ps.Resolved.Source != "high" {
		t.Fatalf("Resolved=%+v", ps.Resolved)
	}
}

func TestPerSourceFor_ExplicitOverride(t *testing.T) {
	low := NewMapSource("low", map[string]any{"k": 1})
	r := newRegistry(t, WithSource(low))
	_ = r.Set("k", 99)

	ps, err := PerSourceFor[int](r, "k")
	if err != nil {
		t.Fatalf("PerSourceFor: %v", err)
	}
	if !ps.Explicit.IsSet || ps.Explicit.Value != 99 {
		t.Fatalf("Explicit=%+v", ps.Explicit)
	}
	if ps.Resolved.Value != 99 || ps.Resolved.Source != srcExplicit {
		t.Fatalf("Resolved=%+v", ps.Resolved)
	}
	// The source's own contribution must still be reported.
	if !ps.Sources[0].IsSet || ps.Sources[0].Value != 1 {
		t.Fatalf("source contribution lost: %+v", ps.Sources[0])
	}
}

func TestPerSourceFor_CoercionErrorRecorded(t *testing.T) {
	src := NewMapSource("s", map[string]any{"k": "not-an-int"})
	r := newRegistry(t, WithSource(src))

	ps, err := PerSourceFor[int](r, "k")
	if err != nil {
		t.Fatalf("PerSourceFor: %v", err)
	}
	if !ps.Sources[0].IsSet {
		t.Fatal("Sources[0].IsSet=false; want true (value was present but not coercible)")
	}
	if ps.Sources[0].Err == nil {
		t.Fatal("Sources[0].Err=nil; want coercion failure")
	}
}

func TestPerSourceFor_BySourceLookup(t *testing.T) {
	a := NewMapSource("a", map[string]any{"k": 1})
	b := NewMapSource("b", map[string]any{"k": 2})
	r := newRegistry(t, WithSources(a, b))

	ps, _ := PerSourceFor[int](r, "k")
	got := ps.BySource("b")
	if !got.IsSet || got.Value != 2 {
		t.Fatalf("BySource(b)=%+v", got)
	}
	missing := ps.BySource("nope")
	if missing.IsSet {
		t.Fatalf("BySource(nope).IsSet=true: %+v", missing)
	}
}

func TestPerSourceFor_NilRegistry(t *testing.T) {
	_, err := PerSourceFor[int](nil, "k")
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err=%v, want ErrInvalidPath", err)
	}
}

func TestPerSourceFor_ClosedRegistry(t *testing.T) {
	r, _ := New()
	_ = r.Close()
	_, err := PerSourceFor[int](r, "k")
	if !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("err=%v, want ErrRegistryClosed", err)
	}
}

func TestPerSourceFor_AliasResolved(t *testing.T) {
	src := NewMapSource("s", map[string]any{
		"server": map[string]any{"port": 8080},
	})
	r := newRegistry(t, WithSource(src))
	if err := r.RegisterAlias("port", "server.port"); err != nil {
		t.Fatal(err)
	}

	ps, _ := PerSourceFor[int](r, "port")
	if ps.Path.String() != "server.port" {
		t.Fatalf("Path=%s, want canonical server.port", ps.Path)
	}
	if !ps.Resolved.IsSet || ps.Resolved.Value != 8080 {
		t.Fatalf("Resolved=%+v", ps.Resolved)
	}
}
