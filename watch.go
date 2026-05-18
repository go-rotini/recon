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

// watchEngine fans in [Watcher]-implementing sources, debounces their
// [SourceChange] events, rebuilds the snapshot, and emits the result on
// the channel returned by [Registry.Events]. Started by [Registry.New]
// and stopped by [Registry.Close].
type watchEngine struct {
	r       *Registry
	ctx     context.Context
	cancel  context.CancelFunc
	pending chan pendingChange
	events  chan Event
	wg      sync.WaitGroup
	// dropped counts Events the public channel could not absorb. The
	// count is surfaced on the next deliverable event as a warning.
	dropped atomic.Int64
}

type pendingChange struct {
	src    string
	change SourceChange
}

// newWatchEngine constructs (but does not start) an engine attached to
// r. The caller must hold r.state.mu.
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

// start subscribes to every [Watcher]-implementing source, kicks off
// the optional poll ticker, and starts the debounce loop. Subscription
// failures are logged but do not abort startup. Caller must hold
// r.state.mu.
func (e *watchEngine) start() {
	hasNonWatcher := false
	for _, src := range e.r.state.sources {
		w, ok := src.(Watcher)
		if !ok {
			hasNonWatcher = true
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
	if hasNonWatcher && e.r.state.opts.pollInterval > 0 {
		e.wg.Add(1)
		go e.poll(e.r.state.opts.pollInterval)
	}
	e.wg.Add(1)
	go e.loop()
}

// poll fires a synthetic pending change every interval to drive
// reloads for non-[Watcher] sources (e.g. [OSEnvSource]). Sources
// exposing Refresh have it called first so their cached state matches.
func (e *watchEngine) poll(interval time.Duration) {
	defer e.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.refreshNonWatcherSources()
			select {
			case e.pending <- pendingChange{src: "poll", change: SourceChange{}}:
			case <-e.ctx.Done():
				return
			}
		}
	}
}

// refreshNonWatcherSources calls Refresh on any non-[Watcher] source
// that exposes it. Refresh is an optional capability discovered by type
// assertion rather than a method on [Source].
func (e *watchEngine) refreshNonWatcherSources() {
	e.r.state.mu.Lock()
	sources := append([]Source(nil), e.r.state.sources...)
	e.r.state.mu.Unlock()
	for _, src := range sources {
		if _, isWatcher := src.(Watcher); isWatcher {
			continue
		}
		if rsh, ok := src.(interface{ Refresh() int }); ok {
			_ = rsh.Refresh()
		}
	}
}

// forward shuttles SourceChange events from one source's subscription
// into the engine's pending channel.
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

// loop debounces pending changes and fires one reload per quiescent
// window.
func (e *watchEngine) loop() {
	defer e.wg.Done()
	defer close(e.events)

	debounce := e.r.state.opts.reloadDebounce
	if debounce <= 0 {
		debounce = 50 * time.Millisecond
	}

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
			// Canonical Stop+drain+Reset: when Stop reports the timer
			// already fired, drain its channel before Reset so the
			// next firing doesn't see a stale tick.
			if armed && !timer.Stop() {
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

// fireReload rebuilds the snapshot and emits one [Event]. On rebuild
// failure the previous snapshot is retained and the event carries Err
// with an empty Changed.
func (e *watchEngine) fireReload(src string) {
	prev := e.r.state.snapshot.Load()

	e.r.state.mu.Lock()
	rebuildErr := e.r.rebuildSnapshotLocked()
	e.r.state.mu.Unlock()

	cur := e.r.state.snapshot.Load()
	var changed []Path
	if rebuildErr == nil {
		changed = diffSnapshots(prev, cur)
	}

	evt := Event{
		Time:    time.Now(),
		Source:  src,
		Changed: changed,
		Err:     rebuildErr,
	}
	if w := e.r.DrainWarnings(); len(w) > 0 {
		evt.Warnings = append(evt.Warnings, w...)
	}
	e.emit(evt)
}

// emit sends evt on the public channel, prepending a warning about any
// previously-dropped events. The dropped-count reset is tied to a
// successful send so the warning isn't itself lost when the channel
// stays full.
func (e *watchEngine) emit(evt Event) {
	if dropped := e.dropped.Load(); dropped > 0 {
		noun := "events were"
		if dropped == 1 {
			noun = "event was"
		}
		evt.Warnings = append(evt.Warnings, DeprecationWarning{
			Path: Path{},
			Message: fmt.Sprintf(
				"recon: %d reload %s dropped because the Events channel was full",
				dropped, noun),
		})
		select {
		case e.events <- evt:
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

// stop cancels the engine's context and waits for every goroutine to
// finish. The public Events channel is closed by the debounce loop on
// its way out.
func (e *watchEngine) stop() {
	e.cancel()
	e.wg.Wait()
}

// diffSnapshots returns the sorted paths whose resolved value differs
// between prev and cur (added, removed, or modified). Identity-equal
// values are not reported.
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

// snapshotKeyValues returns the canonical path→Value map for s, or an
// empty map when s is nil so callers can iterate unconditionally.
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

// valuesEqual reports whether a and b carry the same payload, ignoring
// provenance. [Value]'s constructor canonicalizes types so
// reflect.DeepEqual on Any() is sufficient.
func valuesEqual(a, b Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	return reflect.DeepEqual(a.Any(), b.Any())
}
