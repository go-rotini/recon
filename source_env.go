package recon

import (
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/go-rotini/env"
)

// OSEnvSource is a [Source] backed by the process environment. Lookups
// go through [env.OSEnv] (the sibling env package's OS source), so any
// behavior that package documents (case-folding on Windows, key-shape
// rejection, etc.) applies here transparently.
//
// Values are surfaced as [StringKind] — env vars are always strings on
// the wire; typed coercion happens at the [Registry] level when a caller
// asks for an int / duration / bool.
//
// A prefix filter ([WithEnvPrefix]) limits the source to env vars whose
// name starts with the supplied string; the prefix is NOT stripped from
// keys, so a caller wanting `APP_PORT` to surface as `port` should pair
// this with [Registry.RegisterAlias] or a sub-view rewrite.
type OSEnvSource struct {
	prefix string
	src    env.Source

	mu     sync.RWMutex
	keys   []Path // cached on first Keys() call; refreshed by Refresh
	cached bool
}

// NewOSEnvSource constructs an [OSEnvSource]. Options are limited to
// [WithEnvPrefix] in Phase 4.
func NewOSEnvSource(opts ...EnvOption) *OSEnvSource {
	cfg := envOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &OSEnvSource{
		prefix: cfg.prefix,
		src:    env.OSEnv(),
	}
}

// Name reports the source's identifier. Fixed to "osenv" for parity with
// other sources' single-token names.
func (s *OSEnvSource) Name() string { return "osenv" }

// Get looks up path[0] (env vars are flat — multi-segment paths are not
// part of the env-var addressing model) in the process environment. A
// prefix-mismatched key returns (Value{}, false, nil); the source never
// fabricates env vars that aren't actually present.
func (s *OSEnvSource) Get(path Path) (Value, bool, error) {
	if len(path) != 1 {
		return Value{}, false, nil
	}
	key := path[0]
	if s.prefix != "" && !strings.HasPrefix(key, s.prefix) {
		return Value{}, false, nil
	}
	val, ok := s.src.Lookup(key)
	if !ok {
		return Value{}, false, nil
	}
	return NewValue(val), true, nil
}

// Keys enumerates every env var visible to the source. The result is
// cached after the first call; invoke [OSEnvSource.Refresh] to pick up
// env-var additions or deletions (rare during a running process, but
// supported for the `setenv` test pattern).
func (s *OSEnvSource) Keys() []Path {
	s.mu.RLock()
	if s.cached {
		out := slices.Clone(s.keys)
		s.mu.RUnlock()
		return out
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached {
		return slices.Clone(s.keys)
	}
	s.keys = s.collectKeys()
	s.cached = true
	return slices.Clone(s.keys)
}

// Close is a no-op — [OSEnvSource] holds no resources.
func (s *OSEnvSource) Close() error { return nil }

// Refresh re-scans os.Environ to pick up env vars that were created or
// removed after construction. Returns the new key count for the
// caller's diagnostic logging.
func (s *OSEnvSource) Refresh() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = s.collectKeys()
	s.cached = true
	return len(s.keys)
}

// collectKeys walks os.Environ once, applying the prefix filter. The
// caller must hold s.mu.
func (s *OSEnvSource) collectKeys() []Path {
	envvars := os.Environ()
	out := make([]Path, 0, len(envvars))
	for _, kv := range envvars {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		key := kv[:eq]
		if s.prefix != "" && !strings.HasPrefix(key, s.prefix) {
			continue
		}
		out = append(out, MakePath(key))
	}
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
