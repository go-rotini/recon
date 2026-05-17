package recon

import (
	"testing"
	"time"
)

// TestWatchEngine_WithPollDrivesNonWatcherReload verifies the
// poll-on-non-Watcher-sources path: an OSEnvSource (no Watcher) +
// WithPoll(...) produces a synthetic reload at the configured
// interval. After the env changes and the poll ticks, the registry
// reflects the new env value.
func TestWatchEngine_WithPollDrivesNonWatcherReload(t *testing.T) {
	t.Setenv("APP_PORT", "8080")
	envSrc := NewOSEnvSource(WithEnvPrefix("APP_"))
	r, err := New(
		WithSource(envSrc),
		WithPoll(30*time.Millisecond),
		WithReloadDebounce(5*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if v, _, _ := r.GetString("port"); v != "8080" {
		t.Fatalf("initial port=%q", v)
	}

	// Mutate the env and wait for the poll to refresh the source's
	// cache + the engine to rebuild.
	t.Setenv("APP_PORT", "9090")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case evt, ok := <-r.Events():
			if !ok {
				t.Fatal("events closed before poll-driven reload")
			}
			if evt.Err == nil {
				if v, _, _ := r.GetString("port"); v == "9090" {
					return // pass
				}
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("port never updated to 9090; current=%v",
		mustGetString(t, r, "port"))
}

// TestWatchEngine_WithPollNoOpWithoutNonWatcherSource verifies the
// poll goroutine doesn't start when every registered source already
// implements Watcher — the poll ticker would be redundant.
func TestWatchEngine_WithPollNoOpWithoutNonWatcherSource(t *testing.T) {
	src := newWatchableSource("s", map[string]any{"k": 0})
	r, err := New(
		WithSource(src),
		WithPoll(10*time.Millisecond),
		WithReloadDebounce(5*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// 200ms is plenty of ticks for a stray poll goroutine to fire.
	// We assert the Events channel produces no spurious events.
	select {
	case evt := <-r.Events():
		t.Fatalf("unexpected poll-driven event: %+v", evt)
	case <-time.After(200 * time.Millisecond):
		// expected
	}
}

func mustGetString(t *testing.T, r *Registry, key string) string {
	t.Helper()
	v, _, _ := r.GetString(key)
	return v
}
