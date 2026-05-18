package recon

import (
	"slices"
)

// Description is the structured snapshot view [Registry.Describe]
// returns. Keys are sorted by canonical path; alias paths are not
// listed as separate rows but appear on their target's Aliases field.
type Description struct {
	Keys []KeyDescription
}

// KeyDescription is one per-key row of a [Description]. Value is
// pre-redacted when Secret is true.
type KeyDescription struct {
	// Path is the canonical key.
	Path Path

	// Value is the resolved value's string form, redacted via the
	// registry's secret redactor when the key is marked secret.
	Value string

	// Source is the name of the source that won the precedence race.
	// Empty when no source supplied the key.
	Source string

	// Sources lists every contributor in precedence order, including
	// the reserved labels "explicit" and "default".
	Sources []string

	// Secret reports whether the key is marked secret.
	Secret bool

	// Aliases lists every alias path that resolves to this key.
	Aliases []Path

	// Schema carries a per-key schema fragment when the registered
	// validator exposes one. Empty for the bundled JSON Schema
	// validator.
	Schema string
}

// Describe returns a [Description] of the current snapshot. The
// result reflects the call instant; callers wanting a consistent
// view across multiple calls should pin via [Registry.Snapshot] and
// walk it themselves.
func (r *Registry) Describe() Description {
	if r.state.closed.Load() {
		return Description{}
	}
	snap := r.state.snapshot.Load()
	if snap == nil {
		return Description{}
	}
	r.state.mu.Lock()
	secrets := cloneStringSet(r.state.secretKeys)
	redactor := r.state.opts.secretRedactor
	aliasByCanon := r.invertAliasesLocked()
	r.state.mu.Unlock()

	canonicalKeys := make([]string, 0, len(snap.keys))
	for _, p := range snap.keys {
		ps := p.String()
		if _, isAlias := snap.aliases[ps]; isAlias {
			continue
		}
		canonicalKeys = append(canonicalKeys, ps)
	}
	slices.Sort(canonicalKeys)

	out := Description{Keys: make([]KeyDescription, 0, len(canonicalKeys))}
	for _, ks := range canonicalKeys {
		v := snap.values[ks]
		_, secret := secrets[ks]
		display := v.String()
		if secret && redactor != nil {
			display = redactor(display)
		}
		srcs := snap.sources[ks]
		winner := ""
		if len(srcs) > 0 {
			winner = srcs[0]
		}
		out.Keys = append(out.Keys, KeyDescription{
			Path:    ParsePath(ks),
			Value:   display,
			Source:  winner,
			Sources: append([]string(nil), srcs...),
			Secret:  secret,
			Aliases: aliasByCanon[ks],
		})
	}
	return out
}

// DescribeKey is the per-key form of [Describe]. Alias keys resolve
// to their canonical row, so DescribeKey("port") and
// DescribeKey("server.port") return the same row when port is
// aliased.
func (r *Registry) DescribeKey(key string) (KeyDescription, bool) {
	if r.state.closed.Load() {
		return KeyDescription{}, false
	}
	snap := r.state.snapshot.Load()
	if snap == nil {
		return KeyDescription{}, false
	}
	fullKey := r.fullKey(key)
	r.state.mu.Lock()
	canon := resolveAliasChain(fullKey, r.state.aliases)
	secrets := cloneStringSet(r.state.secretKeys)
	redactor := r.state.opts.secretRedactor
	aliasByCanon := r.invertAliasesLocked()
	r.state.mu.Unlock()

	v, ok := snap.values[canon]
	if !ok {
		return KeyDescription{}, false
	}
	_, secret := secrets[canon]
	display := v.String()
	if secret && redactor != nil {
		display = redactor(display)
	}
	srcs := snap.sources[canon]
	winner := ""
	if len(srcs) > 0 {
		winner = srcs[0]
	}
	return KeyDescription{
		Path:    ParsePath(canon),
		Value:   display,
		Source:  winner,
		Sources: append([]string(nil), srcs...),
		Secret:  secret,
		Aliases: aliasByCanon[canon],
	}, true
}

// MarkSecret records key as containing sensitive data and rebuilds
// the snapshot so [Describe] / [Save] / [Snapshot.String] see the
// updated set immediately. Empty key and closed registry are silent
// no-ops; idempotent. A rebuild failure is logged but not returned —
// the typical caller is the bind walker emitting a side effect.
func (r *Registry) MarkSecret(key string) {
	if key == "" {
		return
	}
	if r.state.closed.Load() {
		return
	}
	fullKey := r.fullKey(key)
	r.state.mu.Lock()
	if r.state.secretKeys == nil {
		r.state.secretKeys = map[string]struct{}{}
	}
	r.state.secretKeys[fullKey] = struct{}{}
	rebuildErr := r.rebuildSnapshotLocked()
	r.state.mu.Unlock()
	if rebuildErr != nil {
		r.state.logger.Warn("recon: MarkSecret rebuild rejected",
			"key", fullKey, "err", rebuildErr)
	}
}

// IsSecret reports whether key has been marked secret, either via
// [MarkSecret] or by the bind walker on a `secret`-tagged field.
func (r *Registry) IsSecret(key string) bool {
	if r.state.closed.Load() {
		return false
	}
	fullKey := r.fullKey(key)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	_, ok := r.state.secretKeys[fullKey]
	return ok
}

// invertAliasesLocked rebuilds the canonical → []alias map from the
// registry's alias map, following chains so an alias-to-alias lands
// on the ultimate canonical. Caller must hold r.state.mu.
func (r *Registry) invertAliasesLocked() map[string][]Path {
	if len(r.state.aliases) == 0 {
		return nil
	}
	out := make(map[string][]Path, len(r.state.aliases))
	for alias, canon := range r.state.aliases {
		final := resolveAliasChain(canon, r.state.aliases)
		out[final] = append(out[final], ParsePath(alias))
	}
	for _, list := range out {
		slices.SortFunc(list, func(a, b Path) int {
			switch {
			case a.String() < b.String():
				return -1
			case a.String() > b.String():
				return 1
			default:
				return 0
			}
		})
	}
	return out
}

// cloneStringSet returns a shallow copy of m.
func cloneStringSet(m map[string]struct{}) map[string]struct{} {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}
