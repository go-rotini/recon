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

	// secretKeys is the set of canonical path strings the registry
	// has been told contain secret data — populated by
	// [Registry.MarkSecret] and by the [Registry.Bind] walker when
	// it encounters a `secret`-tagged field. Consulted by
	// [Registry.Describe] / [Registry.Save] for redaction.
	secretKeys map[string]struct{}

	// immutableBaseline maps a canonical path string to the resolved
	// value first observed there at the time it was marked
	// immutable. Populated by the [Registry.Bind] walker on every
	// `immutable`-tagged field. Consulted on every snapshot rebuild;
	// a candidate whose value differs from the baseline is rejected.
	immutableBaseline map[string]Value

	// pendingWarnings queues non-fatal advisories the registry has
	// produced since the last drain. Populated by the [Bind] walker
	// when it encounters a `deprecated`-tagged field whose source
	// supplied a value. Drained by the watch engine on its next
	// Event emit, and by callers via [Registry.DrainWarnings].
	pendingWarnings []DeprecationWarning

	closeOnce sync.Once
	closed    atomic.Bool
	logger    *slog.Logger
	opts      registryOptions
	watch     *watchEngine
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

	r.rebuildAndReport()

	// Start the watch engine while we still hold the lock so the source
	// snapshot the engine subscribes to matches the snapshot the
	// initial rebuild just installed.
	r.state.watch = newWatchEngine(r)
	r.state.watch.start()

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

		// Stop the watch engine BEFORE closing sources — the engine
		// holds source subscriptions and must release them through
		// ctx cancellation, not through Source.Close racing with the
		// in-flight subscription goroutine.
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
// retain the previous snapshot (the live config keeps working) and
// surface via Event.Err. The channel is closed when the registry is
// closed.
//
// The channel is buffered (capacity controlled by [WithEventBufferSize],
// default 16). Slow consumers cause events to drop; the next
// successfully-delivered Event carries a [DeprecationWarning] entry
// describing the loss.
//
// Returns nil on a closed-before-construction registry — callers
// reading from a nil channel block forever, the right shape for
// "stop consuming events when the registry is gone".
func (r *Registry) Events() <-chan Event {
	if r.state.watch == nil {
		return nil
	}
	return r.state.watch.events
}

// Reload re-reads every watched source and rebuilds the snapshot. Equivalent
// to ReloadContext(context.Background()).
func (r *Registry) Reload() error { return r.ReloadContext(context.Background()) }

// ReloadContext is the context-aware [Reload] — the context flows to
// remote backends during their refresh call. A canceled ctx aborts the
// rebuild and returns ctx.Err() wrapped.
//
// In-memory sources do not consult ctx; the parameter is reserved for
// sources that need request-scoped cancellation (remote backends and
// future I/O-bound sources).
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

// AddSource registers s at the lowest precedence (appended to the
// chain). Returns ErrSourceConflict when a source with the same
// Name() is already registered or when Name() is a reserved
// provenance label ("explicit", "default"). The snapshot is rebuilt
// before AddSource returns.
//
// Transactional: if the rebuild fails the immutable-baseline or
// validator check, the source is rolled out of the chain and the
// error is returned. The Source.Close hook is NOT invoked on
// rollback — the caller still owns the source.
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
		// Roll the source back out of the chain.
		r.state.sources = r.state.sources[:len(r.state.sources)-1]
		return err
	}
	return nil
}

// InsertSource registers s at the given precedence index (0 = highest).
// An index outside [0, len(sources)] is clamped to the nearest valid
// slot. Same conflict + transactional rebuild semantics as
// [Registry.AddSource].
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

// RemoveSource removes the source with the given name. Returns nil
// even when no source by that name is registered — Remove is
// idempotent (the rotini per-command lifecycle adds and removes
// sources around each dispatch; an idempotent Remove keeps that
// pattern simple).
//
// Transactional: if the rebuild fails after removal, the source is
// re-inserted at its original index AND its Close() — already called
// before the rebuild — is NOT undone. Callers writing schema-bound
// configs should treat RemoveSource failures as a "your schema
// rejects this removal" signal and not retry the same Remove.
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

// Validator returns the [SchemaValidator] this registry was
// constructed with, or nil when no validator was installed.
// Downstream tooling (help renderers, doc generators) uses this to
// introspect the active schema.
func (r *Registry) Validator() SchemaValidator {
	if r == nil {
		return nil
	}
	return r.state.opts.validator
}

// Validate runs the configured [SchemaValidator] (if any) against
// the current snapshot and returns its result. A registry with no
// validator returns nil — there's no schema to fail against. A
// closed registry returns [ErrRegistryClosed].
//
// Unlike the implicit validator pass that runs inside every
// snapshot rebuild, this method is on-demand: useful for "check
// current state" tooling that doesn't want to trigger a rebuild
// (e.g., a `myapp config validate` subcommand).
//
// Secret-marked keys' validation messages are redacted in the
// returned error so a `Validate()` call from a logging context
// never leaks the offending value.
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

// addSourceLocked is the locked-caller helper that backs AddSource and
// New's initial-source loop. The caller MUST hold r.state.mu.
//
// Watcher injection: if the source has no per-source [WatcherFactory] of
// its own and the registry has one set via [WithWatcher], it is attached
// here so [Source.Watch] subscribers go through the registry's chosen
// backend. The injection is best-effort — sources that don't expose a
// SetWatcher hook simply keep whatever factory they already had.
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
// [FileSource]; future sources opt in by adding the same one-method
// shape.
type watcherSetter interface {
	SetWatcher(w WatcherFactory)
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

// redactValidationErrorsForSecrets walks err and marks every
// [*ValidationError] whose Path is in secretKeys as Secret=true.
// Applied AFTER the schema validator returns so the validator
// itself doesn't need to know about recon's secret-key set — the
// SchemaValidator interface stays clean.
//
// Walks both single errors and *MultiError trees via errors.As; any
// non-ValidationError entries (Source errors, wrapped causes) are
// left untouched.
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

// rebuildAndReport is the log-but-still-return-error variant of
// [Registry.rebuildSnapshotLocked]. Used by [New] during initial
// construction where the caller intentionally accepts the registry
// even when the first snapshot's validator or immutable check fails.
// The caller MUST hold r.state.mu.
func (r *Registry) rebuildAndReport() {
	if err := r.rebuildSnapshotLocked(); err != nil {
		r.state.logger.Warn("recon: initial snapshot rejected", "err", err)
	}
}

// rebuildSnapshotLocked builds a candidate snapshot, validates it
// (immutable baselines + schema validator), and atomic-installs it
// only on success. The caller MUST hold r.state.mu.
//
// The candidate-then-install model means a failing rebuild leaves the
// previous snapshot in place — every concurrent [Get] / [Bind] /
// [Live.Get] keeps observing the last-known-good resolved view. This
// is the live-config correctness contract: a bad reload cannot
// silently swap in unvalidated state.
//
// Errors are aggregated into a [*MultiError] when both immutable
// violations and validator failures fire on the same rebuild; a
// single-error rebuild returns the typed error directly so
// [errors.Is] / [errors.As] work without unwrapping the MultiError.
//
// Write paths ([Set], [SetDefault], [Unset], [RegisterAlias], etc.)
// must roll back their map mutation when this method returns an
// error, otherwise the registry's explicit / default / alias state
// drifts out of sync with the installed snapshot.
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
		// Candidate rejected — retain the previous snapshot.
		if len(multi.Errors) == 1 {
			return multi.Errors[0]
		}
		return multi
	}

	r.state.snapshot.Store(candidate)
	return nil
}

// checkImmutableLocked compares the candidate snapshot's resolved
// values against the registry's immutable baselines. Returns one
// [*ImmutableChangedError] per path whose value differs.
//
// Caller MUST hold r.state.mu.
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

// queueWarning appends w to the registry's pending-warning queue.
// Used by the [Bind] walker (deprecated-tag emission) and any future
// non-fatal advisory path. The queue is drained by
// [Registry.DrainWarnings] / the watch engine.
func (r *Registry) queueWarning(w DeprecationWarning) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.pendingWarnings = append(r.state.pendingWarnings, w)
}

// DrainWarnings returns and clears the registry's pending warning
// queue. Callers wanting to consume deprecation notices outside the
// watch-event flow (e.g., after a one-shot [Bind] with no live
// reload) use this. The watch engine drains the same queue on every
// Event emit so reload-driven consumers see them on Event.Warnings.
//
// Returns a nil slice when no warnings are queued.
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

// markImmutable records baseline as the canonical value for the
// immutable key at path. A baseline is set exactly once per key —
// subsequent calls with the same path are no-ops so re-binding the
// same struct doesn't reset the baseline.
//
// Called by the [Bind] walker on every field tagged `immutable`.
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

// IsImmutable reports whether path has been baselined as immutable —
// either via a `immutable`-tagged field encountered by [Registry.Bind]
// or via a future explicit MarkImmutable API.
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
