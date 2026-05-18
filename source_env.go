package recon

import (
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/go-rotini/env"
)

// OSEnvSource is a [Source] backed by the process environment. It
// projects every [Path] lookup through a [KeyTransform] (default:
// [SnakeUpperTransform], with [WithEnvPrefix] producing
// [SnakeUpperPrefixTransform]) so a registry key of `server.port`
// reads the `SERVER_PORT` env var — the canonical 12-factor mapping.
//
// Values surface as [StringKind] — env vars are always strings on the
// wire; typed coercion happens at the [Registry] level when a caller
// asks for an int / duration / bool.
//
// A custom transform via [WithEnvTransform] overrides the default; an
// inverse parser via [WithEnvKeyParser] tells Keys() how to project
// env-var names back into [Path] space (the default for snake-upper
// uses [parseSnakeUpper], a lossy inversion that splits every
// underscore as a separator).
type OSEnvSource struct {
	prefix    string
	transform KeyTransform
	parse     func(name string) Path
	src       env.Source

	mu     sync.RWMutex
	keys   []Path
	cached bool
}

// NewOSEnvSource constructs an [OSEnvSource]. Defaults:
//
//   - transform: [SnakeUpperTransform] (or [SnakeUpperPrefixTransform]
//     when [WithEnvPrefix] is set).
//   - parser: snake-upper inverse — every underscore is a separator.
//
// Both are configurable per source via [WithEnvTransform] and
// [WithEnvKeyParser]; the defaults match the 12-factor expectation.
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

// Name reports the source's identifier. Fixed to "osenv".
func (s *OSEnvSource) Name() string { return "osenv" }

// Get projects path through the source's [KeyTransform] and looks
// the resulting env-var name up in the process environment. A path
// that projects to an unset env var returns (Value{}, false, nil);
// the source never fabricates env vars that aren't actually present.
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

// Keys enumerates the paths the source has cached. Each env var
// the source sees is run through the inverse parser to produce a
// [Path]; the prefix (when set) is stripped before parsing.
//
// First-call enumerates os.Environ; subsequent calls return the
// cached set. Invoke [OSEnvSource.Refresh] to pick up env-var
// additions or deletions.
//
// Round-trip caveat: the default snake-upper inverse treats every
// underscore as a path separator, so an env var named `APP_OAUTH2_TOKEN`
// surfaces as Path{"oauth2","token"} — there's no way to recover
// the original spelling of underscore-containing segments from the
// env-var name alone. Supply [WithEnvKeyParser] when your naming
// convention preserves that information differently.
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

// Refresh re-scans os.Environ to pick up env-var additions or
// deletions. Returns the new key count for diagnostic logging.
//
// The watch engine's [WithPoll] option drives Refresh on a timer
// when the registry's reload-engine wants live OS-env coverage.
func (s *OSEnvSource) Refresh() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = s.collectKeys()
	s.cached = true
	return len(s.keys)
}

// collectKeys walks os.Environ once, filters by prefix, parses each
// env-var name back into a [Path], and returns the sorted result.
// The caller MUST hold s.mu (or call from a context where the cache
// is being initialized).
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
