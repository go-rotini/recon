package recon

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRace_ConcurrentGetSetReload runs §8.8's primary stress
// scenario: N goroutines Get-ing concurrently while M goroutines
// Set and Reload. The test passes when no race detector report,
// no deadlock, no panic, and the registry is still usable at the
// end.
func TestRace_ConcurrentGetSetReload(t *testing.T) {
	const (
		readers   = 16
		writers   = 4
		reloaders = 2
		iters     = 2000
	)
	r := newRegistry(t)
	for i := range 32 {
		_ = r.Set("seed."+strconv.Itoa(i), i)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Readers: hammer the snapshot.
	for range readers {
		wg.Go(func() {
			for n := 0; n < iters; n++ {
				select {
				case <-stop:
					return
				default:
				}
				_, _, _ = r.Get("seed.0")
				_ = r.IsSet("seed.5")
				_ = r.AllKeys()
				_ = r.Snapshot()
			}
		})
	}

	// Writers: rotate explicit overrides.
	for w := range writers {
		wg.Go(func() {
			for n := 0; n < iters/4; n++ {
				select {
				case <-stop:
					return
				default:
				}
				_ = r.Set("hot."+strconv.Itoa(w), n)
			}
		})
	}

	// Reloaders: drive a handful of Reloads.
	for range reloaders {
		wg.Go(func() {
			for n := 0; n < iters/16; n++ {
				select {
				case <-stop:
					return
				default:
				}
				_ = r.Reload()
				time.Sleep(time.Microsecond)
			}
		})
	}

	wg.Wait()
	close(stop)

	// Registry is still usable.
	if _, ok, _ := r.Get("seed.0"); !ok {
		t.Fatal("seed.0 vanished under stress")
	}
}

// TestRace_WatcherFloodWithReaders is the §8.8 watcher-stress
// variant: a watcher fires reload events as fast as it can while
// 16 readers do `Get` in a tight loop. The reload-engine debounce
// keeps the work bounded; the readers must never observe a
// partially-installed snapshot (atomic.Pointer.Load is the
// guarantee).
func TestRace_WatcherFloodWithReaders(t *testing.T) {
	src := newWatchableSource("s", map[string]any{"k": 0})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(time.Millisecond),
		WithEventBufferSize(64),
	)

	// Drain events so the channel never blocks the engine.
	done := make(chan struct{})
	go func() {
		for range r.Events() {
		}
		close(done)
	}()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Watcher: 1ms reload cadence.
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			src.Trigger(map[string]any{"k": i})
		}
	})

	// 16 readers.
	for range 16 {
		wg.Go(func() {
			for n := 0; n < 5000; n++ {
				select {
				case <-stop:
					return
				default:
				}
				_, _, _ = r.Get("k")
			}
		})
	}

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
	_ = r.Close()
	<-done
}

// TestRace_LiveGetConcurrentReload exercises §8.8's Live[T]
// scenario: N concurrent [Live.Get] calls against a Live that's
// being rebound by a stream of source-change events.
//
// The single atomic-pointer load contract means every Get returns
// a complete *T, never a partially-initialized one. The test
// confirms that under load: rebinds and reads interleave without
// the race detector complaining.
func TestRace_LiveGetConcurrentReload(t *testing.T) {
	type cfg struct {
		Port int    `recon:"port"`
		Name string `recon:"name"`
	}
	src := newWatchableSource("s", map[string]any{
		"port": 0,
		"name": "init",
	})
	r := newRegistry(t,
		WithSource(src),
		WithReloadDebounce(2*time.Millisecond),
		WithEventBufferSize(64),
	)

	live, err := NewLive[cfg](r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })

	// Drain Live's events.
	go func() {
		for range live.Events() {
		}
	}()

	var observed atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Rebind driver.
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			src.Trigger(map[string]any{
				"port": i,
				"name": "iter-" + strconv.Itoa(i),
			})
			time.Sleep(100 * time.Microsecond)
		}
	})

	// 32 readers.
	for range 32 {
		wg.Go(func() {
			for n := 0; n < 10_000; n++ {
				select {
				case <-stop:
					return
				default:
				}
				p := live.Get()
				if p == nil {
					t.Errorf("Live.Get returned nil under stress")
					return
				}
				observed.Add(1)
			}
		})
	}

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	if observed.Load() == 0 {
		t.Fatal("no Live.Get observations recorded")
	}
}

// TestRace_RegistryCloseDuringReads checks the closed-shutdown
// race: while readers are hammering Get, Close races against
// them. Closed-registry reads must return zero values without
// panicking and without leaking goroutines.
func TestRace_RegistryCloseDuringReads(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Set("k", "v")

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for n := 0; n < 5_000; n++ {
				_, _, _ = r.Get("k")
				_ = r.IsSet("k")
			}
		})
	}

	// Close mid-stream.
	time.Sleep(2 * time.Millisecond)
	_ = r.Close()
	wg.Wait()
}
