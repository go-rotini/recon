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
		out = leafPaths(s.data)
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

// leafPaths returns the path of every leaf of m, in no particular order;
// nil when m has none. A leaf is any value that is not a
// map[string]any. The paths share one backing array, each capped at
// its own length, so they stay independent.
func leafPaths(m map[string]any) []Path {
	leaves, segs := countLeaves(m, 1)
	if leaves == 0 {
		return nil
	}
	w := leafWriter{arena: make([]string, 0, segs), out: make([]Path, 0, leaves)}
	w.walk(m, make([]string, 0, 8))
	return w.out
}

// countLeaves returns the number of leaves under m, and the segments
// their paths hold together, for m at the given depth.
func countLeaves(m map[string]any, depth int) (leaves, segs int) {
	for _, v := range m {
		if sub, ok := v.(map[string]any); ok {
			l, s := countLeaves(sub, depth+1)
			leaves, segs = leaves+l, segs+s
			continue
		}
		leaves++
		segs += depth
	}
	return leaves, segs
}

// leafWriter collects leaf paths into one backing array.
type leafWriter struct {
	arena []string
	out   []Path
}

// walk appends the path of each leaf under m, below prefix.
func (w *leafWriter) walk(m map[string]any, prefix []string) {
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			w.walk(sub, append(prefix, k))
			continue
		}
		start := len(w.arena)
		w.arena = append(append(w.arena, prefix...), k)
		w.out = append(w.out, Path(w.arena[start:len(w.arena):len(w.arena)]))
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

// newFileMapSource is [NewMapSource] for a file source built with cfg. A map that only the
// source refers to (owned) is held as it is rather than copied.
func newFileMapSource(name string, decoded map[string]any, owned bool, cfg fileOptions) *MapSource {
	ms := &MapSource{name: name, data: decoded}
	if !owned || decoded == nil {
		ms.data = deepCopyMap(decoded)
	}
	if cfg.envKeys {
		prefix := cfg.envPrefix
		ms.envKeys = &prefix
	}
	return ms
}

// replaceOwned is [MapSource.Replace] for a map that only the source refers to, which it holds
// without copying.
func (s *MapSource) replaceOwned(m map[string]any) {
	if m == nil {
		m = map[string]any{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = m
}
