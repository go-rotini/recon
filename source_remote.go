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
// satisfies. Real adapters (etcd, consul, vault, AWS SSM, k8s, …) live
// in separate go-rotini modules and depend on `recon` core for this
// interface. The core ships only the interface plus
// [NewInMemoryBackend] for tests.
//
// Backends are string-keyed and string-valued: KV stores are the
// universal lowest-common-denominator. Adapters that hold structured
// payloads (etcd's JSON blobs, Vault's secret maps) decode the value
// into a flat key+JSON string before returning it; the registry's
// `format=` tag option ([Bind] / Phase 6) handles per-field re-decoding.
type RemoteBackend interface {
	// List enumerates every key under prefix. An empty prefix lists
	// every key the backend exposes. The returned slice may be in
	// any order; [RemoteSource] sorts it when it builds the Keys()
	// view.
	List(ctx context.Context, prefix string) ([]string, error)

	// Get returns the value for key. The bool reports whether the
	// key is set (an empty string with set=true is "set to empty",
	// matching Source semantics).
	Get(ctx context.Context, key string) (string, bool, error)

	// Close releases backend resources (HTTP clients, connection
	// pools, polling goroutines). Idempotent.
	Close() error
}

// BackendWatcher is the optional [RemoteBackend] capability for
// push-style notification. Adapters whose backing store sends change
// events (etcd watch, consul long-poll) implement this; pull-only
// stores (AWS SSM, environment-style backends) leave it
// unimplemented and the wrapping [RemoteSource] polls instead.
//
// The channel signals "something changed; re-read whatever you care
// about." Backends MAY coalesce multiple changes into one signal.
// Watch MUST close the channel when ctx cancels.
type BackendWatcher interface {
	Watch(ctx context.Context) (<-chan struct{}, error)
}

// RemoteSource is the [Source] wrapping a [RemoteBackend]. On
// construction it lists + reads every key under the configured
// prefix and caches the result in memory; subsequent [Source.Get]
// calls hit the cache (no per-call backend round-trip).
//
// Live reload: if the backend implements [BackendWatcher], the source
// subscribes to it and re-reads on every signal. Otherwise it polls
// at the interval set by [WithRemotePollInterval] (default off — set
// the interval explicitly to opt in).
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
// read populates the cache; any backend error during this initial
// read surfaces as a wrapped [SourceError] so the caller can fail
// fast.
//
// Returns a wrapped [ErrInvalidPath] when name is empty or backend
// is nil.
func NewRemoteSource(name string, backend RemoteBackend, opts ...RemoteOption) (*RemoteSource, error) {
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
	s := &RemoteSource{
		name:    name,
		backend: backend,
		prefix:  cfg.prefix,
		poll:    cfg.poll,
		trimKey: cfg.trimPrefix,
		cache:   map[string]string{},
	}
	if err := s.refresh(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

// Name reports the source's identifier.
func (s *RemoteSource) Name() string { return s.name }

// Get looks up path against the cache the source loaded from the
// backend. Multi-segment paths are joined with "/" — the backend's
// flat keyspace convention — before lookup. Backends that prefer "."
// as their delimiter should wrap the source in an alias.
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

// Keys returns every key the source has cached. The result is
// sorted by canonical path string. Aliased / transformed keys are
// NOT projected here — the path matches the underlying backend's
// key form (with the prefix stripped when [WithRemoteTrimPrefix] is set).
func (s *RemoteSource) Keys() []Path {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Path, 0, len(s.keyList))
	for _, k := range s.keyList {
		out = append(out, s.keyToPath(k))
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

// Close releases the backend's resources. The cache is dropped; a
// closed RemoteSource returns (nil, false, nil) from every Get.
func (s *RemoteSource) Close() error {
	s.mu.Lock()
	s.cache = nil
	s.keyList = nil
	s.mu.Unlock()
	return s.backend.Close()
}

// Refresh re-reads the backend, replacing the cache atomically. The
// watch loop and external callers both use this for the actual
// re-read on each notification.
func (s *RemoteSource) Refresh(ctx context.Context) error { return s.refresh(ctx) }

// Watch implements the [Watcher] capability. The returned channel
// emits a [SourceChange] whenever the backend reports activity —
// via [BackendWatcher.Watch] when available, otherwise via the poll
// interval configured by [WithRemotePollInterval].
//
// A source with neither a BackendWatcher backend nor a configured
// poll interval returns a closed channel: there's no signal source,
// so no events can ever fire.
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

// watchPush subscribes to the backend's native notification channel
// and refreshes the cache on each signal. Each refresh produces one
// downstream [SourceChange]; refresh errors ride along on Err.
func (s *RemoteSource) watchPush(ctx context.Context, bw BackendWatcher) (<-chan SourceChange, error) {
	sub, err := bw.Watch(ctx)
	if err != nil {
		return nil, fmt.Errorf("recon: remote backend watch: %w", err)
	}
	out := make(chan SourceChange, 1)
	go s.fanWatch(ctx, sub, out)
	return out, nil
}

// watchPoll runs a polling loop at s.poll. Used when the backend
// has no native watch capability.
func (s *RemoteSource) watchPoll(ctx context.Context) <-chan SourceChange {
	out := make(chan SourceChange, 1)
	go func() {
		defer close(out)
		ticker := time.NewTicker(s.poll)
		defer ticker.Stop()
		var prev map[string]string
		s.mu.RLock()
		prev = cloneStringMap(s.cache)
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

// fanWatch forwards every BackendWatcher signal through a refresh +
// downstream emit. Runs as the goroutine for [watchPush].
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

// refresh re-reads every key under the source's prefix and atomic-
// swaps the cache. Returns a wrapped [SourceError] on backend
// failure so callers can distinguish "remote unreachable" from a
// malformed-payload outcome.
func (s *RemoteSource) refresh(ctx context.Context) error {
	keys, err := s.backend.List(ctx, s.prefix)
	if err != nil {
		return &SourceError{Source: s.name, Op: "list", Cause: err}
	}
	cache := make(map[string]string, len(keys))
	for _, k := range keys {
		val, ok, gerr := s.backend.Get(ctx, k)
		if gerr != nil {
			return &SourceError{Source: s.name, Op: "get", Cause: gerr}
		}
		if !ok {
			continue
		}
		cache[k] = val
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

// lookupKey is the path→backend-key projection. The default join
// is "/" (the convention every supported backend uses); the prefix,
// if any, is re-attached so cache lookups match the keys the
// refresh stored.
func (s *RemoteSource) lookupKey(path Path) string {
	joined := strings.Join(path, "/")
	if s.trimKey && s.prefix != "" {
		return s.prefix + joined
	}
	return joined
}

// keyToPath is the inverse of [lookupKey] — splits a backend key
// into a [Path]. Used by [Keys].
func (s *RemoteSource) keyToPath(k string) Path {
	if s.trimKey && s.prefix != "" {
		k = strings.TrimPrefix(k, s.prefix)
	}
	if k == "" {
		return Path{}
	}
	return Path(strings.Split(k, "/"))
}

// cloneStringMap returns a shallow copy of m. Used by the polling
// loop to keep the previous-cache reference stable while the next
// refresh executes.
func cloneStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}

// cacheEqual reports whether two cache snapshots represent the same
// key/value state. Used by the polling loop to decide whether to
// emit a downstream SourceChange.
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
