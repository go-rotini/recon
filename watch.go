package recon

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// watchEngine fans in every [Watcher]-implementing source, debounces
// the resulting [SourceChange] events, rebuilds the registry snapshot,
// and emits the resulting [Event] on the public channel returned by
// [Registry.Events].
//
// Lifecycle:
//   - Started once at construction (via [Registry.New]). Goroutines:
//     one per subscribed source (forwarders) plus one debounce loop.
//   - Stopped on [Registry.Close]. The lifecycle context cancels every
//     forwarder; the debounce loop drains any in-flight pending event,
//     closes the public Events channel, and exits.
//
// The engine is intentionally inert when no source implements Watcher —
// the only running goroutine is the debounce loop, which blocks on its
// idle select until ctx cancellation.
type watchEngine struct {
	r       *Registry
	ctx     context.Context
	cancel  context.CancelFunc
	pending chan pendingChange
	events  chan Event
	wg      sync.WaitGroup
	// dropped counts Events the public channel could not absorb because
	// it was full. The count is surfaced on the next deliverable event
	// as a [DeprecationWarning]-shaped notice so consumers see the
	// pressure without losing the actual reload signal.
	dropped atomic.Int64
}

// pendingChange is the internal envelope around a single source's
// [SourceChange]. The source name is stashed so the engine can attribute
// the outbound [Event] to its origin.
type pendingChange struct {
	src    string
	change SourceChange
}

// newWatchEngine constructs (but does not start) a watch engine
// attached to r. The caller MUST hold r.state.mu — the engine reads
// the source list once at construction.
func newWatchEngine(r *Registry) *watchEngine {
	bufSize := r.state.opts.eventBufSize
	if bufSize <= 0 {
		bufSize = 16
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &watchEngine{
		r:       r,
		ctx:     ctx,
		cancel:  cancel,
		pending: make(chan pendingChange, len(r.state.sources)+1),
		events:  make(chan Event, bufSize),
	}
}

// start subscribes to every [Watcher]-implementing source and kicks off
// the debounce loop. Subscription failures are logged but do not abort
// startup — the registry stays usable for non-watch use even when a
// single source's watcher cannot subscribe.
//
// The caller MUST hold r.state.mu.
func (e *watchEngine) start() {
	for _, src := range e.r.state.sources {
		w, ok := src.(Watcher)
		if !ok {
			continue
		}
		sub, err := w.Watch(e.ctx)
		if err != nil {
			e.r.state.logger.Warn("recon: watch subscribe failed",
				"source", src.Name(), "err", err)
			continue
		}
		e.wg.Add(1)
		go e.forward(src.Name(), sub)
	}
	e.wg.Add(1)
	go e.loop()
}

// forward shuttles SourceChange events from one source's subscription
// into the engine's pending channel. Exits when ctx cancels or the
// subscription closes.
func (e *watchEngine) forward(srcName string, sub <-chan SourceChange) {
	defer e.wg.Done()
	for {
		select {
		case <-e.ctx.Done():
			return
		case change, ok := <-sub:
			if !ok {
				return
			}
			select {
			case e.pending <- pendingChange{src: srcName, change: change}:
			case <-e.ctx.Done():
				return
			}
		}
	}
}

// loop runs the debounce-and-reload state machine. On every pending
// change it (re)sets a debounce timer; when the timer expires, the
// snapshot is rebuilt, the Changed delta is computed against the
// previous snapshot, and an Event is emitted on the public channel.
//
// The implementation goes to some length to keep the timer in a
// well-defined state across rapid pending bursts: Stop+drain+Reset is
// the canonical pattern from the standard library.
func (e *watchEngine) loop() {
	defer e.wg.Done()
	defer close(e.events)

	debounce := e.r.state.opts.reloadDebounce
	if debounce <= 0 {
		debounce = 50 * time.Millisecond
	}

	// armed timer; initially stopped (no pending). drained tracks
	// whether the timer's channel has been consumed since the last
	// Stop call — used by the canonical Stop+drain+Reset dance.
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	armed := false
	var lastSrc string

	for {
		select {
		case <-e.ctx.Done():
			return
		case p := <-e.pending:
			lastSrc = p.src
			if p.change.Err != nil {
				e.emit(Event{
					Time:   time.Now(),
					Source: p.src,
					Err:    p.change.Err,
				})
				continue
			}
			if armed && !timer.Stop() {
				// Timer already fired but hasn't been read — drain.
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(debounce)
			armed = true
		case <-timer.C:
			armed = false
			e.fireReload(lastSrc)
			lastSrc = ""
		}
	}
}

// fireReload runs one full reload cycle: rebuild the snapshot, diff
// against the previous one, validate, and emit. Snapshot installation
// is unconditional (validators are advisory on reload — the previous-
// snapshot-retained contract from §2.5.3 is honored only on hard
// build failures, which Phase 8 sources can't surface).
func (e *watchEngine) fireReload(src string) {
	prev := e.r.state.snapshot.Load()

	e.r.state.mu.Lock()
	validateErr := e.r.rebuildSnapshotLocked()
	e.r.state.mu.Unlock()

	cur := e.r.state.snapshot.Load()
	changed := diffSnapshots(prev, cur)

	evt := Event{
		Time:    time.Now(),
		Source:  src,
		Changed: changed,
		Err:     validateErr,
	}
	e.emit(evt)
}

// emit sends evt on the public channel. When the channel is full, the
// event is dropped and the dropped counter is incremented; the loss is
// surfaced on the next deliverable event as a warning so consumers see
// the back-pressure without missing every signal.
//
// The dropped-count reset is intentionally tied to successful
// delivery: a swap-then-emit would lose the warning when the emit
// itself dropped, defeating the whole point of the counter.
func (e *watchEngine) emit(evt Event) {
	if dropped := e.dropped.Load(); dropped > 0 {
		evt.Warnings = append(evt.Warnings, DeprecationWarning{
			Path:    Path{},
			Message: pluralizeDropped(dropped),
		})
		select {
		case e.events <- evt:
			// Decrement by the count we just reported; any drops that
			// accumulated during the build/emit window survive.
			e.dropped.Add(-dropped)
			return
		default:
			e.dropped.Add(1)
			e.r.state.logger.Warn("recon: events channel full; dropped event",
				"changed", len(evt.Changed), "source", evt.Source)
			return
		}
	}
	select {
	case e.events <- evt:
	default:
		e.dropped.Add(1)
		e.r.state.logger.Warn("recon: events channel full; dropped event",
			"changed", len(evt.Changed), "source", evt.Source)
	}
}

// stop cancels the engine's context, waits for every goroutine to
// finish, and ensures the public Events channel is closed. Safe to
// call from [Registry.Close].
func (e *watchEngine) stop() {
	e.cancel()
	e.wg.Wait()
}

// diffSnapshots returns the paths whose resolved value differs between
// prev and cur. Used by the engine to populate [Event.Changed].
//
// The diff covers three cases per path:
//
//   - present in cur, absent in prev → added (Changed).
//   - present in prev, absent in cur → removed (Changed).
//   - present in both with different Value.Any() → modified (Changed).
//
// Identity-equal values (same provenance, same payload) are NOT
// reported. The return is sorted by canonical path string.
func diffSnapshots(prev, cur *Snapshot) []Path {
	if prev == nil && cur == nil {
		return nil
	}
	prevMap := snapshotKeyValues(prev)
	curMap := snapshotKeyValues(cur)

	seen := map[string]struct{}{}
	out := []string{}
	for k, v := range curMap {
		seen[k] = struct{}{}
		if pv, ok := prevMap[k]; !ok || !valuesEqual(pv, v) {
			out = append(out, k)
		}
	}
	for k := range prevMap {
		if _, ok := seen[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	paths := make([]Path, len(out))
	for i, k := range out {
		paths[i] = ParsePath(k)
	}
	return paths
}

// snapshotKeyValues extracts the canonical path→Value map from s.
// Returns an empty map (not nil) when s is nil so the per-key diff
// loop can iterate without a guard.
func snapshotKeyValues(s *Snapshot) map[string]Value {
	if s == nil {
		return map[string]Value{}
	}
	out := make(map[string]Value, len(s.keys))
	for _, p := range s.keys {
		ps := p.String()
		if _, isAlias := s.aliases[ps]; isAlias {
			continue
		}
		out[ps] = s.values[ps]
	}
	return out
}

// valuesEqual reports whether a and b represent the same resolved
// configuration value. Provenance differences (Source field) do not
// count — only the payload matters; reflect.DeepEqual on Any() is
// authoritative because [Value]'s constructor canonicalizes types.
func valuesEqual(a, b Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	return reflect.DeepEqual(a.Any(), b.Any())
}

// pluralizeDropped formats the dropped-events warning. The drop path
// is exceptional — fmt's allocator overhead is not a concern here.
func pluralizeDropped(n int64) string {
	if n == 1 {
		return "recon: 1 reload event was dropped because the Events channel was full"
	}
	return fmt.Sprintf("recon: %d reload events were dropped because the Events channel was full", n)
}
