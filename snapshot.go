package recon

import (
	"maps"
	"slices"
	"strings"
)

// Reserved source names that appear in [KeyDescription.Source] / [.Sources]
// when the winning value came from the registry's own override / fallback
// layers rather than a registered [Source].
const (
	srcExplicit = "explicit" // Registry.Set values — highest precedence
	srcDefault  = "default"  // Registry.SetDefault values — lowest precedence
)

// reservedSourceNames lists source-name strings the registry uses internally
// for provenance reporting. AddSource rejects a Source whose Name() is one of
// these — see [Registry.AddSource].
var reservedSourceNames = map[string]struct{}{
	srcExplicit: {},
	srcDefault:  {},
}

// Snapshot is the immutable, fully-resolved view of a [Registry] at a single
// point in time. It is built by [Registry.New] / [Registry.Reload] and
// atomic-stored into the registry's snapshot slot; every read goes through a
// single [atomic.Pointer.Load] and a single map lookup. Snapshots are safe to
// share across goroutines and across registry reloads — once a caller has a
// *Snapshot pointer, the underlying data never mutates.
//
// Construction is via [buildSnapshot]; callers do not construct Snapshots
// directly.
type Snapshot struct {
	// values is the canonical-path → resolved Value map. Keys are canonical
	// strings (Path.String()); aliases are pre-resolved at build time so that
	// values[alias.String()] returns the same Value as
	// values[canonical.String()].
	values map[string]Value

	// sources records per-key provenance: every source name (registered
	// sources plus the reserved srcExplicit / srcDefault) that had a value
	// for the key, listed in precedence order.
	sources map[string][]string

	// keys is the sorted union of every key any layer of the registry knows
	// about — the result of [Registry.AllKeys].
	keys []Path

	// sourceNames is the registered-source name list in precedence order
	// (first = highest). Reported by [Registry.Sources].
	sourceNames []string

	// aliases is the alias → canonical map, post-cycle-check, frozen for
	// this snapshot. Read-only.
	aliases map[string]string

	// secretKeys is the set of canonical paths the registry had marked
	// secret at the moment this snapshot was built. Frozen with the
	// snapshot so [Snapshot.String] and downstream introspection can
	// redact consistently with the snapshot's view.
	secretKeys map[string]struct{}

	// redactor is the snapshot-time secret redactor. Frozen so a
	// concurrent [Registry.WithSecretRedactor] swap does not change how
	// a previously-handed-out snapshot renders.
	redactor func(string) string
}

// IsSecret reports whether p was marked secret at the time this
// snapshot was built. Useful when handing a snapshot to a logger or
// pretty-printer that needs to redact independently of the live
// registry's current state.
func (s *Snapshot) IsSecret(p Path) bool {
	if s == nil {
		return false
	}
	_, ok := s.secretKeys[p.String()]
	return ok
}

// Get returns the resolved value at p. The bool reports whether any source —
// or the explicit / default layers — supplied a value; an empty-string value
// counts as set (matching [Source] semantics).
func (s *Snapshot) Get(p Path) (Value, bool) {
	if s == nil {
		return Value{}, false
	}
	v, ok := s.values[p.String()]
	return v, ok
}

// Keys returns every path the snapshot knows about, sorted by canonical
// string form. The returned slice aliases the snapshot's storage and MUST
// NOT be mutated.
func (s *Snapshot) Keys() []Path {
	if s == nil {
		return nil
	}
	return s.keys
}

// SourceFor returns the precedence-ordered list of source names that had a
// value for p in this snapshot. The first element is the winner; subsequent
// elements are shadowed entries (useful for `myapp config explain` output).
// Returns an empty slice when no source supplied the key.
func (s *Snapshot) SourceFor(p Path) []string {
	if s == nil {
		return nil
	}
	return s.sources[p.String()]
}

// AsMap returns the snapshot as a nested map[string]any. Paths are
// split on the canonical delimiter; leaf values are the underlying
// Go values returned by [Value.Any]. Mutating the returned map does
// not affect the snapshot.
//
// AsMap does NOT redact secret-marked values — the validator (and
// any other downstream consumer) needs to see real data to do its
// job. Use [Snapshot.String] for a human-readable redacted view, or
// [Registry.Save] for a serialized redacted view.
//
// Useful for handing the resolved configuration to a
// [SchemaValidator] or to a format encoder that expects nested
// maps.
func (s *Snapshot) AsMap() map[string]any {
	if s == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(s.values))
	for k, v := range s.values {
		// Skip alias entries — their canonical key is already in the map.
		if _, isAlias := s.aliases[k]; isAlias {
			continue
		}
		setNested(out, ParsePath(k), v.Any())
	}
	return out
}

// String returns a compact, human-readable representation of the
// snapshot. Keys marked secret (via [Registry.MarkSecret] or a
// `secret`-tagged Bind field at the time the snapshot was built) are
// rendered through the snapshot's redactor — the default writes "***"
// — so a Snapshot.String() never leaks secret payloads into a log line.
func (s *Snapshot) String() string {
	if s == nil || len(s.keys) == 0 {
		return "recon.Snapshot{}"
	}
	var b strings.Builder
	b.WriteString("recon.Snapshot{\n")
	for _, p := range s.keys {
		ks := p.String()
		v := s.values[ks]
		text := v.String()
		if _, isSecret := s.secretKeys[ks]; isSecret && s.redactor != nil {
			text = s.redactor(text)
		}
		b.WriteString("  ")
		b.WriteString(ks)
		b.WriteString(" = ")
		b.WriteString(text)
		if src := v.Source(); src != "" {
			b.WriteString("  (from ")
			b.WriteString(src)
			b.WriteString(")")
		}
		b.WriteByte('\n')
	}
	b.WriteByte('}')
	return b.String()
}

// setNested writes v at path p in m, creating intermediate map[string]any
// nodes as needed. Used by [Snapshot.AsMap] to convert flat-keyed storage
// back into a nested map for validators and encoders.
func setNested(m map[string]any, p Path, v any) {
	if len(p) == 0 {
		return
	}
	cur := m
	for i, seg := range p {
		if i == len(p)-1 {
			cur[seg] = v
			return
		}
		next, ok := cur[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
}

// snapshotInputs is the working bundle [buildSnapshot] receives. It is a
// straight extraction of the fields a snapshot rebuild needs from the
// registry; keeping it separate from registryOptions lets buildSnapshot stay
// pure (no registry mutex held during the build).
type snapshotInputs struct {
	sources   []Source
	explicits map[string]any
	defaults  map[string]any
	aliases   map[string]string // alias path string → canonical path string
	pins      map[string]string // canonical path string → pinned source name

	// secretKeys is the registry-tracked secret set the snapshot
	// should carry. Frozen with the snapshot so downstream redaction
	// matches the snapshot's view.
	secretKeys map[string]struct{}

	// redactor is the snapshot-time redactor; defaults to "***".
	redactor func(string) string

	// merge controls how multi-source contributions to the same key
	// are combined. [MergeShadow] (the default) keeps "first higher
	// wins"; [MergeAppend] concatenates slices and deep-merges maps
	// across the source chain.
	merge MergeStrategy
}

// buildSnapshot resolves every key in is and produces a frozen *Snapshot.
//
// The algorithm:
//  1. Union the candidate keys: aliases ∪ explicits ∪ defaults ∪ ⋃source.Keys().
//  2. For each candidate, follow any alias chain to its canonical form.
//  3. Resolve the canonical key: explicit > pinned-source > sources in order > default.
//  4. Record provenance — every source (or "explicit" / "default") that had
//     a value for the canonical key, in precedence order.
//  5. Stamp alias entries with the same resolved value so Get(alias) and
//     Get(canonical) return identical results.
//
// Source-side errors during candidate construction (Source.Get
// returning a non-nil error) are silently skipped — the registry's
// [rebuildSnapshotLocked] is responsible for surfacing them through
// the configured [ErrorBehavior]. buildSnapshot itself is
// infallible by design.
func buildSnapshot(is snapshotInputs) *Snapshot {
	s := &Snapshot{
		values:      map[string]Value{},
		sources:     map[string][]string{},
		sourceNames: make([]string, len(is.sources)),
		aliases:     map[string]string{},
		secretKeys:  map[string]struct{}{},
		redactor:    is.redactor,
	}
	for i, src := range is.sources {
		s.sourceNames[i] = src.Name()
	}
	maps.Copy(s.aliases, is.aliases)
	maps.Copy(s.secretKeys, is.secretKeys)

	candidates := collectCandidateKeys(is)

	// Two-pass build: first resolve every canonical key once, then stamp
	// alias entries from the canonical results. Avoids re-doing the source
	// walk per alias.
	resolved := make(map[string]struct{}, len(candidates))
	for keyStr := range candidates {
		canonStr := resolveAliasChain(keyStr, is.aliases)
		if _, done := resolved[canonStr]; done {
			continue
		}
		resolved[canonStr] = struct{}{}

		canon := ParsePath(canonStr)
		val, srcs, found := resolveKey(canon, is)
		if !found {
			continue
		}
		s.values[canonStr] = val
		s.sources[canonStr] = srcs
	}
	// Stamp aliases.
	for aliasStr := range is.aliases {
		canonStr := resolveAliasChain(aliasStr, is.aliases)
		if v, ok := s.values[canonStr]; ok {
			s.values[aliasStr] = v
			s.sources[aliasStr] = s.sources[canonStr]
		}
	}

	// Materialize the sorted, deduplicated key list. Aliases are included
	// so AllKeys reports every queryable path.
	keyStrings := make([]string, 0, len(s.values))
	for k := range s.values {
		keyStrings = append(keyStrings, k)
	}
	slices.Sort(keyStrings)
	s.keys = make([]Path, len(keyStrings))
	for i, k := range keyStrings {
		s.keys[i] = ParsePath(k)
	}
	return s
}

// collectCandidateKeys returns every path string the snapshot must consider —
// the union of explicit / default / alias / source keys.
func collectCandidateKeys(is snapshotInputs) map[string]struct{} {
	out := make(map[string]struct{})
	for k := range is.explicits {
		out[k] = struct{}{}
	}
	for k := range is.defaults {
		out[k] = struct{}{}
	}
	for k := range is.aliases {
		out[k] = struct{}{}
	}
	for _, s := range is.sources {
		for _, p := range s.Keys() {
			out[p.String()] = struct{}{}
		}
	}
	return out
}
