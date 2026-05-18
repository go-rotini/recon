package recon

import (
	"context"
	"fmt"
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
// or until the parent's channel closes.
type Live[T any] struct {
	reg     *Registry
	decode  func() (*T, error)
	curr    atomic.Pointer[T]
	events  chan Event
	stop    chan struct{}
	stopped atomic.Bool
	once    sync.Once
	// lastErr captures the most recent rebind failure; successful
	// rebinds clear it.
	lastErr atomic.Pointer[error]
}

// NewLive constructs a [Live] for T against reg. The initial bind
// runs synchronously; failure returns the error and no goroutine is
// spawned. After that, Live subscribes to reg.Events() and re-binds
// on each reload. opts is forwarded verbatim to every [Registry.Bind]
// call.
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

// Events returns a buffered channel of every reload [Event] Live
// observed. Use it to surface reload failures alongside the live
// state. Closed by [Close] or when the parent's Events channel
// closes.
func (l *Live[T]) Events() <-chan Event { return l.events }

// LastError returns the most recent rebind error, or nil if the
// most recent reload succeeded. Useful for health-check endpoints
// that want a single "is my config currently broken?" boolean.
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
	select {
	case l.events <- out:
	case <-l.stop:
	}
}

func (l *Live[T]) recordErr(err error) {
	if err == nil {
		l.lastErr.Store(nil)
		return
	}
	l.lastErr.Store(&err)
}

// Reload forces a rebind off-cycle, bypassing the registry's reload
// engine. Useful in tests that need deterministic timing. The Events
// channel does not receive an entry for an off-cycle Reload.
func (l *Live[T]) Reload(ctx context.Context) error {
	_ = ctx // reserved for ctx-aware Bind in a future version
	next, err := l.decode()
	if err != nil {
		l.recordErr(err)
		return err
	}
	l.curr.Store(next)
	l.recordErr(nil)
	return nil
}
