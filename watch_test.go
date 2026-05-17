package recon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// watchableSource is a Source + Watcher used by the watch-engine
// tests. It exposes Trigger(change) so a test can drive a synthetic
// reload event without depending on a real filesystem.
type watchableSource struct {
	*MapSource

	mu   sync.Mutex
	subs []chan SourceChange
}

func newWatchableSource(name string, data map[string]any) *watchableSource {
	return &watchableSource{MapSource: NewMapSource(name, data)}
}

func (s *watchableSource) Watch(ctx context.Context) (<-chan SourceChange, error) {
	ch := make(chan SourceChange, 4)
	s.mu.Lock()
	s.subs = append(s.subs, ch)
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, c := range s.subs {
			if c == ch {
				s.subs = append(s.subs[:i], s.subs[i+1:]...)
				close(c)
				return
			}
		}
	}()
	return ch, nil
}

// Trigger updates the underlying MapSource data atomically, then
// emits a SourceChange to every active subscription so the watch
// engine fires its reload.
func (s *watchableSource) Trigger(data map[string]any) {
	s.Replace(data)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.subs {
		select {
		case c <- SourceChange{}:
		default:
		}
	}
}

// TriggerErr emits a SourceChange carrying an error so the engine's
// failure path can be exercised.
func (s *watchableSource) TriggerErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.subs {
		select {
		case c <- SourceChange{Err: err}:
		default:
		}
	}
}

// drainEvents reads up to max Events from ch, stopping on timeout.
// Used by tests to assert "exactly N events arrived in M ms."
func drainEvents(ch <-chan Event, deadline time.Duration) []Event {
	var out []Event
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, evt)
		case <-timer.C:
			return out
		}
	}
}

// awaitEvent blocks until ch produces an event, ctx cancels, or the
// deadline expires. Used by tests that need exactly one event.
func awaitEvent(t *testing.T, ch <-chan Event, deadline time.Duration) Event {
	t.Helper()
	select {
	case evt, ok := <-ch:
		if !ok {
			t.Fatal("Events channel closed before event")
		}
		return evt
	case <-time.After(deadline):
		t.Fatalf("no event within %s", deadline)
		return Event{}
	}
}

func TestWatchEngine_EmitsOnSourceChange(t *testing.T) {
	src := newWatchableSource("s", map[string]any{"k": "v1"})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(20*time.Millisecond),
	)

	src.Trigger(map[string]any{"k": "v2"})
	evt := awaitEvent(t, r.Events(), 2*time.Second)
	if evt.Err != nil {
		t.Fatalf("evt.Err=%v", evt.Err)
	}
	if evt.Source != "s" {
		t.Fatalf("Source=%q, want s", evt.Source)
	}
	if len(evt.Changed) != 1 || evt.Changed[0].String() != "k" {
		t.Fatalf("Changed=%v, want [k]", evt.Changed)
	}

	v, ok, _ := r.Get("k")
	if !ok {
		t.Fatal("k not visible after reload")
	}
	if s, _ := v.AsString(); s != "v2" {
		t.Fatalf("k=%q, want v2", s)
	}
}

func TestWatchEngine_DebounceCoalesces(t *testing.T) {
	src := newWatchableSource("s", map[string]any{"k": 0})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(80*time.Millisecond),
	)

	// Three rapid triggers inside the debounce window should collapse
	// into a single Event.
	src.Trigger(map[string]any{"k": 1})
	src.Trigger(map[string]any{"k": 2})
	src.Trigger(map[string]any{"k": 3})

	evts := drainEvents(r.Events(), 500*time.Millisecond)
	if len(evts) != 1 {
		t.Fatalf("got %d events; want 1 (debounced)", len(evts))
	}
	v, _, _ := r.Get("k")
	i, _ := v.AsInt64()
	if i != 3 {
		t.Fatalf("k=%d, want 3 (last triggered value should win)", i)
	}
}

func TestWatchEngine_SubsequentChangeAfterDebounce(t *testing.T) {
	src := newWatchableSource("s", map[string]any{"k": 0})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(20*time.Millisecond),
	)

	src.Trigger(map[string]any{"k": 1})
	_ = awaitEvent(t, r.Events(), time.Second)

	src.Trigger(map[string]any{"k": 2})
	evt := awaitEvent(t, r.Events(), time.Second)
	if len(evt.Changed) != 1 {
		t.Fatalf("Changed=%v", evt.Changed)
	}
	v, _, _ := r.Get("k")
	i, _ := v.AsInt64()
	if i != 2 {
		t.Fatalf("k=%d, want 2", i)
	}
}

func TestWatchEngine_DiffReportsAddedRemovedModified(t *testing.T) {
	src := newWatchableSource("s", map[string]any{
		"keep":   1,
		"remove": 2,
		"mod":    "old",
	})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(20*time.Millisecond),
	)

	src.Trigger(map[string]any{
		"keep": 1,
		"add":  "new",
		"mod":  "new",
	})
	evt := awaitEvent(t, r.Events(), 2*time.Second)

	changedSet := map[string]bool{}
	for _, p := range evt.Changed {
		changedSet[p.String()] = true
	}
	want := map[string]bool{"add": true, "remove": true, "mod": true}
	if len(changedSet) != len(want) {
		t.Fatalf("Changed=%v, want %v", changedSet, want)
	}
	for k := range want {
		if !changedSet[k] {
			t.Errorf("missing %q in Changed", k)
		}
	}
}

func TestWatchEngine_NoEventWhenNothingChanged(t *testing.T) {
	src := newWatchableSource("s", map[string]any{"k": "v"})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(20*time.Millisecond),
	)

	// Trigger with identical data — diff should be empty, but the
	// engine still emits an event (Changed=[]). The empty Changed
	// signals "rebuild ran but nothing differed" — useful for
	// confirming a refresh completed.
	src.Trigger(map[string]any{"k": "v"})
	evt := awaitEvent(t, r.Events(), 2*time.Second)
	if len(evt.Changed) != 0 {
		t.Fatalf("Changed=%v, want empty for identical reload", evt.Changed)
	}
}

func TestWatchEngine_PropagatesSourceError(t *testing.T) {
	src := newWatchableSource("s", map[string]any{"k": "v"})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(20*time.Millisecond),
	)
	sentinel := errors.New("source broken")
	src.TriggerErr(sentinel)

	evt := awaitEvent(t, r.Events(), 2*time.Second)
	if !errors.Is(evt.Err, sentinel) {
		t.Fatalf("evt.Err=%v, want wrap of sentinel", evt.Err)
	}
	if evt.Source != "s" {
		t.Fatalf("Source=%q, want s", evt.Source)
	}
}

func TestWatchEngine_ClosesChannelOnRegistryClose(t *testing.T) {
	src := newWatchableSource("s", map[string]any{"k": "v"})
	r, err := New(WithSource(src))
	if err != nil {
		t.Fatal(err)
	}
	ch := r.Events()
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Drain whatever's pending; the channel must eventually close.
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // closed — pass
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Events channel not closed within 2s of Close")
		}
	}
}

func TestWatchEngine_DropEventOnFullChannel(t *testing.T) {
	src := newWatchableSource("s", map[string]any{"k": 0})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(5*time.Millisecond),
		WithEventBufferSize(1),
	)

	// Phase 1: fill the buffer + force drops by triggering without
	// consuming. The buffer capacity is 1, so after the first event
	// is buffered every subsequent emit drops.
	for i := range 5 {
		src.Trigger(map[string]any{"k": i})
		time.Sleep(15 * time.Millisecond)
	}

	// Phase 2: consume the first (buffered) event, then trigger
	// another change. The engine's next emit MUST attach a warning
	// describing the drops that piled up during phase 1.
	<-r.Events()
	src.Trigger(map[string]any{"k": 99})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case evt := <-r.Events():
			if len(evt.Warnings) > 0 {
				return // success
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("never observed a drop-count warning")
}

func TestWatchEngine_NoSubscribersNoLeak(t *testing.T) {
	// A registry with no Watcher-implementing sources spawns only
	// the debounce loop. Close must clean up that loop without
	// hanging the test.
	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRegistry_Events_NilOnClosedBeforeNew(t *testing.T) {
	// Defensive: a never-started registry's Events should not panic.
	r := &Registry{state: &registryState{}}
	if ch := r.Events(); ch != nil {
		t.Fatalf("Events on un-initialized registry=%v, want nil", ch)
	}
}
