package recon

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
)

// RemoteBackend is the contract an out-of-process configuration store
// satisfies; wrap one with [NewRemoteSource]. Recon ships no adapter
// for a real store, only [NewInMemoryBackend] for tests.
//
// Backends are string-keyed and string-valued, with "/" separating
// path segments in keys. Adapters with structured payloads should
// pre-serialize values and rely on the `format=` bind tag for
// per-field re-decoding.
type RemoteBackend interface {
	// List enumerates every key under prefix. An empty prefix lists
	// every key.
	List(ctx context.Context, prefix string) ([]string, error)

	// Get returns the value for key. An empty string with set=true is
	// "set to empty", matching [Source] semantics.
	Get(ctx context.Context, key string) (string, bool, error)

	// Close releases backend resources. Idempotent.
	Close() error
}

// BackendWatcher is the optional [RemoteBackend] capability for
// push-style notification. Pull-only backends omit it and the
// wrapping [RemoteSource] polls instead. The channel signals
// "something changed"; backends may coalesce. Watch must close the
// channel when ctx cancels.
type BackendWatcher interface {
	Watch(ctx context.Context) (<-chan struct{}, error)
}

// BackendLister is the optional [RemoteBackend] capability of reading
// every key under a prefix with its value in one call. A [RemoteSource]
// uses it in place of List plus one Get per key.
type BackendLister interface {
	ListValues(ctx context.Context, prefix string) (map[string]string, error)
}

// RemoteSource wraps a [RemoteBackend] as a [Source]. Construction
// reads every key under the configured prefix and caches the result;
// subsequent Get calls hit the cache.
//
// Live reload: when the registry watches it, the source re-reads the
// backend on each [BackendWatcher] notification, or otherwise every
// [WithRemotePollInterval] (off by default).
type RemoteSource struct {
	name    string
	backend RemoteBackend
	prefix  string
	poll    time.Duration
	trimKey bool

	mu      sync.RWMutex
	cache   map[string]string
	keyList []string
}

// NewRemoteSource constructs a [RemoteSource]. The construction-time
// read populates the cache; a backend failure here surfaces as a
// wrapped [*SourceError]. Returns wrapped [ErrInvalidPath] for an
// empty name or nil backend.
func NewRemoteSource(name string, backend RemoteBackend, opts ...RemoteOption) (*RemoteSource, error) {
	return NewRemoteSourceContext(context.Background(), name, backend, opts...)
}

// NewRemoteSourceContext is [NewRemoteSource] with ctx passed to the
// construction-time read, so a stalled backend can be given a deadline.
// A nil ctx returns [ErrNilContext].
func NewRemoteSourceContext(ctx context.Context, name string, backend RemoteBackend, opts ...RemoteOption) (*RemoteSource, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if name == "" {
		return nil, fmt.Errorf("%w: NewRemoteSource: empty name", ErrInvalidPath)
	}
	if backend == nil {
		return nil, fmt.Errorf("%w: NewRemoteSource: nil RemoteBackend", ErrInvalidPath)
	}
	cfg := remoteOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	// A trimmed prefix names a directory: "app" lists app/… (not apple/…)
	// and app/db/host becomes db.host.
	if cfg.trimPrefix && cfg.prefix != "" && !strings.HasSuffix(cfg.prefix, "/") {
		cfg.prefix += "/"
	}
	s := &RemoteSource{
		name:    name,
		backend: backend,
		prefix:  cfg.prefix,
		poll:    cfg.poll,
		trimKey: cfg.trimPrefix,
		cache:   map[string]string{},
	}
	if err := s.refresh(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Name returns the source identifier.
func (s *RemoteSource) Name() string { return s.name }

// Get looks up path against the cache. Multi-segment paths join with
// "/" — the backend's flat keyspace convention.
func (s *RemoteSource) Get(path Path) (Value, bool, error) {
	if len(path) == 0 {
		return Value{}, false, nil
	}
	key := s.lookupKey(path)
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.cache[key]
	if !ok {
		return Value{}, false, nil
	}
	return NewValue(v), true, nil
}

// Keys returns every cached key, sorted by canonical path string. The
// prefix is stripped when [WithRemoteTrimPrefix] is set.
func (s *RemoteSource) Keys() []Path {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Path, 0, len(s.keyList))
	for _, k := range s.keyList {
		out = append(out, s.keyToPath(k))
	}
	sortPaths(out)
	return out
}

// Close releases the backend and drops the cache.
func (s *RemoteSource) Close() error {
	s.mu.Lock()
	s.cache = nil
	s.keyList = nil
	s.mu.Unlock()
	return s.backend.Close()
}

// Refresh re-reads the backend, replacing the cache atomically. It
// does not rebuild the registry's snapshot; call [Registry.Reload]
// afterwards. On error the cache is unchanged.
func (s *RemoteSource) Refresh(ctx context.Context) error { return s.refresh(ctx) }

// Watch implements [Watcher]. It refreshes the cache and emits a
// [SourceChange] on every [BackendWatcher] notification, or on each
// poll tick that finds the data changed. A source with neither a
// [BackendWatcher] backend nor a poll interval returns a closed
// channel.
func (s *RemoteSource) Watch(ctx context.Context) (<-chan SourceChange, error) {
	if bw, ok := s.backend.(BackendWatcher); ok {
		return s.watchPush(ctx, bw)
	}
	if s.poll > 0 {
		return s.watchPoll(ctx), nil
	}
	closed := make(chan SourceChange)
	close(closed)
	return closed, nil
}

func (s *RemoteSource) watchPush(ctx context.Context, bw BackendWatcher) (<-chan SourceChange, error) {
	sub, err := bw.Watch(ctx)
	if err != nil {
		return nil, fmt.Errorf("recon: remote backend watch: %w", err)
	}
	out := make(chan SourceChange, 1)
	go s.fanWatch(ctx, sub, out)
	return out, nil
}

func (s *RemoteSource) watchPoll(ctx context.Context) <-chan SourceChange {
	out := make(chan SourceChange, 1)
	go func() {
		defer close(out)
		ticker := time.NewTicker(s.poll)
		defer ticker.Stop()
		s.mu.RLock()
		prev := cloneStringMap(s.cache)
		s.mu.RUnlock()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				change := SourceChange{}
				if err := s.refresh(ctx); err != nil {
					change.Err = err
				} else {
					s.mu.RLock()
					next := cloneStringMap(s.cache)
					s.mu.RUnlock()
					if cacheEqual(prev, next) {
						continue
					}
					prev = next
				}
				select {
				case out <- change:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func (s *RemoteSource) fanWatch(ctx context.Context, sub <-chan struct{}, out chan<- SourceChange) {
	defer close(out)
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-sub:
			if !ok {
				return
			}
			change := SourceChange{}
			if err := s.refresh(ctx); err != nil {
				change.Err = err
			}
			select {
			case out <- change:
			case <-ctx.Done():
				return
			}
		}
	}
}

// refresh re-reads every key under the source's prefix and swaps the
// cache. Returns a wrapped [*SourceError] on backend failure.
func (s *RemoteSource) refresh(ctx context.Context) error {
	cache, err := s.readAll(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cache = cache
	s.keyList = make([]string, 0, len(cache))
	for k := range cache {
		s.keyList = append(s.keyList, k)
	}
	slices.Sort(s.keyList)
	s.mu.Unlock()
	return nil
}

// readAll reads every key under the source's prefix with its value:
// in one call when the backend is a [BackendLister], else one List plus
// one Get per key.
func (s *RemoteSource) readAll(ctx context.Context) (map[string]string, error) {
	if bl, ok := s.backend.(BackendLister); ok {
		kv, err := bl.ListValues(ctx, s.prefix)
		if err != nil {
			return nil, &SourceError{Source: s.name, Op: "list", Cause: err}
		}
		return cloneStringMap(kv), nil
	}
	keys, err := s.backend.List(ctx, s.prefix)
	if err != nil {
		return nil, &SourceError{Source: s.name, Op: "list", Cause: err}
	}
	cache := make(map[string]string, len(keys))
	for _, k := range keys {
		val, ok, gerr := s.backend.Get(ctx, k)
		if gerr != nil {
			return nil, &SourceError{Source: s.name, Op: "get", Cause: gerr}
		}
		if !ok {
			continue
		}
		cache[k] = val
	}
	return cache, nil
}

// lookupKey projects path to the backend's flat key form, joining
// segments with "/" and re-attaching the prefix when trimKey is set.
func (s *RemoteSource) lookupKey(path Path) string {
	joined := strings.Join(path, "/")
	if s.trimKey && s.prefix != "" {
		return s.prefix + joined
	}
	return joined
}

// keyToPath splits a backend key into a [Path], stripping the prefix
// when trimKey is set.
func (s *RemoteSource) keyToPath(k string) Path {
	if s.trimKey && s.prefix != "" {
		k = strings.TrimPrefix(k, s.prefix)
	}
	if k == "" {
		return Path{}
	}
	return Path(strings.Split(k, "/"))
}

func cloneStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}

func cacheEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
