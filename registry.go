package recon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
)

// registryState holds the fields shared between a [Registry] and every
// sub view derived from it. Sub() returns a fresh *Registry pointing at
// the same *registryState, so mutations are visible across views and a
// single mutex serializes writes.
type registryState struct {
	mu        sync.Mutex
	sources   []Source          // precedence-ordered: index 0 = highest
	explicits map[string]any    // Set / Unset
	defaults  map[string]any    // SetDefault
	aliases   map[string]string // alias → canonical
	pins      map[string]string // canonical → pinned source name

	snapshot atomic.Pointer[Snapshot]

	// secretKeys is the set of canonical paths marked secret. Populated
	// by [Registry.MarkSecret] and by the bind walker on `secret`-tagged
	// fields. Consulted by [Describe] / [Save] for redaction.
	secretKeys map[string]struct{}

	// immutableBaseline maps a canonical path to the value first seen
	// when it was marked immutable. Populated by the bind walker on
	// `immutable`-tagged fields. A rebuild whose candidate value differs
	// from the baseline is rejected.
	immutableBaseline map[string]Value

	// pendingWarnings queues non-fatal advisories. Populated by the bind
	// walker on `deprecated`-tagged fields whose source supplied a
	// value. Drained by [Registry.DrainWarnings] and by the watch engine
	// on every event.
	pendingWarnings []DeprecationWarning

	closeOnce sync.Once
	closed    atomic.Bool
	logger    *slog.Logger
	opts      registryOptions
	watch     *watchEngine
}

// Registry is the central configuration registry. Construct via [New].
// Reads (Get, Bind) are lock-free; writes (Set, AddSource, etc.) take
// the registry's mutex, rebuild the snapshot, and atomic-store.
//
// Close the Registry when no longer needed to release source resources
// and the Events channel. [Sub] returns a *Registry that shares state
// with its parent but resolves keys under a prefix; closing the parent
// invalidates every sub view.
type Registry struct {
	state  *registryState
	prefix Path // empty for a root registry
}

// New constructs a Registry from opts and runs an initial snapshot
// build. Sources in [WithSource] / [WithSources] are added in argument
// order, first being highest precedence.
//
// The returned Registry is functional even when the error is non-nil,
// enabling tests that assert "this registration would have failed"
// while still inspecting the registry. Production callers should treat
// any non-nil error as a hard stop.
func New(opts ...Option) (*Registry, error) {
	options := defaultRegistryOptions()
	for _, opt := range opts {
		opt(&options)
	}
	if options.optionErr != nil {
		return nil, options.optionErr
	}
	installDefaultCodecs(&options)
	installDefaultWatcher(&options)
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

	// Bulk-add under one lock so we rebuild exactly once at the end
	// rather than per source.
	r.state.mu.Lock()
	var addErr error
	for _, s := range options.initialSources {
		if err := r.addSourceLocked(s); err != nil {
			addErr = err
			break
		}
	}

	if addErr == nil && len(options.precedence) > 0 {
		r.applyPrecedenceLocked(options.precedence)
	}

	r.rebuildAndReport()

	// Start the engine while we still hold the lock so the source set it
	// subscribes to matches the snapshot we just installed.
	r.state.watch = newWatchEngine(r)
	r.state.watch.start()

	r.state.mu.Unlock()

	return r, addErr
}

// Close shuts down the registry. Idempotent. Every registered source's
// Close is called regardless of earlier failures; errors aggregate into
// a [*MultiError].
func (r *Registry) Close() error {
	var err error
	r.state.closeOnce.Do(func() {
		r.state.closed.Store(true)

		// Stop the watch engine first: it holds subscription channels
		// and must release them through ctx cancellation, not race with
		// Source.Close.
		if r.state.watch != nil {
			r.state.watch.stop()
		}

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

// Events returns the channel reload events are delivered on. Each
// reload — successful or failed — produces one [Event]; failures
// retain the previous snapshot and surface via [Event.Err]. The channel
// is buffered (capacity from [WithEventBufferSize], default 16). Slow
// consumers cause drops surfaced on the next deliverable Event's
// Warnings. Returns nil on a closed-before-construction registry.
func (r *Registry) Events() <-chan Event {
	if r.state.watch == nil {
		return nil
	}
	return r.state.watch.events
}

// Reload re-reads every watched source and rebuilds the snapshot.
func (r *Registry) Reload() error { return r.ReloadContext(context.Background()) }

// ReloadContext is the context-aware [Reload]. The context flows to
// remote backends during their refresh call; in-memory sources ignore
// it. A canceled ctx aborts and returns ctx.Err() wrapped.
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
	return r.rebuildSnapshotLocked()
}

// AddSource registers s at the lowest precedence. Returns
// [ErrSourceConflict] when a source with the same Name() already exists
// or Name() is a reserved label.
//
// Transactional: if the post-add rebuild fails, the source is rolled
// out of the chain. The source's Close is not invoked on rollback —
// the caller still owns it.
func (r *Registry) AddSource(s Source) error {
	if r.state.closed.Load() {
		return ErrRegistryClosed
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if err := r.addSourceLocked(s); err != nil {
		return err
	}
	if err := r.rebuildSnapshotLocked(); err != nil {
		r.state.sources = r.state.sources[:len(r.state.sources)-1]
		return err
	}
	return nil
}

// InsertSource registers s at precedence index at (0 = highest). An
// out-of-range index is clamped to [0, len(sources)]. Same conflict
// and transactional semantics as [AddSource].
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
	if r.state.opts.watcher != nil {
		if setter, ok := s.(watcherSetter); ok {
			setter.SetWatcher(r.state.opts.watcher)
		}
	}
	r.state.sources = slices.Insert(r.state.sources, at, s)
	if err := r.rebuildSnapshotLocked(); err != nil {
		r.state.sources = slices.Delete(r.state.sources, at, at+1)
		return err
	}
	return nil
}

// RemoveSource removes the source named name. Idempotent: removing an
// unknown name is not an error.
//
// Transactional: if the post-remove rebuild fails, the source is
// re-inserted at its original index. The source's Close is called only
// on a successful removal.
func (r *Registry) RemoveSource(name string) error {
	if r.state.closed.Load() {
		return ErrRegistryClosed
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	for i, s := range r.state.sources {
		if s.Name() != name {
			continue
		}
		removed := r.state.sources[i]
		r.state.sources = slices.Delete(r.state.sources, i, i+1)
		if err := r.rebuildSnapshotLocked(); err != nil {
			r.state.sources = slices.Insert(r.state.sources, i, removed)
			return err
		}
		_ = s.Close()
		return nil
	}
	return nil
}

// Sources returns the registered source names in precedence order
// (first = highest). The returned slice is a copy.
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
// stable resolved configuration to a goroutine or [SchemaValidator]
// without coordinating with reloads.
func (r *Registry) Snapshot() *Snapshot { return r.state.snapshot.Load() }

// Validator returns the [SchemaValidator] this registry was constructed
// with, or nil when none was installed.
func (r *Registry) Validator() SchemaValidator {
	if r == nil {
		return nil
	}
	return r.state.opts.validator
}

// Validate runs the configured [SchemaValidator] against the current
// snapshot. Returns nil when no validator is installed. Secret-marked
// keys in the returned error are redacted.
//
// Unlike the implicit validator pass inside every rebuild, this is
// on-demand and does not trigger a rebuild — suitable for a `config
// validate` subcommand.
func (r *Registry) Validate() error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	validator := r.state.opts.validator
	if validator == nil {
		return nil
	}
	snap := r.state.snapshot.Load()
	input := map[string]any{}
	if snap != nil {
		input = snap.AsMap()
	}
	if err := validator.Validate(input); err != nil {
		r.state.mu.Lock()
		secrets := cloneStringSet(r.state.secretKeys)
		r.state.mu.Unlock()
		redactValidationErrorsForSecrets(err, secrets)
		return fmt.Errorf("recon: schema validation: %w", err)
	}
	return nil
}

// addSourceLocked is the helper used by [AddSource] and by [New]'s
// bulk-add. Caller must hold r.state.mu.
//
// If the source exposes [watcherSetter] and the registry has a watcher
// factory configured, it is injected here so [Source.Watch] uses the
// registry's chosen backend.
func (r *Registry) addSourceLocked(s Source) error {
	if s == nil {
		return fmt.Errorf("%w: nil Source", ErrInvalidPath)
	}
	if err := r.checkSourceNameLocked(s.Name()); err != nil {
		return err
	}
	if r.state.opts.watcher != nil {
		if setter, ok := s.(watcherSetter); ok {
			setter.SetWatcher(r.state.opts.watcher)
		}
	}
	r.state.sources = append(r.state.sources, s)
	return nil
}

// watcherSetter is the optional capability sources implement to accept
// a registry-injected [WatcherFactory]. Currently satisfied by
// [FileSource].
type watcherSetter interface {
	SetWatcher(w WatcherFactory)
}

// checkSourceNameLocked enforces source-name uniqueness and rejects
// reserved labels. Caller must hold r.state.mu.
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

// applyPrecedenceLocked reorders r.state.sources to match order. Names
// not in order keep their original relative position and are appended
// after the named ones. Unknown names are silently ignored.
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

// redactValidationErrorsForSecrets walks err and flags any
// [*ValidationError] whose Path is in secretKeys as Secret. Applied
// after the validator returns so the [SchemaValidator] interface stays
// free of recon-specific concerns.
func redactValidationErrorsForSecrets(err error, secretKeys map[string]struct{}) {
	if err == nil || len(secretKeys) == 0 {
		return
	}
	var multi *MultiError
	if errors.As(err, &multi) {
		for _, sub := range multi.Errors {
			redactValidationErrorsForSecrets(sub, secretKeys)
		}
		return
	}
	var ve *ValidationError
	if errors.As(err, &ve) {
		if _, ok := secretKeys[ve.Path.String()]; ok {
			ve.Secret = true
		}
	}
}

// rebuildAndReport is the non-fatal variant of [rebuildSnapshotLocked]
// used by [New] when the first snapshot fails the immutable or
// validator check but the registry should still be returned. Caller
// must hold r.state.mu.
func (r *Registry) rebuildAndReport() {
	if err := r.rebuildSnapshotLocked(); err != nil {
		r.state.logger.Warn("recon: initial snapshot rejected", "err", err)
	}
}

// rebuildSnapshotLocked builds a candidate snapshot, runs the
// immutable + validator checks, and atomic-installs only on success.
// On failure the previous snapshot is retained so concurrent readers
// keep observing last-known-good state. Caller must hold r.state.mu.
//
// Write paths must roll back their map mutation when this returns an
// error or the explicit / default / alias state drifts out of sync
// with the installed snapshot.
func (r *Registry) rebuildSnapshotLocked() error {
	is := snapshotInputs{
		sources:    r.state.sources,
		explicits:  cloneStringAnyMap(r.state.explicits),
		defaults:   cloneStringAnyMap(r.state.defaults),
		aliases:    cloneStringStringMap(r.state.aliases),
		pins:       cloneStringStringMap(r.state.pins),
		secretKeys: cloneStringSet(r.state.secretKeys),
		redactor:   r.state.opts.secretRedactor,
		merge:      r.state.opts.merge,
	}
	candidate := buildSnapshot(is)

	multi := &MultiError{}
	for _, err := range r.checkImmutableLocked(candidate) {
		multi.Append(err)
	}
	if r.state.opts.validator != nil {
		if err := r.state.opts.validator.Validate(candidate.AsMap()); err != nil {
			redactValidationErrorsForSecrets(err, candidate.secretKeys)
			multi.Append(fmt.Errorf("recon: schema validation: %w", err))
		}
	}
	if len(multi.Errors) > 0 {
		if len(multi.Errors) == 1 {
			return multi.Errors[0]
		}
		return multi
	}

	r.state.snapshot.Store(candidate)
	return nil
}

// checkImmutableLocked returns one [*ImmutableChangedError] per
// baselined path whose candidate value differs. Caller must hold
// r.state.mu.
func (r *Registry) checkImmutableLocked(snap *Snapshot) []error {
	if len(r.state.immutableBaseline) == 0 {
		return nil
	}
	var errs []error
	for path, baseline := range r.state.immutableBaseline {
		cur, ok := snap.values[path]
		if !ok {
			continue
		}
		if valuesEqual(baseline, cur) {
			continue
		}
		old := baseline.String()
		next := cur.String()
		if _, secret := r.state.secretKeys[path]; secret {
			redactor := r.state.opts.secretRedactor
			if redactor != nil {
				old = redactor(old)
				next = redactor(next)
			}
		}
		errs = append(errs, &ImmutableChangedError{
			Path: ParsePath(path),
			Old:  old,
			New:  next,
		})
	}
	return errs
}

// queueWarning appends w to the registry's pending warning queue.
func (r *Registry) queueWarning(w DeprecationWarning) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.pendingWarnings = append(r.state.pendingWarnings, w)
}

// DrainWarnings returns and clears the pending warning queue. The
// watch engine drains the same queue on every event emit; callers that
// run [Bind] without live reload use this to surface deprecations
// directly. Returns nil when empty.
func (r *Registry) DrainWarnings() []DeprecationWarning {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if len(r.state.pendingWarnings) == 0 {
		return nil
	}
	out := r.state.pendingWarnings
	r.state.pendingWarnings = nil
	return out
}

// markImmutable records baseline as the canonical value for path. The
// baseline is set exactly once per path; subsequent calls are no-ops so
// re-binding the same struct does not reset it. Called by the bind
// walker on every `immutable`-tagged field.
func (r *Registry) markImmutable(path Path, baseline Value) {
	if len(path) == 0 {
		return
	}
	pathStr := path.String()
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if r.state.immutableBaseline == nil {
		r.state.immutableBaseline = map[string]Value{}
	}
	if _, exists := r.state.immutableBaseline[pathStr]; exists {
		return
	}
	r.state.immutableBaseline[pathStr] = baseline
}

// IsImmutable reports whether path has an immutable baseline recorded.
func (r *Registry) IsImmutable(key string) bool {
	if r.state.closed.Load() {
		return false
	}
	fullKey := r.fullKey(key)
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	_, ok := r.state.immutableBaseline[fullKey]
	return ok
}

// cloneStringAnyMap returns a shallow copy of m. The map header is
// independent so locked writes do not race the unlocked snapshot
// build.
func cloneStringAnyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	maps.Copy(out, m)
	return out
}

func cloneStringStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}

// validateNotClosed returns [ErrRegistryClosed] when r has been Closed.
func (r *Registry) validateNotClosed() error {
	if r.state.closed.Load() {
		return ErrRegistryClosed
	}
	return nil
}
