package recon

import (
	"fmt"
	"slices"
	"testing"
)

// TestPrecedence_Conformance is the Phase-3 precedence-conformance matrix
// from §8.2 of the requirements doc, scoped to the layers Phase 3 actually
// implements:
//
//	high  ─ explicit (Registry.Set)
//	      ─ pin (Registry.PinSource — overrides the source chain when set)
//	      ─ source chain (registered Sources, first-added = higher)
//	      ─ alias (alias → canonical resolution; doesn't supply a value
//	               itself, but routes lookups across layers)
//	low   ─ default (Registry.SetDefault)
//
// The "8×8 source-type matrix" the doc calls for is the cross product of
// the source pair (MapSource / BufferSource) and the layer the value lives
// in. Every cell asserts which layer wins, and which layers are shadowed.
func TestPrecedence_Conformance(t *testing.T) {
	// makeSource builds either a MapSource or a BufferSource so the matrix
	// can drive both source implementations through identical assertions.
	makeSource := func(t *testing.T, name, kind string, data map[string]any) Source {
		t.Helper()
		switch kind {
		case "map":
			return NewMapSource(name, data)
		case "buf":
			// Hand-roll the JSON so we don't pull in the bundled codec.
			b := mustEncodeJSON(t, data)
			s, err := NewBufferSource(name, "json", b, WithBufferCodec(jsonTestCodec{}))
			if err != nil {
				t.Fatalf("NewBufferSource: %v", err)
			}
			return s
		default:
			t.Fatalf("unknown source kind %q", kind)
			return nil
		}
	}

	for _, hiKind := range []string{"map", "buf"} {
		for _, loKind := range []string{"map", "buf"} {
			name := fmt.Sprintf("high=%s/low=%s", hiKind, loKind)
			t.Run(name, func(t *testing.T) {
				runPrecedenceMatrix(t, hiKind, loKind, makeSource)
			})
		}
	}
}

func runPrecedenceMatrix(
	t *testing.T,
	hiKind, loKind string,
	mk func(t *testing.T, name, kind string, data map[string]any) Source,
) {
	t.Helper()

	// Each scenario builds a registry with a specific set of populated
	// layers and asserts (winner-value, winner-source, full provenance list).
	type scenario struct {
		name string
		// setup runs after sources have been added; use it to install
		// explicits / defaults / pins / aliases.
		setup    func(t *testing.T, r *Registry)
		key      string // post-alias resolution key the caller queries
		wantVal  string // expected string-coerced winning value
		wantWin  string // winning source name
		wantSrcs []string
	}

	scenarios := []scenario{
		{
			name:     "default only",
			setup:    func(_ *testing.T, r *Registry) { _ = r.SetDefault("k", "default") },
			key:      "k",
			wantVal:  "default",
			wantWin:  srcDefault,
			wantSrcs: []string{srcDefault},
		},
		{
			name: "low source only — default ignored",
			setup: func(_ *testing.T, r *Registry) {
				_ = r.SetDefault("k", "default")
			},
			key:      "shared", // present only in the low source's data
			wantVal:  "low",
			wantWin:  "low",
			wantSrcs: []string{"low"},
		},
		{
			name: "high beats low; default shadowed",
			setup: func(_ *testing.T, r *Registry) {
				_ = r.SetDefault("dup", "default")
			},
			key:      "dup",
			wantVal:  "high",
			wantWin:  "high",
			wantSrcs: []string{"high", "low"},
		},
		{
			name: "explicit beats every source",
			setup: func(_ *testing.T, r *Registry) {
				_ = r.SetDefault("dup", "default")
				_ = r.Set("dup", "explicit")
			},
			key:      "dup",
			wantVal:  "explicit",
			wantWin:  srcExplicit,
			wantSrcs: []string{srcExplicit},
		},
		{
			name: "pinned source wins over higher-precedence source",
			setup: func(_ *testing.T, r *Registry) {
				_ = r.PinSource("dup", "low")
			},
			key:      "dup",
			wantVal:  "low",
			wantWin:  "low",
			wantSrcs: []string{"low"},
		},
		{
			name: "pin does NOT block explicit",
			setup: func(_ *testing.T, r *Registry) {
				_ = r.PinSource("dup", "low")
				_ = r.Set("dup", "explicit")
			},
			key:      "dup",
			wantVal:  "explicit",
			wantWin:  srcExplicit,
			wantSrcs: []string{srcExplicit},
		},
		{
			name: "alias routes through every layer",
			setup: func(_ *testing.T, r *Registry) {
				_ = r.SetDefault("dup", "default")
				_ = r.RegisterAlias("alias-of-dup", "dup")
			},
			key:      "alias-of-dup",
			wantVal:  "high",
			wantWin:  "high",
			wantSrcs: []string{"high", "low"},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			highData := map[string]any{
				"dup":    "high",
				"hiOnly": "h",
			}
			lowData := map[string]any{
				"dup":    "low",
				"shared": "low",
				"loOnly": "l",
			}
			high := mk(t, "high", hiKind, highData)
			low := mk(t, "low", loKind, lowData)

			r := newRegistry(t, WithSources(high, low))
			if sc.setup != nil {
				sc.setup(t, r)
			}

			snap := r.Snapshot()
			path := MakePath(sc.key)
			v, ok := snap.Get(path)
			if !ok {
				t.Fatalf("scenario %q: key %q not found", sc.name, sc.key)
			}
			got, err := v.AsString()
			if err != nil {
				t.Fatalf("AsString: %v", err)
			}
			if got != sc.wantVal {
				t.Fatalf("value=%q, want %q", got, sc.wantVal)
			}
			if v.Source() != sc.wantWin {
				t.Fatalf("winner source=%q, want %q", v.Source(), sc.wantWin)
			}
			srcs := snap.SourceFor(path)
			if !slices.Equal(srcs, sc.wantSrcs) {
				t.Fatalf("provenance=%v, want %v", srcs, sc.wantSrcs)
			}
		})
	}
}

// TestPrecedence_ExplicitNilFallsThrough verifies that Set(k, nil) clears the
// explicit override so the next-highest layer wins again.
func TestPrecedence_ExplicitNilFallsThrough(t *testing.T) {
	src := NewMapSource("s", map[string]any{"k": "src"})
	r := newRegistry(t, WithSource(src))

	_ = r.Set("k", "ovr")
	v, _, _ := r.Get("k")
	if s, _ := v.AsString(); s != "ovr" {
		t.Fatalf("with override: k=%q", s)
	}

	_ = r.Set("k", nil)
	v, _, _ = r.Get("k")
	if s, _ := v.AsString(); s != "src" {
		t.Fatalf("after clear: k=%q, want src", s)
	}
}

// TestPrecedence_DefaultBelowEverything verifies the default layer is
// consulted only when no source supplies the key.
func TestPrecedence_DefaultBelowEverything(t *testing.T) {
	src := NewMapSource("s", map[string]any{"k": "src"})
	r := newRegistry(t, WithSource(src))
	_ = r.SetDefault("k", "def")
	_ = r.SetDefault("only-def", "def-only")

	v, _, _ := r.Get("k")
	if s, _ := v.AsString(); s != "src" {
		t.Fatalf("k=%q, want src (source must beat default)", s)
	}
	v, _, _ = r.Get("only-def")
	if s, _ := v.AsString(); s != "def-only" {
		t.Fatalf("only-def=%q, want def-only", s)
	}
}

// TestPrecedence_AliasChain verifies multi-hop alias resolution: a→b→c
// resolves any read of "a" to whichever layer supplies "c".
func TestPrecedence_AliasChain(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("c", "value")
	if err := r.RegisterAlias("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterAlias("b", "c"); err != nil {
		t.Fatal(err)
	}
	v, ok, _ := r.Get("a")
	if !ok {
		t.Fatal("a not resolvable through chain")
	}
	if s, _ := v.AsString(); s != "value" {
		t.Fatalf("a (via chain)=%q, want value", s)
	}
}

// mustEncodeJSON serialises m via the test JSON codec; used to build
// BufferSource payloads in the precedence matrix.
func mustEncodeJSON(t *testing.T, m map[string]any) []byte {
	t.Helper()
	b, err := jsonTestCodec{}.Encode(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}
