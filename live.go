package recon

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Live is a typed, atomic-snapshot view of a [Registry]-bound
// struct. Each successful reload atomic-swaps the *T pointer Live
// hands out, so [Live.Get] is lock-free and always observes a
// complete, validated configuration.
//
// Construct via [NewLive]; close via [Live.Close] when done. Live
// spawns one goroutine that consumes [Registry.Events] until Close
// or until the parent's channel closes. That goroutine rebinds on each
// event and forwards it to [Live.Events] without blocking: when the
// one-slot buffer is full the event is dropped, rebinding continues,
// and the next delivered event carries a warning counting the drops.
type Live[T any] struct {
	reg     *Registry
	decode  func() (*T, error)
	curr    atomic.Pointer[T]
	events  chan Event
	stop    chan struct{}
	stopped atomic.Bool
	once    sync.Once
	// dropped counts events Live.Events could not absorb; the next
	// delivered event reports them as a warning.
	dropped atomic.Int64
	// lastErr captures the most recent rebind failure; successful
	// rebinds clear it.
	lastErr atomic.Pointer[error]
}

// NewLive constructs a [Live] for T against reg. The initial bind
// runs synchronously; failure returns the error and no goroutine is
// spawned. After that, Live reads reg.Events() and re-binds after
// each successful reload. opts is forwarded verbatim to every
// [Registry.Bind] call. Live becomes the consumer of reg.Events(), so
// other readers of that channel compete with it for events.
//
// A registry that could never deliver a reload is refused with
// [ErrNotWatched]: one built [WithoutWatch], or one holding a
// [FileSource] with no [WatcherFactory] (see [WithWatcher]).
func NewLive[T any](reg *Registry, opts ...DecodeOption) (*Live[T], error) {
	if reg == nil {
		return nil, fmt.Errorf("%w: NewLive: nil *Registry", ErrInvalidPath)
	}
	if err := reg.checkWatched(); err != nil {
		return nil, err
	}
	decode := func() (*T, error) {
		var out T
		if err := reg.Bind(&out, opts...); err != nil {
			return nil, err
		}
		return &out, nil
	}
	initial, err := decode()
	if err != nil {
		return nil, err
	}
	l := &Live[T]{
		reg:    reg,
		decode: decode,
		events: make(chan Event, 1),
		stop:   make(chan struct{}),
	}
	l.curr.Store(initial)
	go l.run()
	return l, nil
}

// Get returns the current bound *T. The cost is one
// atomic.Pointer.Load — no locks, no validator re-runs.
//
// The returned pointer is the actual instance Live owns; callers
// must treat it as read-only. A concurrent reload may swap a new
// pointer in at any time.
func (l *Live[T]) Get() *T {
	if l == nil {
		return nil
	}
	return l.curr.Load()
}

// Events returns the channel of every reload [Event] Live observed,
// with Err set when the rebind failed and Time set to when Live
// handled it. It has a one-slot buffer; events that arrive while it is
// full are dropped and counted in a warning on the next delivered
// event (see [Live]). Closed by [Live.Close] or when the parent's
// Events channel closes.
func (l *Live[T]) Events() <-chan Event { return l.events }

// LastError returns the error of the most recent reload or rebind, or
// nil if it succeeded. Useful for health checks.
func (l *Live[T]) LastError() error {
	p := l.lastErr.Load()
	if p == nil {
		return nil
	}
	return *p
}

// Close stops the rebind goroutine. Idempotent.
func (l *Live[T]) Close() error {
	l.once.Do(func() {
		l.stopped.Store(true)
		close(l.stop)
	})
	return nil
}

func (l *Live[T]) run() {
	defer close(l.events)
	src := l.reg.Events()
	for {
		select {
		case <-l.stop:
			return
		case evt, ok := <-src:
			if !ok {
				return
			}
			l.handle(evt)
		}
	}
}

func (l *Live[T]) handle(evt Event) {
	out := evt
	if evt.Err == nil {
		next, err := l.decode()
		switch {
		case err != nil:
			out.Err = err
			l.recordErr(err)
		default:
			l.curr.Store(next)
			l.recordErr(nil)
		}
	} else {
		l.recordErr(evt.Err)
	}
	out.Time = time.Now()
	l.emit(out)
}

// emit delivers evt without blocking. A full channel drops evt; the
// drop count rides on the next event that gets through, as a
// [DeprecationWarning] with an empty Path (matching [Registry.Events]).
func (l *Live[T]) emit(evt Event) {
	if dropped := l.dropped.Load(); dropped > 0 {
		noun := "events were"
		if dropped == 1 {
			noun = "event was"
		}
		evt.Warnings = append(slices.Clip(evt.Warnings), DeprecationWarning{
			Path: Path{},
			Message: fmt.Sprintf(
				"recon: %d reload %s dropped because the Live Events channel was full",
				dropped, noun),
		})
	}
	select {
	case l.events <- evt:
		l.dropped.Store(0)
	default:
		l.dropped.Add(1)
	}
}

func (l *Live[T]) recordErr(err error) {
	if err == nil {
		l.lastErr.Store(nil)
		return
	}
	l.lastErr.Store(&err)
}

// Reload re-binds T from the registry's current snapshot now, without
// rebuilding the snapshot or waiting for an event. Useful in tests
// that need deterministic timing. [Live.Events] receives nothing for
// it. ctx is not used.
func (l *Live[T]) Reload(ctx context.Context) error {
	_ = ctx // unused: the rebind takes no context
	next, err := l.decode()
	if err != nil {
		l.recordErr(err)
		return err
	}
	l.curr.Store(next)
	l.recordErr(nil)
	return nil
}

// checkWatched returns [ErrNotWatched] when r cannot deliver reload
// events: it has no watch engine, or a file source has no factory to
// watch it with.
func (r *Registry) checkWatched() error {
	if r.state.watch == nil {
		return fmt.Errorf("%w: it was built WithoutWatch", ErrNotWatched)
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	for _, src := range r.state.sources {
		if fs, ok := src.(*FileSource); ok && fs.watcherFactory() == nil {
			return fmt.Errorf("%w: file source %q has no watcher; "+
				"pass recon.WithWatcher(fswatch.New()) from github.com/go-rotini/recon/fswatch, "+
				"or recon.WithWatcher(recon.NewPollWatcher(interval))", ErrNotWatched, src.Name())
		}
	}
	return nil
}
