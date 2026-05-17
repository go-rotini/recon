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

	// Set on an immutable key whose baseline was established must
	// fail at the Set call site — the candidate snapshot the
	// transactional rebuild would install differs from the baseline,
	// so the rebuild rejects the candidate and Set rolls back.
	err := r.Set("tier", "staging")
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

	// Roll-back semantic: the resolved value stays at the baseline.
	v, _, _ := r.Get("tier")
	if s, _ := v.AsString(); s != "prod" {
		t.Fatalf("tier=%q after rejected Set, want prod", s)
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

	err := r.Set("token", "newvalue")
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

	// Attempted Set to a different value must fail (baseline
	// established).
	if err := r.Set("tier", "staging"); !errors.Is(err, ErrImmutableChanged) {
		t.Fatalf("Set err=%v, want wrap of ErrImmutableChanged", err)
	}

	// A second Bind must NOT silently update the baseline — even
	// though Bind tries to call markImmutable on the field, the
	// baseline is set-once, so the original value still anchors.
	// Re-binding with the same struct should be a no-op for the
	// baseline map.
	if err := r.Bind(&c); err != nil {
		t.Fatalf("second Bind: %v", err)
	}
	if err := r.Set("tier", "staging"); !errors.Is(err, ErrImmutableChanged) {
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
