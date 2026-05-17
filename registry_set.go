package recon

import "fmt"

// Set installs an explicit override for key. Explicit overrides sit above
// every source in the precedence chain — what Set declares, [Get] returns,
// regardless of which sources are registered or what they would supply.
//
// Set's typical use is runtime overrides driven by a flag handler:
//
//	// `--config-override server.port=9000` handler:
//	r.Set("server.port", 9000)
//
// Set is silent on duplicates — the new value replaces any previous Set for
// the same key. Pass nil to clear an override (equivalent to [Unset]). On a
// sub-view, the key is interpreted relative to the sub's prefix. The
// snapshot is rebuilt before Set returns.
func (r *Registry) Set(key string, value any) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	fk := r.fullKey(key)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if value == nil {
		delete(r.state.explicits, fk)
	} else {
		r.state.explicits[fk] = value
	}
	r.rebuildAndReport()
	return nil
}

// SetDefault installs a fallback value for key. Defaults sit BELOW every
// source — they are consulted only when no source (and no explicit
// override) supplies the key. Used for compile-time fallbacks and as the
// translation target for the rotini spec's `default:` declarations.
//
// Same call-shape semantics as [Set]: nil clears the default, sub-view
// prefixes apply, snapshot rebuilds before return.
func (r *Registry) SetDefault(key string, value any) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	fk := r.fullKey(key)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if value == nil {
		delete(r.state.defaults, fk)
	} else {
		r.state.defaults[fk] = value
	}
	r.rebuildAndReport()
	return nil
}

// Unset removes a previous explicit override for key (a no-op if none
// was set). It does NOT touch sources, defaults, or aliases — only the
// explicit-override layer. Sub-view prefixes apply.
func (r *Registry) Unset(key string) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	fk := r.fullKey(key)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if _, ok := r.state.explicits[fk]; !ok {
		return nil
	}
	delete(r.state.explicits, fk)
	r.rebuildAndReport()
	return nil
}

// RegisterAlias makes lookups of alias resolve to canonical. The alias
// graph is cycle-checked at registration time — adding an alias whose
// addition would close a loop returns *AliasCycleError (matching
// ErrAliasCycle), and the registry's alias map is left unchanged.
//
// Aliases stack: alias1 → alias2 → canonical is supported and resolves in
// one snapshot rebuild. Multiple aliases for the same canonical key are
// allowed. On a sub view, both alias and canonical are interpreted relative
// to the sub's prefix.
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
	r.state.aliases[fa] = fc
	r.rebuildAndReport()
	return nil
}

// PinSource forces resolution of key to consult ONLY the named source.
// When pinned, the source-chain walk is skipped — even if a higher-
// precedence source has a value for key, the pinned source's view wins.
// If the pinned source has no value, the key resolves to "not set" (no
// fallback to defaults either — pinning is authoritative).
//
// Returns *SourceError when sourceName isn't a registered source. Sub-view
// prefixes apply to key.
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
	r.state.pins[fk] = sourceName
	r.rebuildAndReport()
	return nil
}

// Unpin removes a previous pin for key. No-op when key was not pinned.
// Sub-view prefixes apply.
func (r *Registry) Unpin(key string) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	fk := r.fullKey(key)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if _, ok := r.state.pins[fk]; !ok {
		return nil
	}
	delete(r.state.pins, fk)
	r.rebuildAndReport()
	return nil
}

// hasSourceLocked reports whether a source by name is currently registered.
// The caller MUST hold r.state.mu.
func (r *Registry) hasSourceLocked(name string) bool {
	for _, s := range r.state.sources {
		if s.Name() == name {
			return true
		}
	}
	return false
}
