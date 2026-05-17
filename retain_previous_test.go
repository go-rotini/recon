package recon

import (
	"errors"
	"testing"
	"time"
)

// TestRetainPrevious_SetRollsBackOnValidationFailure verifies the
// transactional Set contract: when a Set call would produce a snapshot
// the schema rejects, the explicit-override map is rolled back and the
// installed snapshot stays at the previously-good state.
func TestRetainPrevious_SetRollsBackOnValidationFailure(t *testing.T) {
	schema := []byte(`{
		"type": "object",
		"properties": {"port": {"type": "integer", "minimum": 1, "maximum": 65535}}
	}`)
	r, err := New(WithSchema(schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// Establish a known-good value.
	if err := r.Set("port", 8080); err != nil {
		t.Fatalf("initial Set: %v", err)
	}

	// An invalid Set must fail and not change the registry's state.
	setErr := r.Set("port", 999999)
	if !errors.Is(setErr, ErrValidation) {
		t.Fatalf("invalid Set err=%v, want wrap of ErrValidation", setErr)
	}
	v, _, _ := r.Get("port")
	if i, _ := v.AsInt64(); i != 8080 {
		t.Fatalf("port=%d after rejected Set, want 8080", i)
	}
}

// TestRetainPrevious_UnsetRollsBackOnValidationFailure: removing a
// required key must be rejected — the snapshot would fail validation,
// so the explicit map is restored.
func TestRetainPrevious_UnsetRollsBackOnValidationFailure(t *testing.T) {
	schema := []byte(`{
		"type": "object",
		"required": ["port"],
		"properties": {"port": {"type": "integer"}}
	}`)
	r, err := New(WithSchema(schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if err := r.Set("port", 8080); err != nil {
		t.Fatal(err)
	}
	uerr := r.Unset("port")
	if !errors.Is(uerr, ErrValidation) {
		t.Fatalf("Unset err=%v, want wrap of ErrValidation", uerr)
	}
	if v, _, _ := r.Get("port"); v.String() != "8080" {
		t.Fatalf("port=%v after rejected Unset", v)
	}
}

// TestRetainPrevious_ReloadRetainsPreviousOnValidatorFailure: when a
// watched source delivers an invalid candidate, the registry must
// continue serving the previous snapshot until a valid candidate is
// produced.
func TestRetainPrevious_ReloadRetainsPreviousOnValidatorFailure(t *testing.T) {
	schema := []byte(`{
		"type": "object",
		"required": ["k"],
		"properties": {"k": {"type": "string"}}
	}`)
	src := newWatchableSource("s", map[string]any{"k": "good"})
	r, err := New(
		WithSchema(schema),
		WithSource(src),
		WithReloadDebounce(20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// Confirm we start in a good state.
	if v, _, _ := r.GetString("k"); v != "good" {
		t.Fatalf("initial k=%q", v)
	}

	// Trigger a reload with an invalid candidate (missing required).
	src.Trigger(map[string]any{})
	evt := awaitEvent(t, r.Events(), 2*time.Second)
	if !errors.Is(evt.Err, ErrValidation) {
		t.Fatalf("evt.Err=%v, want wrap of ErrValidation", evt.Err)
	}
	if len(evt.Changed) != 0 {
		t.Fatalf("Changed=%v on rejected reload, want empty", evt.Changed)
	}

	// Readers must still observe the previous snapshot.
	if v, _, _ := r.GetString("k"); v != "good" {
		t.Fatalf("post-rejected-reload k=%q, want previous-snapshot good", v)
	}

	// A subsequent valid reload installs cleanly.
	src.Trigger(map[string]any{"k": "better"})
	evt = awaitEvent(t, r.Events(), 2*time.Second)
	if evt.Err != nil {
		t.Fatalf("valid reload err=%v", evt.Err)
	}
	if v, _, _ := r.GetString("k"); v != "better" {
		t.Fatalf("post-valid-reload k=%q", v)
	}
}

// TestRetainPrevious_LiveGetReturnsLastGood: Live[T] should also
// observe the previous snapshot when reload validation fails.
func TestRetainPrevious_LiveGetReturnsLastGood(t *testing.T) {
	type cfg struct {
		K string `recon:"k,required"`
	}
	src := newWatchableSource("s", map[string]any{"k": "good"})
	r, err := New(
		WithSource(src),
		WithReloadDebounce(20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	live, err := NewLive[cfg](r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })

	goodPtr := live.Get()
	if goodPtr.K != "good" {
		t.Fatalf("initial K=%q", goodPtr.K)
	}

	// Trigger a reload where the bind itself fails (required missing).
	src.Trigger(map[string]any{})
	// Wait for the Live event.
	awaitEvent(t, live.Events(), 2*time.Second)

	// Live.Get must continue to return the last-known-good pointer.
	if live.Get().K != "good" {
		t.Fatalf("Live.Get K=%q after bad reload; want previous-good 'good'",
			live.Get().K)
	}
}

// TestRetainPrevious_AddSourceRollsBack: AddSource whose snapshot
// fails validation must roll the source out of the chain.
func TestRetainPrevious_AddSourceRollsBack(t *testing.T) {
	schema := []byte(`{
		"type": "object",
		"properties": {"port": {"type": "integer", "maximum": 100}}
	}`)
	r, err := New(WithSchema(schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	bad := NewMapSource("bad", map[string]any{"port": 99999})
	if addErr := r.AddSource(bad); !errors.Is(addErr, ErrValidation) {
		t.Fatalf("AddSource err=%v, want ErrValidation", addErr)
	}

	// The source must NOT be in the chain after rollback.
	for _, name := range r.Sources() {
		if name == "bad" {
			t.Fatal("bad source remained in chain after rollback")
		}
	}
}
