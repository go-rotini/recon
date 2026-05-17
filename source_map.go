package recon

import (
	"maps"
	"slices"
	"sync"
)

// MapSource is a [Source] backed by a nested map[string]any. The map's shape
// matches what a config-file decoder produces: nested maps for sub-trees,
// leaf values as their native Go types (string, bool, int / int64, float64,
// time.Time, time.Duration, []any, map[string]any, nil, or any custom type
// callers want to register via a NewValue arm).
//
// MapSource is safe for concurrent reads. It does NOT support Set / writeback
// — it's a read-only source. For programmatic mutation use [Registry.Set].
type MapSource struct {
	name string
	mu   sync.RWMutex
	data map[string]any
}

// NewMapSource returns a [MapSource] named name, holding a deep-copy of m so
// later mutations of m by the caller don't affect the source's view. The
// name must be unique within the [Registry] it is registered with and must
// not collide with a reserved provenance label (see [Registry.AddSource]).
//
// A nil m is equivalent to an empty map — the source returns ok=false for
// every Get and an empty Keys().
func NewMapSource(name string, m map[string]any) *MapSource {
	return &MapSource{
		name: name,
		data: deepCopyMap(m),
	}
}

// Name reports the source's identifier — used by [Registry] for [Event]
// attribution, [Describe] provenance, and uniqueness checks.
func (s *MapSource) Name() string { return s.name }

// Get walks the nested map along path. Returns (value, true, nil) when the
// path resolves to a leaf or sub-tree; (Value{}, false, nil) when any
// intermediate is missing or a non-map. MapSource never returns a non-nil
// error.
//
// An empty Path is a no-op (false, nil) — there's no concept of "the root
// value" in the [Source] contract.
func (s *MapSource) Get(path Path) (Value, bool, error) {
	if len(path) == 0 {
		return Value{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cur := any(s.data)
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return Value{}, false, nil
		}
		cur, ok = m[seg]
		if !ok {
			return Value{}, false, nil
		}
	}
	return NewValue(cur), true, nil
}

// Keys enumerates every leaf path in the underlying map. Intermediate map
// nodes are NOT reported — only the deepest non-map values, since those are
// what [Source.Get] returns as scalars. The returned slice is sorted by
// canonical path string for deterministic output.
func (s *MapSource) Keys() []Path {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Path
	walkLeaves(s.data, nil, &out)
	slices.SortFunc(out, func(a, b Path) int {
		switch {
		case a.String() < b.String():
			return -1
		case a.String() > b.String():
			return 1
		default:
			return 0
		}
	})
	return out
}

// Close is a no-op for [MapSource] — it holds no external resources.
// Returning nil satisfies the idempotent-close contract on [Source].
func (s *MapSource) Close() error { return nil }

// Replace atomically swaps the underlying map. Useful in tests for
// driving a registry through a sequence of source states without
// constructing a new [MapSource]. The caller MUST NOT mutate m after this
// call (Replace takes ownership via deep copy).
func (s *MapSource) Replace(m map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = deepCopyMap(m)
}

// walkLeaves appends every (path, leaf) pair to *out by depth-first
// recursion. A "leaf" is any value that is not a map[string]any. Empty
// sub-maps emit no entries — they contain no leaf to report.
func walkLeaves(m map[string]any, prefix Path, out *[]Path) {
	for k, v := range m {
		p := prefix.Append(k)
		if sub, ok := v.(map[string]any); ok {
			walkLeaves(sub, p, out)
			continue
		}
		*out = append(*out, p)
	}
}

// deepCopyMap returns an independent deep copy of m. Nested maps and slices
// are copied recursively; leaf values (strings, numbers, time.Time, etc.)
// are immutable so they share storage with the source.
func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

// deepCopyValue is the per-element half of [deepCopyMap]. Recurses through
// map[string]any and []any; treats every other concrete type as immutable.
func deepCopyValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return deepCopyMap(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = deepCopyValue(e)
		}
		return out
	case []string:
		out := make([]string, len(x))
		copy(out, x)
		return out
	case map[string]string:
		out := make(map[string]string, len(x))
		maps.Copy(out, x)
		return out
	default:
		return v
	}
}
