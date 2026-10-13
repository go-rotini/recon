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
// stays readable through Get and Bind, and a scalar read of the key
// itself gets its own value. Where the snapshot is written as a single
// tree ([Snapshot.AsMap], [Registry.Save] and the validator input), or
// the key is read as a map (a map or `any` field, GetStringMap, or Get
// of a key holding a null), the higher-precedence side of such a
// conflict wins; when both come from the same layer, the keys below
// win.
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

	// lookupKeys and lookupPaths list every resolved key, sorted, for
	// reads: unlike keyStrs they keep the spellings left out of the
	// listed views. They share keyStrs and keys when none is left out.
	lookupKeys  []string
	lookupPaths []Path

	// overridden holds the keys whose own value yields to the keys below
	// them when read as a map: a listed key below comes from a layer of
	// higher precedence, or from the same layer while the key's own
	// value is not a map.
	overridden map[string]struct{}

	// inParent holds the keys whose value yields to an entry for them
	// inside a map that a layer of higher precedence set at a parent.
	inParent map[string]struct{}

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
// When keys below p are set and no layer set p itself, Get returns a
// [MapKind] value assembled from them, so a map section read from a
// file can be fetched or bound as a whole. It does the same when p
// holds a null and a key below comes from a layer of the same or
// higher precedence. Each key in the assembled map resolves as in
// [Snapshot.AsMap]; its [Value.Source] is the highest-precedence
// contributor. Any other value set at p is returned as is. When a
// layer set a map holding an entry for p at a parent of p, Get returns
// that entry unless p itself was set by a layer of the same or higher
// precedence.
func (s *Snapshot) Get(p Path) (Value, bool) {
	if s == nil {
		return Value{}, false
	}
	return s.read(p.String(), p, readGet)
}

// readMode selects how a read treats a key's own value when keys below
// it are also set.
type readMode uint8

const (
	// readExact returns the key's own value: the read of a scalar.
	readExact readMode = iota
	// readGet is [Snapshot.Get]: a null at the key yields to the keys
	// below it when they win over it.
	readGet
	// readSection is a read that wants a map (a map or `any` field,
	// GetStringMap, UnmarshalKey into a map): a null or scalar at the
	// key yields to the keys below it when they win over it.
	readSection
)

// lookup is [Snapshot.Get] for the canonical string ks of p. p may be
// nil, in which case it is parsed from ks only when needed.
func (s *Snapshot) lookup(ks string, p Path) (Value, bool) {
	return s.read(ks, p, readGet)
}

// read resolves ks as mode asks. p is ks parsed, or nil.
func (s *Snapshot) read(ks string, p Path, mode readMode) (Value, bool) {
	if v, ok := s.values[ks]; ok {
		if len(s.inParent) == 0 && (mode == readExact || (mode == readGet && v.Kind() != NullKind)) {
			return v, true
		}
		canon := ks
		if _, isAlias := s.aliases[ks]; isAlias {
			canon, p = resolveAliasChain(ks, s.aliases), nil
		}
		if _, yields := s.inParent[canon]; yields {
			if pv, ok := s.lookupInParent(canon); ok {
				return pv, true
			}
		}
		if mode == readExact || (mode == readGet && v.Kind() != NullKind) {
			return v, true
		}
		if _, below := s.overridden[canon]; !below {
			return v, true
		}
		if sv, ok := s.lookupSubtree(canon, p); ok {
			return sv, true
		}
		return v, true
	}
	if v, ok := s.lookupInParent(ks); ok {
		return v, true
	}
	return s.lookupSubtree(ks, p)
}

// lookupInParent finds ks inside a map value stored at one of its
// parents. When several parents hold it, the one from the
// highest-precedence layer wins, and the nearest parent among equals.
func (s *Snapshot) lookupInParent(ks string) (Value, bool) {
	var (
		found    Value
		foundAt  string
		ok       bool
		ranks    map[string]int
		bestRank int
	)
	topLevelDelims(ks, func(i int) bool {
		parent, has := s.values[ks[:i]]
		if !has || parent.Kind() != MapKind {
			return true
		}
		v, hit := entryIn(parent, ks[i+1:])
		if !hit {
			return true
		}
		if ok {
			if ranks == nil {
				ranks = s.rankIndex()
				bestRank = s.rankOf(ranks, foundAt)
			}
			r := s.rankOf(ranks, ks[:i])
			if r > bestRank {
				return true
			}
			bestRank = r
		}
		found, foundAt, ok = v.withSource(parent.Source()), ks[:i], true
		return true
	})
	return found, ok
}

// entryIn returns the entry at the path rest inside the map value m.
func entryIn(m Value, rest string) (Value, bool) {
	cur := m
	for _, seg := range ParsePath(rest) {
		mm, err := cur.AsMap()
		if err != nil {
			return Value{}, false
		}
		next, ok := mm[seg]
		if !ok {
			return Value{}, false
		}
		cur = next
	}
	return cur, true
}

// rankOf is the precedence rank of the layer that won key k.
func (s *Snapshot) rankOf(ranks map[string]int, k string) int {
	winner := ""
	if srcs := s.sources[k]; len(srcs) > 0 {
		winner = srcs[0]
	}
	return rankFor(ranks, winner, len(s.sourceNames))
}

// lookupSubtree assembles a [MapKind] value from the keys below ks.
// A conflict between keys below ks resolves as in [Snapshot.AsMap]. It
// uses the listed keys, so a value read through two spellings appears
// once, and the spellings left out of the listing only when no listed
// key lies below ks.
func (s *Snapshot) lookupSubtree(ks string, p Path) (Value, bool) {
	if ks == "" {
		return Value{}, false
	}
	prefix := ks + DefaultDelimiter
	keys, paths := s.keysBelow(s.keyStrs, s.keys, prefix)
	if len(keys) == 0 && len(s.lookupKeys) > len(s.keyStrs) {
		keys, paths = s.keysBelow(s.lookupKeys, s.lookupPaths, prefix)
	}
	if len(keys) == 0 {
		return Value{}, false
	}
	if p == nil {
		p = ParsePath(ks)
	}
	shadowed := s.shadowedKeys(keys)
	ranks := s.rankIndex()
	out := map[string]any{}
	best, bestRank := "", -1
	for i, k := range keys {
		if _, drop := shadowed[k]; drop {
			continue
		}
		v := s.values[k]
		setNested(out, paths[i][len(p):], unwrapValueDeep(v))
		if r := rankFor(ranks, v.Source(), len(s.sourceNames)); bestRank < 0 || r < bestRank {
			best, bestRank = v.Source(), r
		}
	}
	return NewValue(out).withSource(best), true
}

// keysBelow returns the keys of the sorted list strs, other than
// aliases, that start with prefix, and their paths from the matching
// list paths.
func (s *Snapshot) keysBelow(strs []string, paths []Path, prefix string) ([]string, []Path) {
	lo := sort.SearchStrings(strs, prefix)
	hi := lo
	for hi < len(strs) && strings.HasPrefix(strs[hi], prefix) {
		hi++
	}
	var keys []string
	var ps []Path
	for i := lo; i < hi; i++ {
		if _, isAlias := s.aliases[strs[i]]; isAlias {
			continue
		}
		keys = append(keys, strs[i])
		ps = append(ps, paths[i])
	}
	return keys, ps
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
	keys := make([]string, len(idx))
	for j, i := range idx {
		keys[j] = s.keyStrs[i]
	}
	return s.shadowedKeys(keys)
}

// shadowedKeys is [Snapshot.shadowedAmong] for a list of keys.
func (s *Snapshot) shadowedKeys(keys []string) map[string]struct{} {
	ranks := s.rankIndex()
	rank := make(map[string]int, len(keys))
	for _, k := range keys {
		rank[k] = s.rankOf(ranks, k)
	}
	var out map[string]struct{}
	drop := func(k string) {
		if out == nil {
			out = map[string]struct{}{}
		}
		out[k] = struct{}{}
	}
	for _, k := range keys {
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
	s.lookupKeys, s.lookupPaths = s.keyStrs, s.keys
	if len(s.keyStrs) < len(s.values) {
		s.lookupKeys = slices.Sorted(maps.Keys(s.values))
		s.lookupPaths = make([]Path, len(s.lookupKeys))
		for i, k := range s.lookupKeys {
			if p, ok := paths[k]; ok {
				s.lookupPaths[i] = p
			} else {
				s.lookupPaths[i] = ParsePath(k)
			}
		}
	}
	s.overridden = overriddenParents(s)
	s.inParent = parentMapWins(s)
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

// overriddenParents returns the keys of s whose own value yields, on
// a map read, to the keys below them; see the overridden field.
func overriddenParents(s *Snapshot) map[string]struct{} {
	var out map[string]struct{}
	var ranks map[string]int
	for _, k := range s.keyStrs {
		if _, isAlias := s.aliases[k]; isAlias {
			continue
		}
		rk := -1
		topLevelDelims(k, func(j int) bool {
			a := k[:j]
			av, ok := s.values[a]
			if !ok {
				return true
			}
			if _, isAlias := s.aliases[a]; isAlias {
				return true
			}
			if ranks == nil {
				ranks = s.rankIndex()
			}
			if rk < 0 {
				rk = s.rankOf(ranks, k)
			}
			ra := s.rankOf(ranks, a)
			if rk < ra || (rk == ra && av.Kind() != MapKind) {
				if out == nil {
					out = map[string]struct{}{}
				}
				out[a] = struct{}{}
			}
			return true
		})
	}
	return out
}

// parentMapWins returns the keys of s that a map value set at one of
// their parents by a layer of higher precedence also holds; see the
// inParent field.
func parentMapWins(s *Snapshot) map[string]struct{} {
	hasMap := false
	for _, v := range s.values {
		if v.Kind() == MapKind {
			hasMap = true
			break
		}
	}
	if !hasMap {
		return nil
	}
	var out map[string]struct{}
	var ranks map[string]int
	for _, k := range s.lookupKeys {
		if _, isAlias := s.aliases[k]; isAlias {
			continue
		}
		rk := -1
		topLevelDelims(k, func(j int) bool {
			av, ok := s.values[k[:j]]
			if !ok || av.Kind() != MapKind {
				return true
			}
			if ranks == nil {
				ranks = s.rankIndex()
			}
			if rk < 0 {
				rk = s.rankOf(ranks, k)
			}
			if s.rankOf(ranks, k[:j]) >= rk {
				return true
			}
			if _, hit := entryIn(av, k[j+1:]); hit {
				if out == nil {
					out = map[string]struct{}{}
				}
				out[k] = struct{}{}
				return false
			}
			return true
		})
	}
	return out
}
