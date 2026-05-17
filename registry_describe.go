package recon

import (
	"slices"
)

// Description is the structured snapshot view [Registry.Describe]
// returns. The Keys slice is sorted by canonical path string so the
// "myapp config show" / "myapp config sources" tooling sees a stable
// order across runs.
//
// Description is a value type — callers may stash it, format it,
// serialize it. Mutating the slice does not affect the registry; the
// underlying [KeyDescription] values are immutable.
type Description struct {
	// Keys is the per-key provenance row for every visible path in
	// the current snapshot. Canonical paths only — aliases are listed
	// on their target's [KeyDescription.Aliases] field rather than as
	// duplicate rows.
	Keys []KeyDescription
}

// KeyDescription carries the per-key information [Description]
// surfaces. The fields mirror §4.13 of the requirements doc; every
// field can be rendered to a "configurable via" line in help output
// or to a single row in a `config show` table.
//
// Value is pre-redacted when [KeyDescription.Secret] is true — the
// redacted form (typically "***") rides on Value so callers don't
// need to consult the secret flag before printing.
type KeyDescription struct {
	// Path is the canonical key (after alias resolution).
	Path Path

	// Value is the resolved value's string form, pre-redacted when
	// the key is marked secret. The redaction comes from the
	// registry's configured secret redactor (default "***", set via
	// [WithSecretRedactor]).
	Value string

	// Source is the name of the source that won the precedence
	// race — empty when no source supplied the key (only the
	// "default" or "explicit" layer did).
	Source string

	// Sources lists every source that had a value for the key, in
	// precedence order (winner first). Includes the reserved
	// provenance labels "explicit" and "default" when those layers
	// supplied a value.
	Sources []string

	// Secret reports whether the registry knows this key is secret —
	// set explicitly via [Registry.MarkSecret] or as a side effect of
	// [Registry.Bind] hitting a `secret`-tagged field. Re-checked on
	// every Describe so a Bind that ran since the last call is
	// visible.
	Secret bool

	// Aliases lists every alias path that resolves to this key.
	// Empty when no aliases were registered.
	Aliases []Path

	// Schema carries a schema fragment describing this key when a
	// validator is registered and exposes per-key fragments. The
	// bundled JSON Schema validator does not currently surface
	// per-key fragments, so Schema is empty for it. Custom
	// validators may populate this through a future hook.
	Schema string
}

// Describe returns a [Description] of the current snapshot. The
// result reflects the registry's state at the call instant — a
// concurrent reload may produce a different Description on the next
// call. Snapshot-stable handling: callers wanting a consistent view
// across multiple calls should pin a snapshot via [Registry.Snapshot]
// and walk it themselves.
//
// Returns an empty Description on a closed or never-loaded registry.
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

	// Canonical paths only — skip the alias entries (they're listed
	// on their target's Aliases field).
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

// DescribeKey is the per-key form of [Describe]. Resolves alias keys
// to their canonical form before lookup so DescribeKey("port")
// returns the same row as DescribeKey("server.port") when port is
// aliased to server.port.
//
// Returns (KeyDescription{}, false) when the key isn't in the
// current snapshot (or when the registry is closed).
func (r *Registry) DescribeKey(key string) (KeyDescription, bool) {
	if r.state.closed.Load() {
		return KeyDescription{}, false
	}
	snap := r.state.snapshot.Load()
	if snap == nil {
		return KeyDescription{}, false
	}
	fullKey := r.fullKey(key)
	// Walk alias to canonical so an alias query lands on the same
	// row Describe would have surfaced for the canonical key.
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

// MarkSecret records key as containing sensitive data. The current
// snapshot is rebuilt so [Snapshot.String], [Registry.Describe],
// [Registry.Save], and any downstream rendering all see the updated
// secret set immediately.
//
// Subsequent reads via [Snapshot.IsSecret] reflect the new mark.
// MarkSecret is idempotent. Calls with an empty key are ignored —
// recon's secret system is path-keyed and rejects the empty path as
// a safety measure against accidentally marking the entire registry
// secret.
//
// A MarkSecret on a closed registry is a silent no-op. A rebuild
// failure (immutable / validator) is logged via the registry's
// logger but does not surface — MarkSecret's contract is "mark this
// path; don't fail" because the typical caller is a Bind walker
// emitting the side effect during a struct walk.
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

// IsSecret reports whether key has been marked secret via
// [MarkSecret] or as a side effect of binding a `secret`-tagged
// field through [Registry.Bind].
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

// invertAliasesLocked rebuilds a canonical → []alias map from the
// registry's alias-to-canonical state. Used by [Describe] /
// [DescribeKey] to populate [KeyDescription.Aliases]. The caller
// MUST hold r.state.mu.
func (r *Registry) invertAliasesLocked() map[string][]Path {
	if len(r.state.aliases) == 0 {
		return nil
	}
	out := make(map[string][]Path, len(r.state.aliases))
	for alias, canon := range r.state.aliases {
		// Follow the chain so aliases that point at another alias
		// land on the ultimate canonical key.
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

// cloneStringSet returns a shallow copy of m. Used by Describe /
// DescribeKey to release the registry mutex before reading.
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
