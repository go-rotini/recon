package recon

import (
	"maps"
	"reflect"
	"slices"
	"sort"
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
// point in time. Once a caller holds a *Snapshot, its data never
// changes. Obtain one from [Registry.Snapshot]; the methods are safe on
// a nil *Snapshot.
//
// Configuration files are held as leaves: a YAML section
// db: {host: h, port: 5} is the two keys db.host and db.port. When one
// layer sets a key and another sets keys below it, every one of them
// stays readable through Get and Bind. Where the snapshot is written as
// a single tree ([Snapshot.AsMap], [Registry.Save] and the validator
// input), the higher-precedence side of such a conflict wins; when both
// come from the same layer, the keys below win.
type Snapshot struct {
	// values maps canonical path string to resolved Value. Alias paths
	// are pre-resolved at build time so values[alias] == values[canon].
	values map[string]Value

	// sources records per-key provenance: every contributing source name
	// (including srcExplicit / srcDefault) in precedence order.
	sources map[string][]string

	// keys is the sorted union of canonical and alias paths; keyStrs
	// holds their canonical strings in the same order.
	keys    []Path
	keyStrs []string

	// sourceNames is the registered source list in precedence order
	// (first = highest).
	sourceNames []string

	// aliases maps alias path string to canonical path string. Frozen
	// for this snapshot.
	aliases map[string]string

	// secretKeys is the set of paths marked secret when this snapshot
	// was built. Frozen so [String] stays consistent across concurrent
	// registry mutations.
	secretKeys map[string]struct{}

	// redactor is the snapshot-time secret redactor.
	redactor func(string) string
}

// IsSecret reports whether p was marked secret when this snapshot was
// built: p, a key p is an alias of, or a parent of either was marked.
func (s *Snapshot) IsSecret(p Path) bool {
	if s == nil {
		return false
	}
	return s.secrets().has(p.String())
}

// secrets returns the matcher for the snapshot's frozen secret set.
func (s *Snapshot) secrets() secretMatcher {
	return secretMatcher{keys: s.secretKeys, aliases: s.aliases}
}

// Get returns the resolved value at p. The bool reports whether any
// layer supplied a value; an empty-string value counts as set.
//
// When no layer set p itself but keys below it are set, Get returns a
// [MapKind] value assembled from them, each at its own precedence, so a
// map section read from a file can be fetched or bound as a whole. Its
// [Value.Source] is the highest-precedence contributor. When a layer
// set a map at a parent of p, Get returns the entry inside it.
func (s *Snapshot) Get(p Path) (Value, bool) {
	if s == nil {
		return Value{}, false
	}
	return s.lookup(p.String(), p)
}

// lookup is [Snapshot.Get] for the canonical string ks of p. p may be
// nil, in which case it is parsed from ks only when needed.
func (s *Snapshot) lookup(ks string, p Path) (Value, bool) {
	if v, ok := s.values[ks]; ok {
		return v, true
	}
	if v, ok := s.lookupInParent(ks); ok {
		return v, true
	}
	return s.lookupSubtree(ks, p)
}

// lookupInParent finds ks inside a map value stored at one of its
// parents.
func (s *Snapshot) lookupInParent(ks string) (Value, bool) {
	var parent Value
	at := -1
	topLevelDelims(ks, func(i int) bool {
		if v, ok := s.values[ks[:i]]; ok {
			parent, at = v, i
			return false
		}
		return true
	})
	if at < 0 || parent.Kind() != MapKind {
		return Value{}, false
	}
	cur := parent
	for _, seg := range ParsePath(ks[at+1:]) {
		m, err := cur.AsMap()
		if err != nil {
			return Value{}, false
		}
		next, ok := m[seg]
		if !ok {
			return Value{}, false
		}
		cur = next
	}
	return cur.withSource(parent.Source()), true
}

// lookupSubtree assembles a [MapKind] value from the keys below ks.
func (s *Snapshot) lookupSubtree(ks string, p Path) (Value, bool) {
	if ks == "" {
		return Value{}, false
	}
	prefix := ks + DefaultDelimiter
	lo := sort.SearchStrings(s.keyStrs, prefix)
	hi := lo
	for hi < len(s.keyStrs) && strings.HasPrefix(s.keyStrs[hi], prefix) {
		hi++
	}
	if lo == hi {
		return Value{}, false
	}
	if p == nil {
		p = ParsePath(ks)
	}
	out := map[string]any{}
	best, bestRank := "", -1
	ranks := s.rankIndex()
	found := false
	for i := lo; i < hi; i++ {
		k := s.keyStrs[i]
		if _, isAlias := s.aliases[k]; isAlias {
			continue
		}
		v := s.values[k]
		setNested(out, s.keys[i][len(p):], unwrapValueDeep(v))
		found = true
		if r := rankFor(ranks, v.Source(), len(s.sourceNames)); bestRank < 0 || r < bestRank {
			best, bestRank = v.Source(), r
		}
	}
	if !found {
		return Value{}, false
	}
	return NewValue(out).withSource(best), true
}

// rankIndex maps each registered source name to its precedence rank.
func (s *Snapshot) rankIndex() map[string]int {
	out := make(map[string]int, len(s.sourceNames))
	for i, n := range s.sourceNames {
		out[n] = i + 1
	}
	return out
}

// rankFor is the precedence rank of the layer named name: 0 for
// explicit, 1+i for the i-th source, n+1 for default.
func rankFor(ranks map[string]int, name string, n int) int {
	switch name {
	case srcExplicit:
		return 0
	case srcDefault:
		return n + 1
	}
	if r, ok := ranks[name]; ok {
		return r
	}
	return n + 1
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
// paths on the canonical delimiter. List and map values are plain
// []any and map[string]any. Mutating the returned map does not affect
// the snapshot.
//
// AsMap does not redact secret-marked values; downstream validators
// need real data. Use [Snapshot.String] for a redacted human view or
// [Registry.Save] for a serialized redacted view.
func (s *Snapshot) AsMap() map[string]any {
	if s == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(s.values))
	idx := make([]int, 0, len(s.keyStrs))
	for i, k := range s.keyStrs {
		if _, isAlias := s.aliases[k]; !isAlias {
			idx = append(idx, i)
		}
	}
	shadowed := s.shadowedAmong(idx)
	for _, i := range idx {
		k := s.keyStrs[i]
		if _, drop := shadowed[k]; drop {
			continue
		}
		setNested(out, s.keys[i], unwrapValueDeep(s.values[k]))
	}
	return out
}

// shadowedAmong returns the keys, out of the keyStrs entries at idx,
// that lose a conflict with a parent or child key in the same set: the
// side from the lower-precedence layer. Keys from the same layer both
// stay, and the keys below win when written in sorted order.
func (s *Snapshot) shadowedAmong(idx []int) map[string]struct{} {
	ranks := s.rankIndex()
	n := len(s.sourceNames)
	rank := make(map[string]int, len(idx))
	for _, i := range idx {
		k := s.keyStrs[i]
		winner := ""
		if srcs := s.sources[k]; len(srcs) > 0 {
			winner = srcs[0]
		}
		rank[k] = rankFor(ranks, winner, n)
	}
	var out map[string]struct{}
	drop := func(k string) {
		if out == nil {
			out = map[string]struct{}{}
		}
		out[k] = struct{}{}
	}
	for _, i := range idx {
		k := s.keyStrs[i]
		rk := rank[k]
		topLevelDelims(k, func(j int) bool {
			ra, ok := rank[k[:j]]
			switch {
			case !ok:
			case ra < rk:
				drop(k)
			case rk < ra:
				drop(k[:j])
			}
			return true
		})
	}
	return out
}

// String returns a compact human-readable form of the snapshot. Keys
// marked secret are rendered through the snapshot's redactor.
func (s *Snapshot) String() string {
	if s == nil || len(s.keys) == 0 {
		return "recon.Snapshot{}"
	}
	secrets := s.secrets()
	var b strings.Builder
	b.WriteString("recon.Snapshot{\n")
	for _, ks := range s.keyStrs {
		v := s.values[ks]
		text := v.String()
		if s.redactor != nil && secrets.has(ks) {
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
// nodes as needed. A non-map value in the way is replaced by a map.
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

// secretMatcher answers whether a key is secret: the key, the key it is
// an alias of, or a parent of either was marked.
type secretMatcher struct {
	keys    map[string]struct{}
	aliases map[string]string
}

// has reports whether the canonical key string ks is secret.
func (m secretMatcher) has(ks string) bool {
	if len(m.keys) == 0 {
		return false
	}
	if m.matches(ks) {
		return true
	}
	if canon := resolveAliasChain(ks, m.aliases); canon != ks {
		return m.matches(canon)
	}
	return false
}

// matches reports whether ks or a parent of it is in the set, directly
// or through an alias marked secret.
func (m secretMatcher) matches(ks string) bool {
	if m.marked(ks) {
		return true
	}
	hit := false
	topLevelDelims(ks, func(i int) bool {
		hit = m.marked(ks[:i])
		return !hit
	})
	return hit
}

// marked reports whether ks itself is in the set, or is the canonical
// key of an alias in the set.
func (m secretMatcher) marked(ks string) bool {
	if _, ok := m.keys[ks]; ok {
		return true
	}
	for alias, canon := range m.aliases {
		if _, ok := m.keys[alias]; ok && resolveAliasChain(canon, m.aliases) == ks {
			return true
		}
	}
	return false
}

// snapshotInputs bundles the fields [buildSnapshot] needs. Extracting
// it lets buildSnapshot run without holding the registry mutex.
type snapshotInputs struct {
	sources   []Source
	explicits map[string]any
	defaults  map[string]any
	aliases   map[string]string // alias → canonical
	pins      map[string]string // canonical → pinned source name
	bound     map[string]struct{}

	secretKeys map[string]struct{}
	redactor   func(string) string
	merge      MergeStrategy

	// aliasesOf maps a canonical path string to the alias paths that
	// resolve to it, sorted. Filled by buildSnapshot.
	aliasesOf map[string][]Path
}

// buildSnapshot resolves every candidate key under is and produces a
// frozen *Snapshot.
//
// Resolution order per canonical key: explicit > pinned-source > sources
// in precedence order > default. A source whose Get returns an error is
// treated as not having the key; the error is not reported. Map values
// given to Set and SetDefault are resolved leaf by leaf.
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

	is.explicits = flattenLayer(is.explicits)
	is.defaults = flattenLayer(is.defaults)
	is.aliasesOf = invertAliases(is.aliases)

	candidates := collectCandidateKeys(is)
	// First resolve each canonical key once; then stamp aliases.
	paths := make(map[string]Path, len(candidates))
	for keyStr, keyPath := range candidates {
		canonStr := resolveAliasChain(keyStr, is.aliases)
		if _, done := paths[canonStr]; done {
			continue
		}
		canon := keyPath
		if canonStr != keyStr || canon == nil {
			canon = ParsePath(canonStr)
		}
		paths[canonStr] = canon
		val, srcs, found := resolveKey(canon, canonStr, is)
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

	hidden := duplicateSpellings(s, is, candidates)
	s.keyStrs = make([]string, 0, len(s.values))
	for k := range s.values {
		if _, skip := hidden[k]; !skip {
			s.keyStrs = append(s.keyStrs, k)
		}
	}
	slices.Sort(s.keyStrs)
	s.keyStrs = hideBoundParents(s, is, candidates)
	s.keys = make([]Path, len(s.keyStrs))
	for i, k := range s.keyStrs {
		if p, ok := paths[k]; ok {
			s.keys[i] = p
		} else {
			s.keys[i] = ParsePath(k)
		}
	}
	return s
}

// boundOnly reports whether k is in the snapshot only because a bind
// read it: no source enumerates it and no Set, SetDefault or alias
// names it.
func boundOnly(k string, is snapshotInputs, candidates map[string]Path) bool {
	if _, ok := is.bound[k]; !ok || candidates[k] != nil {
		return false
	}
	if _, ok := is.explicits[k]; ok {
		return false
	}
	if _, ok := is.defaults[k]; ok {
		return false
	}
	_, alias := is.aliases[k]
	return !alias
}

// duplicateSpellings returns the source-enumerated keys that name the
// same native entry as a bound key won by the same source, such as
// max.conns for a bind of max_conns when both read APP_MAX_CONNS. They
// stay readable but are left out of [Snapshot.Keys] and the views built
// from it, so the value is listed once, under the bound spelling.
func duplicateSpellings(s *Snapshot, is snapshotInputs, candidates map[string]Path) map[string]struct{} {
	var hidden map[string]struct{}
	index := map[string]map[string][]string{} // source → native name → keys
	for b := range is.bound {
		if !boundOnly(b, is, candidates) {
			continue
		}
		srcs, ok := s.sources[b]
		if !ok {
			continue
		}
		var nk nativeKeyer
		for _, src := range is.sources {
			if src.Name() == srcs[0] {
				nk, _ = src.(nativeKeyer)
				break
			}
		}
		if nk == nil {
			continue
		}
		idx, built := index[srcs[0]]
		if !built {
			idx = map[string][]string{}
			for e, p := range candidates {
				if es, ok := s.sources[e]; p != nil && ok && es[0] == srcs[0] {
					n := nk.nativeKey(p)
					idx[n] = append(idx[n], e)
				}
			}
			index[srcs[0]] = idx
		}
		for _, e := range idx[nk.nativeKey(ParsePath(b))] {
			if e == b {
				continue
			}
			if hidden == nil {
				hidden = map[string]struct{}{}
			}
			hidden[e] = struct{}{}
		}
	}
	return hidden
}

// hideBoundParents drops from the sorted s.keyStrs each bound-only key
// holding a map whose entries are listed as keys below it, so the
// entries are not listed twice.
func hideBoundParents(s *Snapshot, is snapshotInputs, candidates map[string]Path) []string {
	if len(is.bound) == 0 {
		return s.keyStrs
	}
	all := s.keyStrs
	out := make([]string, 0, len(all))
	for _, k := range all {
		if s.values[k].Kind() == MapKind && boundOnly(k, is, candidates) {
			prefix := k + DefaultDelimiter
			if j := sort.SearchStrings(all, prefix); j < len(all) && strings.HasPrefix(all[j], prefix) {
				continue
			}
		}
		out = append(out, k)
	}
	return out
}

// collectCandidateKeys returns the union of explicit, default, alias,
// bound and source-supplied keys. A source key carries its parsed Path;
// the others carry nil.
func collectCandidateKeys(is snapshotInputs) map[string]Path {
	out := make(map[string]Path)
	for k := range is.explicits {
		out[k] = nil
	}
	for k := range is.defaults {
		out[k] = nil
	}
	for k := range is.aliases {
		out[k] = nil
	}
	for k := range is.bound {
		out[k] = nil
	}
	for _, s := range is.sources {
		for _, p := range s.Keys() {
			out[p.String()] = p
		}
	}
	return out
}

// invertAliases maps each canonical key to the alias paths that
// resolve to it, following chains, sorted by string.
func invertAliases(aliases map[string]string) map[string][]Path {
	if len(aliases) == 0 {
		return nil
	}
	byCanon := make(map[string][]string, len(aliases))
	for alias := range aliases {
		canon := resolveAliasChain(alias, aliases)
		byCanon[canon] = append(byCanon[canon], alias)
	}
	out := make(map[string][]Path, len(byCanon))
	for canon, list := range byCanon {
		slices.Sort(list)
		ps := make([]Path, len(list))
		for i, a := range list {
			ps[i] = ParsePath(a)
		}
		out[canon] = ps
	}
	return out
}

// flattenLayer expands every non-empty map value in an explicit or
// default layer into one entry per leaf, so each leaf takes its own
// place in the precedence chain. An entry set at a deeper key wins
// over the same leaf reached through a map at a shallower one. m is
// returned unchanged when it holds no map.
func flattenLayer(m map[string]any) map[string]any {
	var mapKeys []string
	for k, v := range m {
		if isNonEmptyMap(v) {
			mapKeys = append(mapKeys, k)
		}
	}
	if len(mapKeys) == 0 {
		return m
	}
	out := make(map[string]any, len(m))
	slices.SortFunc(mapKeys, func(a, b string) int {
		return len(ParsePath(a)) - len(ParsePath(b))
	})
	for _, k := range mapKeys {
		flattenInto(out, ParsePath(k), NewValue(m[k]))
	}
	for k, v := range m {
		if !isNonEmptyMap(v) {
			out[k] = v
		}
	}
	return out
}

// flattenInto writes v's leaves under p into out.
func flattenInto(out map[string]any, p Path, v Value) {
	if m, err := v.AsMap(); err == nil && len(m) > 0 {
		for k, e := range m {
			flattenInto(out, p.Append(k), e)
		}
		return
	}
	out[p.String()] = v
}

// isNonEmptyMap reports whether v is a map with string keys and at
// least one entry, or a [MapKind] [Value] with one.
func isNonEmptyMap(v any) bool {
	if val, ok := v.(Value); ok {
		m, err := val.AsMap()
		return err == nil && len(m) > 0
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String && rv.Len() > 0
}
