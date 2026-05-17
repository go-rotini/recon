package recon

import (
	"testing"
	"time"
)

func TestConfigs_Events_MultiplexesPerRegistry(t *testing.T) {
	cs := NewConfigs()
	t.Cleanup(func() { _ = cs.Close() })

	dbSrc := newWatchableSource("db-src", map[string]any{"k": 0})
	dbReg, err := New(
		WithSource(dbSrc),
		WithReloadDebounce(20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	srvSrc := newWatchableSource("srv-src", map[string]any{"k": 0})
	srvReg, err := New(
		WithSource(srvSrc),
		WithReloadDebounce(20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := cs.Register("database", dbReg); err != nil {
		t.Fatal(err)
	}
	if err := cs.Register("server", srvReg); err != nil {
		t.Fatal(err)
	}

	events := cs.Events()
	if events == nil {
		t.Fatal("Events returned nil")
	}

	dbSrc.Trigger(map[string]any{"k": 1})
	srvSrc.Trigger(map[string]any{"k": 2})

	seen := map[string]bool{}
	deadline := time.Now().Add(2 * time.Second)
	for len(seen) < 2 && time.Now().Before(deadline) {
		select {
		case evt, ok := <-events:
			if !ok {
				t.Fatal("Events channel closed before both events arrived")
			}
			if evt.Err != nil {
				t.Fatalf("evt(%s).Err=%v", evt.Name, evt.Err)
			}
			seen[evt.Name] = true
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !seen["database"] || !seen["server"] {
		t.Fatalf("expected events from both registries; got %v", seen)
	}
}

func TestConfigs_Events_LateRegisterIsPickedUp(t *testing.T) {
	cs := NewConfigs()
	t.Cleanup(func() { _ = cs.Close() })

	events := cs.Events()

	src := newWatchableSource("s", map[string]any{"k": 0})
	r, err := New(
		WithSource(src),
		WithReloadDebounce(20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Register("late", r); err != nil {
		t.Fatal(err)
	}

	src.Trigger(map[string]any{"k": 1})
	select {
	case evt, ok := <-events:
		if !ok {
			t.Fatal("Events closed before late-registered event arrived")
		}
		if evt.Name != "late" {
			t.Fatalf("Name=%q, want late", evt.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event from late-registered registry")
	}
}

func TestConfigs_Events_NilOnClosedConfigs(t *testing.T) {
	cs := NewConfigs()
	_ = cs.Close()
	if ch := cs.Events(); ch != nil {
		t.Fatalf("Events on closed Configs=%v, want nil", ch)
	}
}

func TestConfigs_Events_ClosedOnConfigsClose(t *testing.T) {
	cs := NewConfigs()
	r, _ := New()
	_ = cs.Register("r", r)
	ch := cs.Events()
	_ = cs.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("Events channel not closed within 2s of Configs.Close")
}
