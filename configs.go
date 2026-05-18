package recon

import (
	"context"
	"fmt"
	"sync"
)

// Configs is a set of named [Registry] instances. Use when an
// application has multiple independent configuration namespaces with
// their own precedence, schema, or watch policy.
//
// Safe for concurrent use. Closing a Configs closes every contained
// Registry.
type Configs struct {
	mu      sync.RWMutex
	byName  map[string]*Registry
	order   []string // registration order, preserved for [Names]
	closed  bool
	closeMu sync.Once

	// events / eventCtx are populated lazily on the first [Events]
	// call. Register and Remove keep the per-registry forwarders in
	// sync.
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

// Register attaches r under name. Returns wrapped [ErrSourceConflict]
// when the name is taken or empty, [ErrInvalidPath] when r is nil.
//
// If [Events] has already been called, the new registry is folded
// into the multiplexed stream automatically.
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

// MustGet panics when name is unknown.
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

// Remove unregisters and closes the named registry. Idempotent. The
// registry's [Close] closes its Events channel, which lets any
// running forwarder exit on its own.
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

// Close closes every contained registry. Idempotent. Returns a
// [*MultiError] aggregating per-registry Close errors.
//
// The multiplex engine is stopped before registries close so
// forwarders observe ctx cancellation rather than racing closed
// source channels.
func (c *Configs) Close() error {
	var err error
	c.closeMu.Do(func() {
		// Stop the multiplex under lock to coordinate with Register;
		// release before Wait so forwarders that need the lock can
		// exit.
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

// NamedEvent is a registry [Event] tagged with the source registry's
// registration name.
type NamedEvent struct {
	Event

	Name string
}

// Events returns a multiplexed channel carrying every contained
// registry's events, tagged by name. The channel is created on the
// first call and reused on subsequent calls.
//
// Registries added via [Register] after Events is called are folded
// in automatically; those removed via [Remove] or closed externally
// have their forwarder shut down. Closed by [Close]. Returns nil on
// a closed Configs.
func (c *Configs) Events() <-chan NamedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	if c.events == nil {
		c.eventCtx, c.eventStop = context.WithCancel(context.Background())
		c.events = make(chan NamedEvent, len(c.byName)*4+1)
		c.forwarders = map[string]struct{}{}
		for _, name := range c.order {
			c.startForwarderLocked(name, c.byName[name])
		}
	}
	return c.events
}

// startForwarderLocked spawns a goroutine that forwards r.Events()
// onto the multiplexed channel, tagging each entry with name. Caller
// must hold c.mu.
func (c *Configs) startForwarderLocked(name string, r *Registry) {
	if _, exists := c.forwarders[name]; exists {
		return
	}
	c.forwarders[name] = struct{}{}
	src := r.Events()
	if src == nil {
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
