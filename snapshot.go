package recon

import (
	"maps"
	"slices"
	"strings"
)

// Reserved source names used in provenance reporting when the winning
// value came from the registry's override / fallback layers rather than
// a registered [Source].
const (
	srcExplicit = "explicit" // [Registry.Set] values — highest precedence
	srcDefault  = "default"  // [Registry.SetDefault] values — lowest precedence
)

// reservedSourceNames are rejected by [Registry.AddSource].
var reservedSourceNames = map[string]struct{}{
	srcExplicit: {},
	srcDefault:  {},
}

// Snapshot is the immutable, fully-resolved view of a [Registry] at one
// point in time. It is atomic-stored on the registry; once a caller
// holds a *Snapshot, the underlying data never mutates. Construct via
// [buildSnapshot] — callers do not construct Snapshots directly.
type Snapshot struct {
	// values maps canonical path string to resolved Value. Alias paths
	// are pre-resolved at build time so values[alias] == values[canon].
	values map[string]Value

	// sources records per-key provenance: every contributing source name
	// (including srcExplicit / srcDefault) in precedence order.
	sources map[string][]string

	// keys is the sorted union of canonical and alias paths.
	keys []Path

	// sourceNames is the registered source list in precedence order
	// (first = highest).
	sourceNames []string

	// aliases maps alias path string to canonical path string. Frozen
	// for this snapshot.
	aliases map[string]string

	// secretKeys is the set of canonical paths marked secret when this
	// snapshot was built. Frozen so [String] stays consistent across
	// concurrent registry mutations.
	secretKeys map[string]struct{}

	// redactor is the snapshot-time secret redactor.
	redactor func(string) string
}

// IsSecret reports whether p was marked secret when this snapshot was
// built.
func (s *Snapshot) IsSecret(p Path) bool {
	if s == nil {
		return false
	}
	_, ok := s.secretKeys[p.String()]
	return ok
}

// Get returns the resolved value at p. The bool reports whether any
// layer supplied a value; an empty-string value counts as set.
func (s *Snapshot) Get(p Path) (Value, bool) {
	if s == nil {
		return Value{}, false
	}
	v, ok := s.values[p.String()]
	return v, ok
}

// Keys returns every known path sorted by canonical string form. The
// returned slice aliases the snapshot's storage and must not be mutated.
func (s *Snapshot) Keys() []Path {
	if s == nil {
		return nil
	}
	return s.keys
}

// SourceFor returns the precedence-ordered list of source names that
// supplied a value for p. The first element is the winner; subsequent
// elements are shadowed entries.
func (s *Snapshot) SourceFor(p Path) []string {
	if s == nil {
		return nil
	}
	return s.sources[p.String()]
}

// AsMap returns the snapshot as a nested map[string]any, splitting
// paths on the canonical delimiter. Mutating the returned map does not
// affect the snapshot.
//
// AsMap does not redact secret-marked values; downstream validators
// need real data. Use [Snapshot.String] for a redacted human view or
// [Registry.Save] for a serialized redacted view.
func (s *Snapshot) AsMap() map[string]any {
	if s == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(s.values))
	for k, v := range s.values {
		if _, isAlias := s.aliases[k]; isAlias {
			continue
		}
		setNested(out, ParsePath(k), v.Any())
	}
	return out
}

// String returns a compact human-readable form of the snapshot. Keys
// marked secret are rendered through the snapshot's redactor.
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

// setNested writes v at p in m, creating intermediate map[string]any
// nodes as needed.
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

// snapshotInputs bundles the fields [buildSnapshot] needs. Extracting
// it lets buildSnapshot run without holding the registry mutex.
type snapshotInputs struct {
	sources   []Source
	explicits map[string]any
	defaults  map[string]any
	aliases   map[string]string // alias → canonical
	pins      map[string]string // canonical → pinned source name

	secretKeys map[string]struct{}
	redactor   func(string) string
	merge      MergeStrategy
}

// buildSnapshot resolves every candidate key under is and produces a
// frozen *Snapshot.
//
// Resolution order per canonical key: explicit > pinned-source > sources
// in precedence order > default. Source-side errors during candidate
// resolution are dropped here; [Registry.rebuildSnapshotLocked]
// surfaces them through the configured [ErrorBehavior].
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

	// First resolve each canonical key once; then stamp aliases.
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
	for aliasStr := range is.aliases {
		canonStr := resolveAliasChain(aliasStr, is.aliases)
		if v, ok := s.values[canonStr]; ok {
			s.values[aliasStr] = v
			s.sources[aliasStr] = s.sources[canonStr]
		}
	}

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

// collectCandidateKeys returns the union of explicit, default, alias,
// and source-supplied keys.
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
