package recon

import (
	"maps"
	"sync"
)

// MapSource is a [Source] backed by a nested map[string]any matching
// what a config-file decoder produces. Per-key overrides belong in
// [Registry.Set]; [MapSource.Replace] swaps the whole map.
type MapSource struct {
	name string
	mu   sync.RWMutex
	data map[string]any

	// envKeys, when set, makes the top-level keys environment variable
	// names under this prefix (see [WithFileEnvKeys]).
	envKeys *string
}

// envKeyName is the top-level key path is stored under in env-keys
// mode.
func (s *MapSource) envKeyName(path Path) string {
	return SnakeUpperPrefixTransform(*s.envKeys)(path)
}

// nativeKey returns the key path is stored under.
func (s *MapSource) nativeKey(path Path) string {
	if s.envKeys == nil {
		return path.String()
	}
	return s.envKeyName(path)
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
	if s.envKeys != nil {
		path = Path{s.envKeyName(path)}
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
// map nodes are not reported. For a file read [WithFileEnvKeys], each
// top-level key spelled exactly as Get looks it up (the prefix, then
// upper case) is reported as its snake-upper path, as [OSEnvSource.Keys]
// does. The returned slice is sorted by canonical path string.
func (s *MapSource) Keys() []Path {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Path
	if s.envKeys != nil {
		for name := range s.data {
			if p := parseSnakeUpper(name, *s.envKeys); len(p) > 0 && s.envKeyName(p) == name {
				out = append(out, p)
			}
		}
	} else {
		walkLeaves(s.data, nil, &out)
	}
	sortPaths(out)
	return out
}

// Close is a no-op.
func (s *MapSource) Close() error { return nil }

// Replace atomically swaps in a deep copy of m. It does not rebuild the
// registry's snapshot; call [Registry.Reload] afterwards.
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

// newFileMapSource is [NewMapSource] for a file source built with cfg.
func newFileMapSource(name string, decoded map[string]any, cfg fileOptions) *MapSource {
	ms := NewMapSource(name, decoded)
	if cfg.envKeys {
		prefix := cfg.envPrefix
		ms.envKeys = &prefix
	}
	return ms
}
