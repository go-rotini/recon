package recon

import (
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
}

// NewConfigs returns an empty *Configs.
func NewConfigs() *Configs {
	return &Configs{byName: map[string]*Registry{}}
}

// Register attaches r under name. Returns [ErrSourceConflict] when the
// name is already taken or when name is empty.
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
// that isn't registered is a no-op.
func (c *Configs) Remove(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.byName[name]
	if !ok {
		return nil
	}
	delete(c.byName, name)
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
func (c *Configs) Close() error {
	var err error
	c.closeMu.Do(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.closed = true
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
// Used by the (Phase-8+) multiplexed Events channel a Configs will expose
// once the watch engine lands.
type NamedEvent struct {
	Event

	Name string
}
