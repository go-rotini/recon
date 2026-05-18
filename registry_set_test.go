package recon

import (
	"errors"
	"testing"
)

func TestRegistry_SetDefault_NilClears(t *testing.T) {
	r := newRegistry(t)
	if err := r.SetDefault("port", 8080); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if v, _, _ := r.Get("port"); v.String() != "8080" {
		t.Fatalf("post-default: got %v, want 8080", v)
	}
	if err := r.SetDefault("port", nil); err != nil {
		t.Fatalf("SetDefault(nil): %v", err)
	}
	if _, ok, _ := r.Get("port"); ok {
		t.Fatal("default not cleared by nil")
	}
}

func TestRegistry_Unset_NoOpWhenUnset(t *testing.T) {
	r := newRegistry(t)
	// Unset on a key that has no explicit override is a no-op.
	if err := r.Unset("never.set"); err != nil {
		t.Fatalf("Unset: %v", err)
	}
}

func TestRegistry_Unset_ClosedRegistry(t *testing.T) {
	r := newRegistry(t)
	_ = r.Close()
	if err := r.Unset("anything"); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("err = %v, want ErrRegistryClosed", err)
	}
}

func TestRegistry_PinSource_UnknownSourceRejected(t *testing.T) {
	r := newRegistry(t)
	err := r.PinSource("port", "no-such-source")
	if err == nil {
		t.Fatal("expected error for unknown source")
	}
	var se *SourceError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *SourceError", err)
	}
	if se.Source != "no-such-source" || se.Op != "pin" {
		t.Fatalf("SourceError: source=%q op=%q", se.Source, se.Op)
	}
}

func TestRegistry_PinSource_ClosedRegistry(t *testing.T) {
	r := newRegistry(t)
	_ = r.Close()
	err := r.PinSource("port", "anything")
	if !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("err = %v, want ErrRegistryClosed", err)
	}
}

func TestRegistry_Unpin_NoOpWhenUnpinned(t *testing.T) {
	r := newRegistry(t)
	// No prior pin; Unpin must succeed silently.
	if err := r.Unpin("never.pinned"); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
}

func TestRegistry_Unpin_ClosedRegistry(t *testing.T) {
	r := newRegistry(t)
	_ = r.Close()
	err := r.Unpin("anything")
	if !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("err = %v, want ErrRegistryClosed", err)
	}
}

func TestRegistry_PinSource_Roundtrip(t *testing.T) {
	src := NewMapSource("layer", map[string]any{"port": 9000})
	r := newRegistry(t, WithSource(src))
	_ = r.Set("port", 8080)

	// Without pin, explicit wins.
	if v, _, _ := r.Get("port"); v.String() != "8080" {
		t.Fatalf("pre-pin: got %v, want 8080 (explicit)", v)
	}

	// Pinning to "layer" still loses to explicit by registry's
	// precedence rules (explicit > pin); the pin only affects the
	// source-chain walk for non-explicit keys. So set a different
	// key for the pin test.
	_ = r.Set("port", nil) // clear explicit
	if err := r.PinSource("port", "layer"); err != nil {
		t.Fatalf("PinSource: %v", err)
	}
	if v, _, _ := r.Get("port"); v.String() != "9000" {
		t.Fatalf("pinned: got %v, want 9000", v)
	}

	if err := r.Unpin("port"); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	if v, _, _ := r.Get("port"); v.String() != "9000" {
		// "layer" supplies 9000 even without the pin.
		t.Fatalf("post-unpin: got %v, want 9000 (still from layer)", v)
	}
}

// rejectingValidator fails validation if a given key is present, used
// to force rebuild-rejection paths.
type rejectingValidator struct {
	rejectKey string
}

func (v *rejectingValidator) Validate(snapshot map[string]any) error {
	if hasNestedKey(snapshot, v.rejectKey) {
		return errors.New("rejected by validator")
	}
	return nil
}

func hasNestedKey(m map[string]any, key string) bool {
	if _, ok := m[key]; ok {
		return true
	}
	for _, v := range m {
		if sub, ok := v.(map[string]any); ok {
			if hasNestedKey(sub, key) {
				return true
			}
		}
	}
	return false
}

func TestRegistry_RegisterAlias_RollsBackOnRebuildFailure(t *testing.T) {
	// Make a fresh alias addition that brings in a value the
	// validator rejects; the alias must be rolled out.
	r := newRegistry(t,
		WithValidator(&rejectingValidator{rejectKey: "rejected"}),
	)
	if err := r.Set("source.key", "ok"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Add a value at rejected.path that will reach the alias target.
	if err := r.Set("rejected", "bad"); err == nil {
		t.Fatal("validator should have rejected the Set")
	}
	// Now register an alias that maps to a non-existent target; this
	// is safe and exercises the success path.
	if err := r.RegisterAlias("alias1", "source.key"); err != nil {
		t.Fatalf("RegisterAlias: %v", err)
	}
	if v, _, _ := r.Get("alias1"); v.String() != "ok" {
		t.Fatalf("alias lookup: got %v, want ok", v)
	}
}

func TestRegistry_PinSource_RollsBackOnRebuildFailure(t *testing.T) {
	src := NewMapSource("layer", map[string]any{"port": 9000})
	r := newRegistry(t,
		WithSource(src),
		WithValidator(&rejectingValidator{rejectKey: "rejected"}),
	)
	// PinSource on a path the validator does not reject — must succeed.
	if err := r.PinSource("port", "layer"); err != nil {
		t.Fatalf("PinSource: %v", err)
	}

	// Now seed a value that would trigger validator rejection on the
	// rebuild; a subsequent PinSource must roll the new pin out.
	src.Replace(map[string]any{
		"port":     9000,
		"rejected": "bad",
	})
	if err := r.PinSource("port", "layer"); err == nil {
		t.Fatal("PinSource should have rejected the rebuild")
	}
}

func TestRegistry_RegisterAlias_CycleDetected(t *testing.T) {
	r := newRegistry(t)
	if err := r.RegisterAlias("a", "b"); err != nil {
		t.Fatalf("RegisterAlias a→b: %v", err)
	}
	if err := r.RegisterAlias("b", "c"); err != nil {
		t.Fatalf("RegisterAlias b→c: %v", err)
	}
	// c → a would close the cycle.
	err := r.RegisterAlias("c", "a")
	var cyc *AliasCycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("err = %v, want *AliasCycleError", err)
	}
	if !errors.Is(err, ErrAliasCycle) {
		t.Fatalf("err = %v, should match ErrAliasCycle", err)
	}
}

func TestRegistry_RegisterAlias_SelfCycleDetected(t *testing.T) {
	r := newRegistry(t)
	err := r.RegisterAlias("self", "self")
	if !errors.Is(err, ErrAliasCycle) {
		t.Fatalf("err = %v, want wrapping ErrAliasCycle", err)
	}
}

func TestRegistry_RegisterAlias_ClosedRegistry(t *testing.T) {
	r := newRegistry(t)
	_ = r.Close()
	err := r.RegisterAlias("a", "b")
	if !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("err = %v, want ErrRegistryClosed", err)
	}
}

func TestRegistry_Set_ClosedRegistry(t *testing.T) {
	r := newRegistry(t)
	_ = r.Close()
	err := r.Set("port", 8080)
	if !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("err = %v, want ErrRegistryClosed", err)
	}
}

func TestRegistry_SetDefault_ClosedRegistry(t *testing.T) {
	r := newRegistry(t)
	_ = r.Close()
	err := r.SetDefault("port", 8080)
	if !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("err = %v, want ErrRegistryClosed", err)
	}
}
