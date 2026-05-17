package recon

import (
	"errors"
	"testing"
	"time"
)

func TestImmutable_BindSetsBaseline(t *testing.T) {
	type C struct {
		Tier string `recon:"tier,immutable"`
	}
	r := newRegistry(t)
	_ = r.Set("tier", "prod")
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if !r.IsImmutable("tier") {
		t.Fatal("IsImmutable=false after binding immutable-tagged field")
	}
}

func TestImmutable_ReloadRejectsChange(t *testing.T) {
	type C struct {
		Tier string `recon:"tier,immutable"`
	}
	src := newWatchableSource("s", map[string]any{"tier": "prod"})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(20*time.Millisecond),
	)

	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	// Reload with the SAME value succeeds.
	src.Trigger(map[string]any{"tier": "prod"})
	_ = awaitEvent(t, r.Events(), 2*time.Second)
	if rerr := r.Reload(); rerr != nil {
		t.Fatalf("same-value Reload: %v", rerr)
	}

	// Reload with a DIFFERENT value fails.
	src.Trigger(map[string]any{"tier": "staging"})
	evt := awaitEvent(t, r.Events(), 2*time.Second)
	if !errors.Is(evt.Err, ErrImmutableChanged) {
		t.Fatalf("evt.Err=%v, want wrap of ErrImmutableChanged", evt.Err)
	}
}

func TestImmutable_ManualReloadReturnsError(t *testing.T) {
	type C struct {
		Tier string `recon:"tier,immutable"`
	}
	r := newRegistry(t)
	_ = r.Set("tier", "prod")
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatal(err)
	}

	// Mutate the underlying explicit value out from under the
	// immutable baseline; Reload should surface the violation.
	_ = r.Set("tier", "staging")
	err := r.Reload()
	if !errors.Is(err, ErrImmutableChanged) {
		t.Fatalf("err=%v, want wrap of ErrImmutableChanged", err)
	}
	var ice *ImmutableChangedError
	if !errors.As(err, &ice) {
		t.Fatalf("err=%v, want *ImmutableChangedError", err)
	}
	if ice.Path.String() != "tier" {
		t.Fatalf("Path=%s, want tier", ice.Path)
	}
}

func TestImmutable_SecretsRedactedInError(t *testing.T) {
	type C struct {
		Token string `recon:"token,immutable,secret"`
	}
	r := newRegistry(t)
	_ = r.Set("token", "hunter2")
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatal(err)
	}

	_ = r.Set("token", "newvalue")
	err := r.Reload()
	var ice *ImmutableChangedError
	if !errors.As(err, &ice) {
		t.Fatalf("err=%v, want *ImmutableChangedError", err)
	}
	if ice.Old == "hunter2" || ice.New == "newvalue" {
		t.Fatalf("secret leaked in error: old=%q new=%q", ice.Old, ice.New)
	}
}

func TestImmutable_BaselineSetOnceOnly(t *testing.T) {
	type C struct {
		Tier string `recon:"tier,immutable"`
	}
	r := newRegistry(t)
	_ = r.Set("tier", "prod")
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatal(err)
	}

	// A second Bind after a value change must NOT silently update
	// the baseline — the violation should already be there.
	_ = r.Set("tier", "staging")
	// Bind fails because the schema rebuild fails the immutable
	// check during the snapshot install Bind triggers... actually
	// Bind doesn't trigger a rebuild. We just rebaseline issue is
	// what's being tested. Explicitly Reload to surface.
	if err := r.Reload(); !errors.Is(err, ErrImmutableChanged) {
		t.Fatalf("Reload err=%v, want wrap of ErrImmutableChanged", err)
	}

	// Confirm second Bind doesn't reset the baseline.
	_ = r.Bind(&c) // value still differs but baseline is preserved
	if err := r.Reload(); !errors.Is(err, ErrImmutableChanged) {
		t.Fatalf("baseline reset after second Bind: %v", err)
	}
}

func TestImmutable_KeyWithoutTagUnaffected(t *testing.T) {
	type C struct {
		Tier string `recon:"tier,immutable"`
		Port int    `recon:"port"`
	}
	r := newRegistry(t)
	_ = r.Set("tier", "prod")
	_ = r.Set("port", 8080)
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatal(err)
	}

	// Mutating the non-immutable key is fine.
	_ = r.Set("port", 9090)
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload after non-immutable change: %v", err)
	}
}

func TestImmutable_ClosedRegistryIsNotImmutable(t *testing.T) {
	r, _ := New()
	_ = r.Close()
	if r.IsImmutable("anything") {
		t.Fatal("IsImmutable=true on closed registry")
	}
}
