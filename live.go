package recon

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Live is a typed, atomic-snapshot view of a [Registry]-bound struct.
// On every reload that produces a valid binding, Live atomic-swaps the
// pointer it hands out so [Live.Get] is lock-free and always observes
// a complete, validated configuration.
//
// Construct via [NewLive]; close via [Live.Close] when the parent
// registry is closed (Live spawns a goroutine that consumes from
// [Registry.Events] until either is closed).
//
// The mental model matches go-rotini/env's Live[T]: hand out a
// pointer to a fully-decoded T, replace it atomically on each
// successful reload, never block readers.
type Live[T any] struct {
	reg     *Registry
	decode  func() (*T, error)
	curr    atomic.Pointer[T]
	events  chan Event
	stop    chan struct{}
	stopped atomic.Bool
	once    sync.Once
	// lastErr captures the most recent rebind failure for diagnostic
	// inspection via [Live.LastError]. Successful rebinds clear it.
	lastErr atomic.Pointer[error]
}

// NewLive constructs a [Live] for T against reg. The initial bind
// runs synchronously inside NewLive — a failure returns the error and
// no goroutine is spawned. After the initial bind succeeds, Live
// subscribes to reg.Events() and re-binds on each reload event.
//
// opts is forwarded verbatim to every call to [Registry.Bind] —
// including [WithCustomDecoder], [WithDecodeTag], and the strict /
// lenient toggles — so the per-call options are stable across the
// lifetime of the Live.
func NewLive[T any](reg *Registry, opts ...DecodeOption) (*Live[T], error) {
	if reg == nil {
		return nil, fmt.Errorf("%w: NewLive: nil *Registry", ErrInvalidPath)
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

// Get returns the current bound *T. Hot-path cost is a single
// atomic.Pointer.Load — no locks, no validator re-runs, no map
// lookups beyond what the underlying snapshot already cached.
//
// The returned pointer is the actual instance Live owns; callers
// MUST treat it as read-only because a concurrent reload may swap a
// new pointer in at any time. Mutating *T defeats the safety
// guarantees Live offers.
func (l *Live[T]) Get() *T {
	if l == nil {
		return nil
	}
	return l.curr.Load()
}

// Events returns a buffered channel of every reload [Event] Live
// observed since construction. Use to surface reload failures
// alongside the live state (Live.Get keeps returning the
// previously-good snapshot when a reload fails).
//
// The channel is closed when [Live.Close] is invoked or when the
// parent registry's Events channel closes.
func (l *Live[T]) Events() <-chan Event { return l.events }

// LastError returns the most recent rebind error, or nil if the most
// recent reload succeeded. Useful for health-check endpoints that
// want a single "is my config currently broken?" boolean without
// having to consume from the Events channel.
func (l *Live[T]) LastError() error {
	p := l.lastErr.Load()
	if p == nil {
		return nil
	}
	return *p
}

// Close stops the rebind goroutine. Idempotent. Returns nil; the
// signature is for forward compatibility with sources that might
// later need to surface a close error.
func (l *Live[T]) Close() error {
	l.once.Do(func() {
		l.stopped.Store(true)
		close(l.stop)
	})
	return nil
}

// run is the goroutine body that feeds the rebind loop. It consumes
// the registry's Events channel, re-binds on each reload, and forwards
// the resulting Event downstream (with the rebind error stamped onto
// Event.Err when the rebind itself failed).
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

// handle processes one registry [Event]: re-bind if the registry
// reload itself succeeded, forward the resulting outcome on Live's
// own channel. A registry-side validation failure short-circuits the
// rebind (the snapshot is the previous one anyway).
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
	select {
	case l.events <- out:
	case <-l.stop:
	}
}

// recordErr updates the Live's lastErr slot. Passing nil clears it
// so LastError reports only the most recent rebind outcome.
func (l *Live[T]) recordErr(err error) {
	if err == nil {
		l.lastErr.Store(nil)
		return
	}
	l.lastErr.Store(&err)
}

// Reload forces a rebind off-cycle, bypassing the registry's reload
// engine. Useful in tests that need deterministic timing without
// driving a synthetic source-change event.
//
// Returns the rebind error (and leaves the previous pointer in place
// on failure). The Events channel does NOT receive an entry for an
// off-cycle Reload — it's caller-initiated, not engine-initiated.
func (l *Live[T]) Reload(ctx context.Context) error {
	_ = ctx // reserved for sources that consult ctx on Bind
	next, err := l.decode()
	if err != nil {
		l.recordErr(err)
		return err
	}
	l.curr.Store(next)
	l.recordErr(nil)
	return nil
}
