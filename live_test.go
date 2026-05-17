package recon

import (
	"errors"
	"testing"
	"time"
)

type liveConfig struct {
	Port int    `recon:"port"`
	Name string `recon:"name"`
}

func TestLive_InitialBind(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("port", 8080)
	_ = r.Set("name", "rotini")

	live, err := NewLive[liveConfig](r)
	if err != nil {
		t.Fatalf("NewLive: %v", err)
	}
	t.Cleanup(func() { _ = live.Close() })

	cfg := live.Get()
	if cfg == nil {
		t.Fatal("Get returned nil")
	}
	if cfg.Port != 8080 || cfg.Name != "rotini" {
		t.Fatalf("cfg=%+v", *cfg)
	}
}

func TestLive_NilRegistryRejected(t *testing.T) {
	_, err := NewLive[liveConfig](nil)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err=%v, want ErrInvalidPath", err)
	}
}

func TestLive_GetReturnsAtomicPointer(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("port", 8080)
	_ = r.Set("name", "x")

	live, err := NewLive[liveConfig](r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })

	p1 := live.Get()
	p2 := live.Get()
	if p1 != p2 {
		t.Fatal("Get returned different pointers without a reload")
	}
}

func TestLive_RebindsOnReload(t *testing.T) {
	src := newWatchableSource("s", map[string]any{
		"port": 8080, "name": "before",
	})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(20*time.Millisecond),
	)

	live, err := NewLive[liveConfig](r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })

	pBefore := live.Get()
	if pBefore.Name != "before" {
		t.Fatalf("initial name=%q", pBefore.Name)
	}

	src.Trigger(map[string]any{"port": 9090, "name": "after"})

	// Wait for the rebind by polling Live's Events channel.
	evt := awaitEvent(t, live.Events(), 2*time.Second)
	if evt.Err != nil {
		t.Fatalf("evt.Err=%v", evt.Err)
	}
	pAfter := live.Get()
	if pAfter == pBefore {
		t.Fatal("Get returned same pointer after reload")
	}
	if pAfter.Port != 9090 || pAfter.Name != "after" {
		t.Fatalf("after=%+v", *pAfter)
	}
}

func TestLive_RebindFailureRetainsPreviousPointer(t *testing.T) {
	// Required tag will fail when the source no longer supplies the key.
	type strictConfig struct {
		Port int `recon:"port,required"`
	}
	src := newWatchableSource("s", map[string]any{"port": 8080})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(20*time.Millisecond),
	)
	live, err := NewLive[strictConfig](r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })

	pBefore := live.Get()
	if pBefore.Port != 8080 {
		t.Fatalf("initial port=%d", pBefore.Port)
	}

	// Trigger a change that removes the required key.
	src.Trigger(map[string]any{})

	evt := awaitEvent(t, live.Events(), 2*time.Second)
	if evt.Err == nil {
		t.Fatal("expected rebind error on missing required key")
	}
	if live.Get() != pBefore {
		t.Fatal("Live swapped pointer despite rebind failure")
	}
	if !errors.Is(live.LastError(), ErrMissingRequired) {
		t.Fatalf("LastError=%v, want wrap of ErrMissingRequired", live.LastError())
	}
}

func TestLive_LastErrorClearsOnSuccess(t *testing.T) {
	type strictConfig struct {
		Port int `recon:"port,required"`
	}
	src := newWatchableSource("s", map[string]any{"port": 8080})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(20*time.Millisecond),
	)
	live, err := NewLive[strictConfig](r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })

	// Break it.
	src.Trigger(map[string]any{})
	_ = awaitEvent(t, live.Events(), 2*time.Second)
	if live.LastError() == nil {
		t.Fatal("LastError nil after broken reload")
	}

	// Fix it.
	src.Trigger(map[string]any{"port": 9090})
	evt := awaitEvent(t, live.Events(), 2*time.Second)
	if evt.Err != nil {
		t.Fatalf("evt.Err=%v after fix", evt.Err)
	}
	if live.LastError() != nil {
		t.Fatalf("LastError=%v after successful rebind, want nil", live.LastError())
	}
}

func TestLive_CloseStopsRebinding(t *testing.T) {
	src := newWatchableSource("s", map[string]any{"port": 1, "name": "x"})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(10*time.Millisecond),
	)
	live, err := NewLive[liveConfig](r)
	if err != nil {
		t.Fatal(err)
	}

	if err := live.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Second Close is idempotent.
	if err := live.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	// After Close, Get still returns the last-bound value.
	cfg := live.Get()
	if cfg == nil || cfg.Name != "x" {
		t.Fatalf("post-close Get=%+v", cfg)
	}
}

func TestLive_ManualReload(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("port", 1)
	_ = r.Set("name", "before")

	live, err := NewLive[liveConfig](r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })

	pBefore := live.Get()
	_ = r.Set("name", "after")
	if err := live.Reload(t.Context()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	pAfter := live.Get()
	if pAfter == pBefore {
		t.Fatal("manual Reload did not swap pointer")
	}
	if pAfter.Name != "after" {
		t.Fatalf("after.Name=%q", pAfter.Name)
	}
}

func TestLive_InitialBindFailurePropagates(t *testing.T) {
	type strictConfig struct {
		DSN string `recon:"db.dsn,required"`
	}
	r := newRegistry(t) // empty — required key absent

	_, err := NewLive[strictConfig](r)
	if !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("err=%v, want wrap of ErrMissingRequired", err)
	}
}

func TestLive_EventsChannelClosesOnLiveClose(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("port", 1)
	_ = r.Set("name", "x")
	live, err := NewLive[liveConfig](r)
	if err != nil {
		t.Fatal(err)
	}
	ch := live.Events()
	_ = live.Close()

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
	t.Fatal("Live.Events not closed within 2s of Close")
}

// liveMyInt is the typed scalar a custom decoder fills.
type liveMyInt int

func TestLive_DecodeOptionsPropagate(t *testing.T) {
	// Confirm that DecodeOptions handed to NewLive flow into every
	// subsequent rebind. A custom decoder is the cleanest probe: if
	// it does NOT fire, the field stays at its zero value; if it
	// DOES fire, we observe the decoder's signature output.
	type cfg struct {
		N liveMyInt `recon:"n"`
	}
	r := newRegistry(t)
	_ = r.Set("n", "fortytwo")

	dec := WithCustomDecoder(func(v Value) (liveMyInt, error) {
		s, _ := v.AsString()
		if s == "fortytwo" {
			return 42, nil
		}
		return 0, errors.New("only fortytwo supported")
	})
	live, err := NewLive[cfg](r, dec)
	if err != nil {
		t.Fatalf("NewLive: %v", err)
	}
	t.Cleanup(func() { _ = live.Close() })
	if live.Get().N != 42 {
		t.Fatalf("N=%d, want 42 (custom decoder did not run)", live.Get().N)
	}
}
