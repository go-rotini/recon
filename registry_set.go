package recon

import "fmt"

// Set installs an explicit override for key. Explicit overrides sit above
// every source in the precedence chain — what Set declares, [Get]
// returns, regardless of which sources are registered or what they
// would supply.
//
// On a sub view, the key is interpreted relative to the sub's prefix.
// Pass nil to clear an override (equivalent to [Unset]). The snapshot
// is rebuilt before Set returns; if the rebuild fails the
// immutable-baseline or validator check, the override is rolled back
// and the error is returned. Callers can treat Set as transactional:
// either the new override is visible to every reader or none of it is.
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

// SetDefault installs a fallback value for key. Defaults sit BELOW every
// source — they are consulted only when no source (and no explicit
// override) supplies the key. Used for compile-time fallbacks and as
// the translation target for spec-declared defaults the rotini codegen
// emits.
//
// Same transactional contract as [Set]: nil clears the default, sub-
// view prefixes apply, the snapshot rebuilds before return, and a
// validator / immutable failure rolls the mutation back.
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

// Unset removes a previous explicit override for key (a no-op if none
// was set). It does NOT touch sources, defaults, or aliases — only the
// explicit-override layer. Sub-view prefixes apply.
//
// Transactional: a rebuild failure rolls the explicit value back into
// place and returns the error.
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
// graph is cycle-checked at registration time — adding an alias whose
// addition would close a loop returns *AliasCycleError (matching
// ErrAliasCycle), and the registry's alias map is left unchanged.
//
// Aliases stack: alias1 → alias2 → canonical is supported and resolves
// in one snapshot rebuild. Multiple aliases for the same canonical key
// are allowed. On a sub view, both alias and canonical are interpreted
// relative to the sub's prefix.
//
// Transactional: a rebuild failure rolls the alias back out.
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

// PinSource forces resolution of key to consult ONLY the named source.
// When pinned, the source-chain walk is skipped — even if a higher-
// precedence source has a value for key, the pinned source's view wins.
// If the pinned source has no value, the key resolves to "not set" —
// no fallback to defaults either; pinning is authoritative.
//
// Returns *SourceError when sourceName isn't a registered source.
// Sub-view prefixes apply to key. Transactional rollback on rebuild
// failure.
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
// Sub-view prefixes apply. Transactional rollback on rebuild failure.
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

// hasSourceLocked reports whether a source by name is currently
// registered. The caller MUST hold r.state.mu.
func (r *Registry) hasSourceLocked(name string) bool {
	for _, s := range r.state.sources {
		if s.Name() == name {
			return true
		}
	}
	return false
}

// restoreStringAnyEntry rolls a map[string]any mutation back to the
// pre-write state. Used by the transactional rebuild path in [Set],
// [SetDefault], [Unset] — when the rebuild rejects the candidate, the
// caller calls this with the (prev, hadPrev) tuple captured before
// the mutation.
func restoreStringAnyEntry(m map[string]any, key string, prev any, hadPrev bool) {
	if hadPrev {
		m[key] = prev
		return
	}
	delete(m, key)
}

// restoreStringStringEntry is the string-valued twin of
// [restoreStringAnyEntry]; used for the alias and pin maps.
func restoreStringStringEntry(m map[string]string, key, prev string, hadPrev bool) {
	if hadPrev {
		m[key] = prev
		return
	}
	delete(m, key)
}
