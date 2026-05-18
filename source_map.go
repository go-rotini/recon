package recon

import (
	"maps"
	"slices"
	"sync"
)

// MapSource is a [Source] backed by a nested map[string]any matching
// what a config-file decoder produces. Read-only: programmatic writes
// go through [Registry.Set].
type MapSource struct {
	name string
	mu   sync.RWMutex
	data map[string]any
}

// NewMapSource returns a [MapSource] named name holding a deep copy of
// m so later caller mutations do not affect the source. A nil m is
// treated as empty.
func NewMapSource(name string, m map[string]any) *MapSource {
	return &MapSource{
		name: name,
		data: deepCopyMap(m),
	}
}

// Name returns the source identifier.
func (s *MapSource) Name() string { return s.name }

// Get walks the nested map along path. Returns (value, true, nil) on
// hit; (Value{}, false, nil) when any intermediate is missing or a
// non-map. Never returns a non-nil error. An empty Path returns
// (false, nil).
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

// Keys enumerates every leaf path in the underlying map. Intermediate
// map nodes are not reported. The returned slice is sorted by
// canonical path string.
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

// Close is a no-op.
func (s *MapSource) Close() error { return nil }

// Replace atomically swaps the underlying map. Useful in tests. The
// caller must not mutate m after the call; Replace takes ownership
// via deep copy.
func (s *MapSource) Replace(m map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = deepCopyMap(m)
}

// walkLeaves appends every (path, leaf) pair to *out by depth-first
// recursion. A leaf is any value that is not a map[string]any.
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

// deepCopyMap returns an independent deep copy of m. Leaf values
// (strings, numbers, time.Time) are immutable and share storage.
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
