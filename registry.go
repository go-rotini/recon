package recon

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
)

// registryState holds every field a [Registry] needs to share across sub-
// tree views. Sub() returns a fresh *Registry pointing at the SAME
// *registryState — so mutations through a sub are visible to the parent
// and vice versa, and a single mutex serializes writes across both views.
//
// The split is necessary because Go's sync.Mutex / sync.Once / atomic
// types cannot be copied: parent and sub need to share them through a
// pointer indirection, not by struct-field copy.
type registryState struct {
	mu        sync.Mutex
	sources   []Source          // precedence-ordered: index 0 = highest
	explicits map[string]any    // Set / Unset
	defaults  map[string]any    // SetDefault
	aliases   map[string]string // alias path string → canonical path string
	pins      map[string]string // canonical path string → pinned source name

	snapshot atomic.Pointer[Snapshot]

	closeOnce sync.Once
	closed    atomic.Bool
	logger    *slog.Logger
	opts      registryOptions
}

// Registry is the central data-in registry. Construct via [New]; safe for
// concurrent use after construction. Reads go through an atomic-pointer load
// + map lookup (lock-free); writes (Set, AddSource, RegisterAlias, …) take
// the registry's mutex, rebuild the snapshot, and atomic-store.
//
// A Registry must be closed via [Registry.Close] when no longer needed; this
// signals registered sources to release their resources and closes the
// Events channel that consumers of live reload subscribe to.
//
// Sub() returns a *Registry that shares state with its parent but resolves
// keys relative to a sub-tree prefix. The two values are independently
// usable; closing the parent invalidates every sub view derived from it.
type Registry struct {
	state  *registryState
	prefix Path // empty for a root registry
}

// New constructs a Registry from the supplied options and runs an initial
// snapshot build. Sources passed via WithSource are added in argument order
// (first = highest precedence). The error reports the first failure
// encountered while applying options (alias cycle, duplicate source name,
// codec misuse, …) or building the initial snapshot.
//
// The returned Registry is fully functional even when the error is non-nil
// — useful for tests that want to assert "this source-registration would
// have failed; the registry should still be intact." Callers that want
// fail-fast behavior should treat any non-nil error as a hard stop.
func New(opts ...Option) (*Registry, error) {
	options := defaultRegistryOptions()
	for _, opt := range opts {
		opt(&options)
	}
	installDefaultCodecs(&options)
	r := &Registry{
		state: &registryState{
			opts:      options,
			explicits: map[string]any{},
			defaults:  map[string]any{},
			aliases:   map[string]string{},
			pins:      map[string]string{},
			logger:    options.logger,
		},
	}
	if r.state.logger == nil {
		r.state.logger = slog.Default()
	}

	// Install the initial sources. AddSource takes the registry's lock and
	// rebuilds the snapshot per call; for the construction-time bulk add we
	// take the lock once and defer the single snapshot rebuild to the end.
	r.state.mu.Lock()
	var addErr error
	for _, s := range options.initialSources {
		if err := r.addSourceLocked(s); err != nil {
			addErr = err
			break
		}
	}

	// Apply the requested precedence order, if any.
	if addErr == nil && len(options.precedence) > 0 {
		r.applyPrecedenceLocked(options.precedence)
	}

	r.rebuildSnapshotLocked()
	r.state.mu.Unlock()

	return r, addErr
}

// Close shuts down the registry. Idempotent: every call after the first is
// a no-op. Close walks every registered [Source] and calls its Close method;
// errors are aggregated into a *MultiError and returned, but every source
// gets its Close call regardless of earlier failures.
func (r *Registry) Close() error {
	var err error
	r.state.closeOnce.Do(func() {
		r.state.closed.Store(true)
		r.state.mu.Lock()
		defer r.state.mu.Unlock()
		multi := &MultiError{}
		for _, s := range r.state.sources {
			if cerr := s.Close(); cerr != nil {
				multi.Append(&SourceError{Source: s.Name(), Op: "close", Cause: cerr})
			}
		}
		if len(multi.Errors) > 0 {
			err = multi
		}
	})
	return err
}

// Reload re-reads every watched source and rebuilds the snapshot. Equivalent
// to ReloadContext(context.Background()).
func (r *Registry) Reload() error { return r.ReloadContext(context.Background()) }

// ReloadContext is the context-aware Reload — passed through to remote
// backends during refresh (and, in Phase 8+, to the watch engine's reload
// path). A canceled ctx aborts the rebuild and returns ctx.Err().
//
// Phase 3 sources (MapSource, BufferSource) do not consult ctx; the
// parameter exists so the API is stable across future source additions.
func (r *Registry) ReloadContext(ctx context.Context) error {
	if r.state.closed.Load() {
		return ErrRegistryClosed
	}
	if ctx == nil {
		return ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("recon: reload context: %w", err)
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.rebuildSnapshotLocked()
	return nil
}

// AddSource registers s at the lowest precedence (appended to the chain).
// Returns ErrSourceConflict when a source with the same Name() is already
// registered or when Name() is a reserved provenance label ("explicit",
// "default"). The snapshot is rebuilt before AddSource returns.
func (r *Registry) AddSource(s Source) error {
	if r.state.closed.Load() {
		return ErrRegistryClosed
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if err := r.addSourceLocked(s); err != nil {
		return err
	}
	r.rebuildSnapshotLocked()
	return nil
}

// InsertSource registers s at the given precedence index (0 = highest).
// An index outside [0, len(sources)] is clamped to the nearest valid slot.
// Same conflict semantics as [Registry.AddSource].
func (r *Registry) InsertSource(at int, s Source) error {
	if r.state.closed.Load() {
		return ErrRegistryClosed
	}
	if s == nil {
		return fmt.Errorf("%w: nil Source", ErrInvalidPath)
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if err := r.checkSourceNameLocked(s.Name()); err != nil {
		return err
	}
	switch {
	case at < 0:
		at = 0
	case at > len(r.state.sources):
		at = len(r.state.sources)
	}
	r.state.sources = slices.Insert(r.state.sources, at, s)
	r.rebuildSnapshotLocked()
	return nil
}

// RemoveSource removes the source with the given name. Returns nil even
// when no source by that name is registered — Remove is idempotent (the
// rotini per-command lifecycle adds and removes sources around each
// dispatch; an idempotent Remove keeps that pattern simple).
func (r *Registry) RemoveSource(name string) error {
	if r.state.closed.Load() {
		return ErrRegistryClosed
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	for i, s := range r.state.sources {
		if s.Name() == name {
			_ = s.Close()
			r.state.sources = slices.Delete(r.state.sources, i, i+1)
			r.rebuildSnapshotLocked()
			return nil
		}
	}
	return nil
}

// Sources returns the registered source names in precedence order (first =
// highest). The returned slice is a copy; mutating it does not affect the
// registry.
func (r *Registry) Sources() []string {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	out := make([]string, len(r.state.sources))
	for i, s := range r.state.sources {
		out[i] = s.Name()
	}
	return out
}

// Snapshot returns the current immutable view. Useful for handing a
// stable resolved configuration to a downstream component (a goroutine, a
// SchemaValidator) without that component having to coordinate with reloads.
func (r *Registry) Snapshot() *Snapshot { return r.state.snapshot.Load() }

// addSourceLocked is the locked-caller helper that backs AddSource and
// New's initial-source loop. The caller MUST hold r.state.mu.
func (r *Registry) addSourceLocked(s Source) error {
	if s == nil {
		return fmt.Errorf("%w: nil Source", ErrInvalidPath)
	}
	if err := r.checkSourceNameLocked(s.Name()); err != nil {
		return err
	}
	r.state.sources = append(r.state.sources, s)
	return nil
}

// checkSourceNameLocked enforces the uniqueness + reserved-name rules a
// freshly-registered Source must satisfy.
func (r *Registry) checkSourceNameLocked(name string) error {
	if name == "" {
		return fmt.Errorf("%w: source name is empty", ErrInvalidPath)
	}
	if _, reserved := reservedSourceNames[name]; reserved {
		return fmt.Errorf("%w: %q is a reserved provenance label", ErrSourceConflict, name)
	}
	for _, existing := range r.state.sources {
		if existing.Name() == name {
			return fmt.Errorf("%w: %q", ErrSourceConflict, name)
		}
	}
	return nil
}

// applyPrecedenceLocked re-orders r.state.sources to match the supplied list of
// source names. Names not in the list keep their original relative order,
// appended after the named ones. Unknown names are silently ignored — they
// may refer to sources that haven't been added yet (the registry tolerates
// optimistic ordering).
func (r *Registry) applyPrecedenceLocked(order []string) {
	if len(r.state.sources) == 0 {
		return
	}
	indexed := make(map[string]Source, len(r.state.sources))
	for _, s := range r.state.sources {
		indexed[s.Name()] = s
	}
	out := make([]Source, 0, len(r.state.sources))
	consumed := map[string]struct{}{}
	for _, name := range order {
		if s, ok := indexed[name]; ok {
			out = append(out, s)
			consumed[name] = struct{}{}
		}
	}
	for _, s := range r.state.sources {
		if _, done := consumed[s.Name()]; !done {
			out = append(out, s)
		}
	}
	r.state.sources = out
}

// rebuildSnapshotLocked recomputes the snapshot from the registry's current
// state and atomic-stores it. The caller MUST hold r.state.mu.
//
// In Phase 3 the rebuild is infallible — codec / remote-source failures
// that will need to surface in later phases are intentionally absent here,
// so this returns no error. When sources start failing (Phase 4+), the
// previous snapshot will be retained (atomic-store skipped) to honor the
// "failed reload, previous value held" contract from §2.5.3.
func (r *Registry) rebuildSnapshotLocked() {
	is := snapshotInputs{
		sources:    r.state.sources,
		explicits:  cloneStringAnyMap(r.state.explicits),
		defaults:   cloneStringAnyMap(r.state.defaults),
		aliases:    cloneStringStringMap(r.state.aliases),
		pins:       cloneStringStringMap(r.state.pins),
		requireAll: r.state.opts.requireAll,
	}
	r.state.snapshot.Store(buildSnapshot(is))
}

// cloneStringAnyMap returns a shallow copy of m. The values are reference-
// shared (the registry never mutates them in-place), but the map header is
// independent so the locked-write methods don't race the unlocked-read
// snapshot build.
func cloneStringAnyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	maps.Copy(out, m)
	return out
}

// cloneStringStringMap is the string→string twin of [cloneStringAnyMap].
func cloneStringStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}

// validateNotClosed returns ErrRegistryClosed when r has been Close()d.
// Shared by registry_get.go / registry_set.go for a uniform closed-check
// guard.
func (r *Registry) validateNotClosed() error {
	if r.state.closed.Load() {
		return ErrRegistryClosed
	}
	return nil
}
