package recon

import (
	"context"
	"fmt"
	"sync"
)

// Configs is a set of named [Registry] instances. Used when an application
// has multiple independent configuration namespaces — for example, the
// rotini spec's `configuration_files[]` array where each named entry
// (`database`, `server`, …) is its own logical configuration with its own
// precedence, schema, and watch policy.
//
// Configs is safe for concurrent use. Closing a Configs closes every
// contained Registry.
type Configs struct {
	mu      sync.RWMutex
	byName  map[string]*Registry
	order   []string // registration order; preserved for Names()
	closed  bool
	closeMu sync.Once

	// events / eventCtx are populated lazily on the first Events()
	// call; the per-registry forwarders are started under
	// startForwardersLocked, and Register/Remove keeps them in sync.
	events     chan NamedEvent
	eventCtx   context.Context
	eventStop  context.CancelFunc
	forwarders map[string]struct{}
	wg         sync.WaitGroup
}

// NewConfigs returns an empty *Configs.
func NewConfigs() *Configs {
	return &Configs{byName: map[string]*Registry{}}
}

// Register attaches r under name. Returns [ErrSourceConflict] when the
// name is already taken or when name is empty.
//
// If [Configs.Events] has already been called, the new registry's
// Events channel is folded into the multiplexed stream — no need to
// re-subscribe after each Register.
func (c *Configs) Register(name string, r *Registry) error {
	if name == "" {
		return fmt.Errorf("%w: empty name", ErrSourceConflict)
	}
	if r == nil {
		return fmt.Errorf("%w: nil *Registry for %q", ErrInvalidPath, name)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.byName[name]; exists {
		return fmt.Errorf("%w: %q", ErrSourceConflict, name)
	}
	c.byName[name] = r
	c.order = append(c.order, name)
	if c.events != nil {
		c.startForwarderLocked(name, r)
	}
	return nil
}

// Get returns the registry registered under name.
func (c *Configs) Get(name string) (*Registry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.byName[name]
	return r, ok
}

// MustGet panics if name is unknown. The rotini codegen knows statically
// which names exist (from the spec's `configuration_files[]`) and uses
// MustGet at call sites where a missing name is a programmer error rather
// than a runtime condition.
func (c *Configs) MustGet(name string) *Registry {
	r, ok := c.Get(name)
	if !ok {
		panic(fmt.Errorf("recon.Configs.MustGet(%q): %w", name, ErrSourceConflict))
	}
	return r
}

// Names returns the registered names in registration order.
func (c *Configs) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.order))
	copy(out, c.order)
	return out
}

// Remove unregisters and closes the named registry. Idempotent — a name
// that isn't registered is a no-op. The registry's Close() invocation
// closes its Events channel, which lets the multiplex engine's
// per-name forwarder exit on its own — no explicit forwarder
// teardown is needed here.
func (c *Configs) Remove(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.byName[name]
	if !ok {
		return nil
	}
	delete(c.byName, name)
	delete(c.forwarders, name)
	for i, n := range c.order {
		if n == name {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	return r.Close()
}

// Close closes every contained registry. Idempotent. Returns a *MultiError
// collecting every per-registry Close error.
//
// Stops the multiplex engine (if any) BEFORE closing registries so
// the forwarders observe ctx cancellation and exit cleanly rather
// than seeing closed source channels mid-forward.
func (c *Configs) Close() error {
	var err error
	c.closeMu.Do(func() {
		// Stop the multiplex first — under the lock to coordinate
		// with concurrent Register calls, then release before Wait
		// to avoid deadlocking forwarders that need the lock.
		c.mu.Lock()
		c.closed = true
		stop := c.eventStop
		events := c.events
		c.mu.Unlock()

		if stop != nil {
			stop()
		}
		c.wg.Wait()
		if events != nil {
			close(events)
		}

		c.mu.Lock()
		defer c.mu.Unlock()
		multi := &MultiError{}
		for _, name := range c.order {
			if cerr := c.byName[name].Close(); cerr != nil {
				multi.Append(fmt.Errorf("close %q: %w", name, cerr))
			}
		}
		c.byName = map[string]*Registry{}
		c.order = nil
		if len(multi.Errors) > 0 {
			err = multi
		}
	})
	return err
}

// NamedEvent is a registry event tagged with its source registry's name.
// Delivered on the channel returned by [Configs.Events].
type NamedEvent struct {
	Event

	Name string
}

// Events returns a multiplexed channel carrying every contained
// registry's [Event], tagged with its registration name. The channel
// is created on the first call and reused on subsequent calls —
// every Events caller observes the same stream.
//
// Registries added via [Configs.Register] after Events is called are
// automatically folded into the stream; registries removed via
// [Configs.Remove] (or closed externally) have their forwarder
// shut down. Buffer capacity matches each underlying registry's
// [WithEventBufferSize] sum — a slow consumer triggers per-registry
// drop warnings on the underlying Events channels, not on the
// multiplexed channel.
//
// The channel is closed when [Configs.Close] runs. Returns nil on a
// closed Configs.
func (c *Configs) Events() <-chan NamedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	if c.events == nil {
		c.eventCtx, c.eventStop = context.WithCancel(context.Background())
		// Buffer sized for the number of currently-registered
		// registries plus headroom; rebuffered on demand if more
		// register later.
		c.events = make(chan NamedEvent, len(c.byName)*4+1)
		c.forwarders = map[string]struct{}{}
		for _, name := range c.order {
			c.startForwarderLocked(name, c.byName[name])
		}
	}
	return c.events
}

// startForwarderLocked spawns a goroutine that forwards every Event
// from r.Events() onto the multiplexed channel, tagging each entry
// with name. The caller MUST hold c.mu.
func (c *Configs) startForwarderLocked(name string, r *Registry) {
	if _, exists := c.forwarders[name]; exists {
		return
	}
	c.forwarders[name] = struct{}{}
	src := r.Events()
	if src == nil {
		// Registry was already closed; nothing to forward.
		delete(c.forwarders, name)
		return
	}
	c.wg.Go(func() {
		for {
			select {
			case <-c.eventCtx.Done():
				return
			case evt, ok := <-src:
				if !ok {
					return
				}
				select {
				case c.events <- NamedEvent{Event: evt, Name: name}:
				case <-c.eventCtx.Done():
					return
				}
			}
		}
	})
}
