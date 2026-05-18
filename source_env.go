package recon

import (
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/go-rotini/env"
)

// OSEnvSource is a [Source] backed by the process environment. Path
// lookups go through a [KeyTransform] (default: [SnakeUpperTransform],
// or [SnakeUpperPrefixTransform] when [WithEnvPrefix] is set) so
// "server.port" reads SERVER_PORT.
//
// Values surface as [StringKind]; typed coercion happens at the
// [Registry] level. Use [WithEnvTransform] for a non-default forward
// projection and [WithEnvKeyParser] for the inverse used by [Keys].
type OSEnvSource struct {
	prefix    string
	transform KeyTransform
	parse     func(name string) Path
	src       env.Source

	mu     sync.RWMutex
	keys   []Path
	cached bool
}

// NewOSEnvSource constructs an [OSEnvSource] with snake-upper defaults.
func NewOSEnvSource(opts ...EnvOption) *OSEnvSource {
	cfg := envOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	transform := cfg.transform
	if transform == nil {
		transform = SnakeUpperPrefixTransform(cfg.prefix)
	}
	parser := cfg.parser
	if parser == nil {
		parser = func(name string) Path { return parseSnakeUpper(name, cfg.prefix) }
	}
	return &OSEnvSource{
		prefix:    cfg.prefix,
		transform: transform,
		parse:     parser,
		src:       env.OSEnv(),
	}
}

// Name is fixed to "osenv".
func (s *OSEnvSource) Name() string { return "osenv" }

// Get projects path through the [KeyTransform] and looks the result
// up in the environment. An unset env var returns (Value{}, false, nil).
func (s *OSEnvSource) Get(path Path) (Value, bool, error) {
	if len(path) == 0 {
		return Value{}, false, nil
	}
	name := s.transform(path)
	if name == "" {
		return Value{}, false, nil
	}
	val, ok := s.src.Lookup(name)
	if !ok {
		return Value{}, false, nil
	}
	return NewValue(val), true, nil
}

// Keys enumerates paths cached from os.Environ. The first call scans
// the environment; later calls return the cached set until [Refresh].
//
// The default snake-upper inverse treats every underscore as a
// separator, so APP_OAUTH2_TOKEN surfaces as Path{"oauth2","token"}.
// Supply [WithEnvKeyParser] when a different convention preserves
// segment boundaries.
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

// Close is a no-op.
func (s *OSEnvSource) Close() error { return nil }

// Refresh re-scans os.Environ to pick up additions or deletions and
// returns the new key count. Driven by the watch engine's [WithPoll]
// when live env coverage is wanted.
func (s *OSEnvSource) Refresh() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = s.collectKeys()
	s.cached = true
	return len(s.keys)
}

// collectKeys walks os.Environ, filters by prefix, parses each name
// to a Path, and returns the sorted result. Caller must hold s.mu.
func (s *OSEnvSource) collectKeys() []Path {
	envvars := os.Environ()
	out := make([]Path, 0, len(envvars))
	for _, kv := range envvars {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		name := kv[:eq]
		if s.prefix != "" && !strings.HasPrefix(name, s.prefix) {
			continue
		}
		p := s.parse(name)
		if len(p) == 0 {
			continue
		}
		out = append(out, p)
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
