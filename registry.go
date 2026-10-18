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

	// bound holds the paths a bind looked up that no source's Keys
	// reported, so every rebuild resolves them too (an env var such as
	// APP_MAX_CONNS reaches a max_conns field this way). Replaced, never
	// mutated, so a snapshot build can read it without a copy.
	bound map[string]struct{}

	// consulted holds every path a bind has read. Save keeps an
	// un-prefixed environment source's keys only when a bind read them.
	consulted map[string]struct{}

	closeOnce sync.Once
	closed    atomic.Bool
	logger    *slog.Logger
	opts      registryOptions
	watch     watchRunner
}

// Registry is the central configuration registry. Construct via [New].
// Reads (Get, Bind) are lock-free; writes (Set, AddSource, etc.) take
// the registry's mutex, rebuild the snapshot, and atomic-store.
//
// Close the Registry when no longer needed to release source resources
// and the Events channel. [Registry.Sub] returns a *Registry that shares state
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
// An invalid option (such as a [WithSchema] schema that fails to
// compile) returns a nil Registry. A source that cannot be added is
// returned as the error alongside a usable Registry holding the
// sources before it; treat any non-nil error as fatal. If the first
// snapshot holds values and fails schema validation, New returns the
// validation error and a nil Registry; the sources are not closed and
// stay the caller's. A first snapshot with no values is not refused,
// so a registry can start empty under a schema that requires keys and
// receive them through [Registry.Set].
//
// The registry's codec set (used by `format=` struct tags and [Registry.Save])
// is [DefaultCodecs] unless an option replaces it. Use [NewWithCodecs]
// to link only the codecs a program uses.
func New(opts ...Option) (*Registry, error) {
	options := defaultRegistryOptions()
	for _, opt := range opts {
		opt(&options)
	}
	if options.optionErr != nil {
		return nil, options.optionErr
	}
	options.finishCodecs(DefaultCodecs)
	return buildRegistry(options, startWatchEngine)
}

// NewWithCodecs is [New] with codecs as the registry's codec set in
// place of [DefaultCodecs]; nil means no codecs. Options apply on top
// of it ([WithCodecs] replaces it, [WithCodec] adds to it). A program
// that builds registries only this way (and builds file sources with
// [NewFileSourceWithCodecs]) links no codec it did not pass.
func NewWithCodecs(codecs *Codecs, opts ...Option) (*Registry, error) {
	options := defaultRegistryOptions()
	options.codecs = codecs
	if options.codecs == nil {
		options.codecs = NewCodecs()
	}
	for _, opt := range opts {
		opt(&options)
	}
	if options.optionErr != nil {
		return nil, options.optionErr
	}
	options.finishCodecs(func() *Codecs { return NewCodecs() })
	return buildRegistry(options, startWatchEngine)
}

// buildRegistry builds a Registry from fully resolved options.
func buildRegistry(options registryOptions, startWatch func(*Registry) watchRunner) (*Registry, error) {
	r := &Registry{
		state: &registryState{
			opts:   options,
			logger: options.logger, // its maps are made by the first write (see writable)
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

	// A rejected first snapshot that holds values fails New: returning
	// a registry would hide every one of them. A rejected empty one
	// does not, so values can still arrive later through Set.
	if candidate, err := r.rebuildCandidateLocked(); err != nil && len(candidate.values) > 0 {
		r.state.mu.Unlock()
		return nil, errors.Join(addErr, err)
	}

	// Start the engine while we still hold the lock so the source set it
	// subscribes to matches the snapshot we just installed.
	if !options.noWatch && startWatch != nil {
		r.state.watch = startWatch(r)
	}

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
		var multi MultiError // escapes only when it holds errors
		for _, s := range r.state.sources {
			if cerr := s.Close(); cerr != nil {
				multi.Append(&SourceError{Source: s.Name(), Op: "close", Cause: cerr})
			}
		}
		if len(multi.Errors) > 0 {
			err = &MultiError{Errors: multi.Errors}
		}
	})
	return err
}

// Events returns the channel reload events are delivered on. Each
// reload the watch engine runs (after a watched source changes or a
// [WithPoll] tick) produces one [Event], successful or failed; failures
// retain the previous snapshot and surface via [Event.Err]. Calls to
// [Registry.Reload] and the write methods produce no Event. The channel
// is buffered (capacity from [WithEventBufferSize], default 16). Slow
// consumers cause drops surfaced on the next deliverable Event's
// Warnings. Returns nil for a registry built [WithoutWatch].
func (r *Registry) Events() <-chan Event {
	if r.state.watch == nil {
		return nil
	}
	return r.state.watch.eventsChan()
}

// Reload rebuilds the snapshot from every source's current contents.
// It does not make sources re-read their backing store: a
// [FileSource] re-reads on [FileSource.Reload] or a watcher event, a
// [RemoteSource] on [RemoteSource.Refresh] or its watch. The rebuild
// runs the immutable and schema checks; on failure the previous
// snapshot stays and the error is returned.
func (r *Registry) Reload() error { return r.ReloadContext(context.Background()) }

// ReloadContext is [Registry.Reload] that first checks ctx: a nil ctx
// returns [ErrNilContext] and a done ctx returns its error wrapped.
// ctx is not passed to sources.
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
// or Name() is a reserved label. A registry that watches subscribes to
// s as it does to sources given at construction (see [Watcher]).
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
	if r.state.watch != nil {
		r.state.watch.subscribeLocked(s)
	}
	return nil
}

// InsertSource registers s at precedence index at (0 = highest). An
// out-of-range index is clamped to [0, len(sources)]. Same conflict
// and transactional semantics as [Registry.AddSource].
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
	r.injectWatcherLocked(s)
	r.state.sources = slices.Insert(r.state.sources, at, s)
	if err := r.rebuildSnapshotLocked(); err != nil {
		r.state.sources = slices.Delete(r.state.sources, at, at+1)
		return err
	}
	if r.state.watch != nil {
		r.state.watch.subscribeLocked(s)
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
		if r.state.watch != nil {
			r.state.watch.unsubscribeLocked(name)
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
		secrets := r.secretMatcherLocked()
		r.state.mu.Unlock()
		redactValidationErrorsForSecrets(err, secrets)
		return schemaValidationError(err)
	}
	return nil
}

// addSourceLocked is the helper used by [AddSource] and by [New]'s
// bulk-add. Caller must hold r.state.mu.
//
// The registry-wide watcher factory, if any, is injected via
// [Registry.injectWatcherLocked].
func (r *Registry) addSourceLocked(s Source) error {
	if s == nil {
		return fmt.Errorf("%w: nil Source", ErrInvalidPath)
	}
	if err := r.checkSourceNameLocked(s.Name()); err != nil {
		return err
	}
	r.injectWatcherLocked(s)
	r.state.sources = append(r.state.sources, s)
	return nil
}

// injectWatcherLocked hands the registry-wide [WatcherFactory] to a
// source that exposes [watcherSetter], so [Source.Watch] uses the
// registry's chosen backend. A source that already has its own factory
// (from [WithFileWatcher]) keeps it. Caller must hold r.state.mu.
func (r *Registry) injectWatcherLocked(s Source) {
	if r.state.opts.watcher == nil {
		return
	}
	setter, ok := s.(watcherSetter)
	if !ok {
		return
	}
	if own, ok := s.(watcherGetter); ok && own.watcherFactory() != nil {
		return
	}
	setter.SetWatcher(r.state.opts.watcher)
}

// watcherGetter reports a source's own [WatcherFactory]. Satisfied by
// [FileSource].
type watcherGetter interface {
	watcherFactory() WatcherFactory
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
	if isReservedSourceName(name) {
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
// [*ValidationError] whose Path is secret as Secret. It visits every
// error in the tree, through both Unwrap() error and Unwrap() []error
// (so errors.Join and multiple %w verbs are covered). Applied after
// the validator returns so the [SchemaValidator] interface stays free
// of recon-specific concerns.
func redactValidationErrorsForSecrets(err error, secrets secretMatcher) {
	if err == nil || len(secrets.keys) == 0 {
		return
	}
	if ve, ok := err.(*ValidationError); ok && secrets.has(ve.Path.String()) { //nolint:errorlint // every node of the tree is visited below
		ve.Secret = true
	}
	switch x := err.(type) { //nolint:errorlint // walks the tree by hand
	case interface{ Unwrap() []error }:
		for _, sub := range x.Unwrap() {
			redactValidationErrorsForSecrets(sub, secrets)
		}
	case interface{ Unwrap() error }:
		redactValidationErrorsForSecrets(x.Unwrap(), secrets)
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
	_, err := r.rebuildCandidateLocked()
	return err
}

// rebuildCandidateLocked is [Registry.rebuildSnapshotLocked] that also
// returns the candidate snapshot, installed or not. Caller must hold
// r.state.mu.
func (r *Registry) rebuildCandidateLocked() (*Snapshot, error) {
	is := snapshotInputs{
		sources:    r.state.sources,
		explicits:  snapshotCopy(r.state.explicits),
		defaults:   snapshotCopy(r.state.defaults),
		aliases:    snapshotCopy(r.state.aliases),
		pins:       snapshotCopy(r.state.pins),
		bound:      r.state.bound,
		secretKeys: cloneStringSet(r.state.secretKeys),
		redactor:   r.state.opts.secretRedactor,
		merge:      r.state.opts.merge,
	}
	candidate := buildSnapshot(is)

	var multi MultiError // escapes only when it holds errors, below
	for _, err := range r.checkImmutableLocked(candidate) {
		multi.Append(err)
	}
	if r.state.opts.validator != nil {
		if err := r.state.opts.validator.Validate(candidate.AsMap()); err != nil {
			redactValidationErrorsForSecrets(err, candidate.secrets())
			multi.Append(schemaValidationError(err))
		}
	}
	if len(multi.Errors) > 0 {
		if len(multi.Errors) == 1 {
			return candidate, multi.Errors[0]
		}
		return candidate, &MultiError{Errors: multi.Errors}
	}

	r.state.snapshot.Store(candidate)
	return candidate, nil
}

// checkImmutableLocked returns one [*ImmutableChangedError] per
// baselined path whose candidate value differs or is no longer set
// (New is "<unset>"). Caller must hold r.state.mu.
func (r *Registry) checkImmutableLocked(snap *Snapshot) []error {
	if len(r.state.immutableBaseline) == 0 {
		return nil
	}
	secrets := snap.secrets()
	var errs []error
	for path, baseline := range r.state.immutableBaseline {
		mode := readExact
		if baseline.Kind() == MapKind {
			mode = readSection
		}
		cur, ok := snap.read(path, nil, mode)
		if ok && valuesEqual(baseline, cur) {
			continue
		}
		old := baseline.String()
		next := unsetText
		if ok {
			next = cur.String()
		}
		if secrets.has(path) {
			redactor := r.state.opts.secretRedactor
			if redactor != nil {
				old = redactor(old)
				if ok {
					next = redactor(next)
				}
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

// unsetText is [ImmutableChangedError.New] for a key that is no longer
// set.
const unsetText = "<unset>"

// queueWarning appends w to the registry's pending warning queue,
// unless an equal warning is already queued.
func (r *Registry) queueWarning(w DeprecationWarning) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	for _, q := range r.state.pendingWarnings {
		if q.Source == w.Source && q.Message == w.Message && q.Path.Equal(w.Path) && q.Replacement.Equal(w.Replacement) {
			return
		}
	}
	r.state.pendingWarnings = append(r.state.pendingWarnings, w)
}

// secretMatcherLocked returns a matcher over copies of the registry's
// secret and alias sets. Caller must hold r.state.mu.
func (r *Registry) secretMatcherLocked() secretMatcher {
	return secretMatcher{
		keys:    cloneStringSet(r.state.secretKeys),
		aliases: cloneStringStringMap(r.state.aliases),
	}
}

// DrainWarnings returns and clears the pending warning queue. A warning
// already queued is not queued again, so binding repeatedly does not
// grow it. The watch engine drains the same queue into each reload [Event]; callers
// that run [Registry.Bind] without live reload use this to surface
// deprecations directly. Returns nil when empty.
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

// IsImmutable reports whether key has an immutable baseline, recorded
// when a bind found a value for an `immutable`-tagged field.
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

// snapshotCopy is the copy of m a snapshot build reads: nil when m is empty, which the build
// reads as the empty map it is, and never writes to.
func snapshotCopy[V any](m map[string]V) map[string]V {
	if len(m) == 0 {
		return nil
	}
	return maps.Clone(m)
}

// validateNotClosed returns [ErrRegistryClosed] when r has been Closed.
func (r *Registry) validateNotClosed() error {
	if r.state.closed.Load() {
		return ErrRegistryClosed
	}
	return nil
}

// NewWithCodecsWithoutWatch is [NewWithCodecs] with [WithoutWatch]: a one-shot registry. A
// program that builds registries only this way links no watch engine.
func NewWithCodecsWithoutWatch(codecs *Codecs, opts ...Option) (*Registry, error) {
	options := defaultRegistryOptions()
	options.codecs = codecs
	if options.codecs == nil {
		options.codecs = NewCodecs()
	}
	for _, opt := range opts {
		opt(&options)
	}
	if options.optionErr != nil {
		return nil, options.optionErr
	}
	options.finishCodecs(func() *Codecs { return NewCodecs() })
	options.noWatch = true
	return buildRegistry(options, nil)
}
