package recon

import "fmt"

// Set installs an explicit override for key. Explicit overrides sit
// above every source in the precedence chain.
//
// On a sub view, key is interpreted relative to the sub's prefix. Pass
// nil to clear an override (equivalent to [Unset]). The snapshot is
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
// override) supplies the key. Same transactional semantics as [Set];
// nil clears the default.
func (r *Registry) SetDefault(key string, value any) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	fk := r.fullKey(key)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
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
	prev, hadPrev := r.state.aliases[fa]
	r.state.aliases[fa] = fc
	if err := r.rebuildSnapshotLocked(); err != nil {
		restoreStringStringEntry(r.state.aliases, fa, prev, hadPrev)
		return err
	}
	return nil
}

// PinSource forces resolution of key to consult only the named source.
// When pinned the source chain is skipped; if the pinned source has
// no value, the key resolves to "not set" (no default fallback).
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
