package recon

import (
	"fmt"
	"maps"
)

// Set installs an explicit override for key. Explicit overrides sit
// above every source in the precedence chain.
//
// A non-empty map value is merged leaf by leaf: Set("labels",
// map[string]any{"z": 3}) adds labels.z and leaves a source's labels.x
// in place. An empty map is a single value at key instead, so reading
// key (Get, or a map or struct field bound at it) gives the empty map,
// and [Snapshot.AsMap] and [Registry.Save] show it in place of lower
// layers' keys below key. Those keys stay readable on their own.
//
// On a sub view, key is interpreted relative to the sub's prefix. Pass
// nil to clear an override (equivalent to [Registry.Unset]). The snapshot is
// rebuilt before Set returns; if the rebuild fails the immutable or
// validator check, the override is rolled back and the error is
// returned.
func (r *Registry) Set(key string, value any) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	fk := r.fullKey(key)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.writable()
	prev, hadPrev := r.state.explicits[fk]
	if value == nil {
		delete(r.state.explicits, fk)
	} else {
		r.state.explicits[fk] = value
	}
	if err := r.rebuildSnapshotLocked(); err != nil {
		restoreStringAnyEntry(r.state.explicits, fk, prev, hadPrev)
		return err
	}
	return nil
}

// SetDefault installs a fallback value for key. Defaults sit below
// every source — they apply only when no source (and no explicit
// override) supplies the key. Same transactional semantics and map
// handling as [Registry.Set]; nil clears the default.
func (r *Registry) SetDefault(key string, value any) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	fk := r.fullKey(key)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.writable()
	prev, hadPrev := r.state.defaults[fk]
	if value == nil {
		delete(r.state.defaults, fk)
	} else {
		r.state.defaults[fk] = value
	}
	if err := r.rebuildSnapshotLocked(); err != nil {
		restoreStringAnyEntry(r.state.defaults, fk, prev, hadPrev)
		return err
	}
	return nil
}

// SetValues is [Registry.Set] for every key in values, with a single
// snapshot rebuild; setting many keys one Set at a time rebuilds the
// snapshot after each. A nil value clears that key's override. If the
// rebuild fails, no override changes and the error is returned.
func (r *Registry) SetValues(values map[string]any) error {
	return r.setLayer(values, func(s *registryState) map[string]any { return s.explicits })
}

// SetDefaults is [Registry.SetDefault] for every key in values, with a
// single snapshot rebuild. A nil value clears that key's default. If
// the rebuild fails, no default changes and the error is returned.
func (r *Registry) SetDefaults(values map[string]any) error {
	return r.setLayer(values, func(s *registryState) map[string]any { return s.defaults })
}

// setLayer writes values into the explicit or default layer and
// rebuilds once, restoring the layer on failure.
func (r *Registry) setLayer(values map[string]any, layer func(*registryState) map[string]any) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.writable()
	m := layer(r.state)
	prev := cloneStringAnyMap(m)
	for key, value := range values {
		fk := r.fullKey(key)
		if value == nil {
			delete(m, fk)
		} else {
			m[fk] = value
		}
	}
	if err := r.rebuildSnapshotLocked(); err != nil {
		clear(m)
		maps.Copy(m, prev)
		return err
	}
	return nil
}

// Unset removes a previous explicit override for key. Does not affect
// sources, defaults, or aliases. Transactional: a rebuild failure
// rolls the value back.
func (r *Registry) Unset(key string) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	fk := r.fullKey(key)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	prev, hadPrev := r.state.explicits[fk]
	if !hadPrev {
		return nil
	}
	delete(r.state.explicits, fk)
	if err := r.rebuildSnapshotLocked(); err != nil {
		r.state.explicits[fk] = prev
		return err
	}
	return nil
}

// RegisterAlias makes lookups of alias resolve to canonical. The alias
// graph is cycle-checked at registration time; a cycle returns
// [*AliasCycleError] with the alias map unchanged.
//
// Aliases chain: alias1 → alias2 → canonical resolves in one rebuild.
// Multiple aliases for one canonical are allowed. Transactional
// rollback on rebuild failure.
func (r *Registry) RegisterAlias(alias, canonical string) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	fa := r.fullKey(alias)
	fc := r.fullKey(canonical)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if chain := validateNoAliasCycle(fa, fc, r.state.aliases); chain != nil {
		return &AliasCycleError{Chain: chain}
	}
	r.state.writable()
	prev, hadPrev := r.state.aliases[fa]
	r.state.aliases[fa] = fc
	if err := r.rebuildSnapshotLocked(); err != nil {
		restoreStringStringEntry(r.state.aliases, fa, prev, hadPrev)
		return err
	}
	return nil
}

// PinSource forces resolution of key to consult only the named source.
// An explicit [Registry.Set] override still wins; otherwise the source
// chain is skipped, and if the pinned source has no value the key
// resolves to "not set" (no default fallback).
//
// Returns [*SourceError] when sourceName isn't registered.
// Transactional rollback on rebuild failure.
func (r *Registry) PinSource(key, sourceName string) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	fk := r.fullKey(key)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if !r.hasSourceLocked(sourceName) {
		return &SourceError{
			Source: sourceName, Op: "pin",
			Cause: fmt.Errorf("not registered: %w", ErrSourceConflict),
		}
	}
	r.state.writable()
	prev, hadPrev := r.state.pins[fk]
	r.state.pins[fk] = sourceName
	if err := r.rebuildSnapshotLocked(); err != nil {
		restoreStringStringEntry(r.state.pins, fk, prev, hadPrev)
		return err
	}
	return nil
}

// Unpin removes a previous pin for key. No-op when key was not pinned.
// Transactional rollback on rebuild failure.
func (r *Registry) Unpin(key string) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	fk := r.fullKey(key)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	prev, hadPrev := r.state.pins[fk]
	if !hadPrev {
		return nil
	}
	delete(r.state.pins, fk)
	if err := r.rebuildSnapshotLocked(); err != nil {
		r.state.pins[fk] = prev
		return err
	}
	return nil
}

// hasSourceLocked reports whether a source by name is registered.
// Caller must hold r.state.mu.
func (r *Registry) hasSourceLocked(name string) bool {
	for _, s := range r.state.sources {
		if s.Name() == name {
			return true
		}
	}
	return false
}

// restoreStringAnyEntry rolls a map[string]any mutation back. Used by
// the transactional rebuild path; pass the (prev, hadPrev) tuple
// captured before the mutation.
func restoreStringAnyEntry(m map[string]any, key string, prev any, hadPrev bool) {
	if hadPrev {
		m[key] = prev
		return
	}
	delete(m, key)
}

func restoreStringStringEntry(m map[string]string, key, prev string, hadPrev bool) {
	if hadPrev {
		m[key] = prev
		return
	}
	delete(m, key)
}

// writable makes the override, default, alias and pin maps a write needs. A registry nothing
// writes to keeps them nil, which every reader reads as empty. Caller must hold s.mu.
func (s *registryState) writable() {
	if s.explicits == nil {
		s.explicits, s.defaults = map[string]any{}, map[string]any{}
		s.aliases, s.pins = map[string]string{}, map[string]string{}
	}
}
