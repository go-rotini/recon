package recon

import (
	"log/slog"
	"time"
)

// Option configures a [Registry] at construction time. Options are
// applied in the order passed to [New]; later options override earlier
// ones when they touch the same setting.
type Option func(*registryOptions)

// registryOptions is the internal struct Option closures mutate. The
// fields are unexported so the public contract is the option surface,
// not the struct. Defaults come from [defaultRegistryOptions].
type registryOptions struct {
	initialSources []Source
	precedence     []string // explicit order by source name

	strict        bool
	errorBehavior ErrorBehavior

	reloadDebounce time.Duration
	eventBufSize   int
	pollInterval   time.Duration

	validator SchemaValidator

	codecs     *Codecs
	codecEdits []func(*Codecs) // WithCodec/WithoutCodec before a set exists
	watcher    WatcherFactory
	noWatch    bool

	secretRedactor func(string) string

	logger *slog.Logger

	merge MergeStrategy

	// optionErr captures a failure produced by an Option that needs to
	// surface an error (e.g. [WithSchema] when schema compilation
	// fails). [New] returns it as its error.
	optionErr error
}

// defaultRegistryOptions returns the construction defaults:
// [FailCollect], 50ms reload debounce, 16-event buffer, [MergeShadow],
// "***" secret redactor.
func defaultRegistryOptions() registryOptions {
	return registryOptions{
		errorBehavior:  FailCollect,
		reloadDebounce: 50 * time.Millisecond,
		eventBufSize:   16,
		merge:          MergeShadow,
		secretRedactor: defaultSecretRedactor,
	}
}

func defaultSecretRedactor(string) string { return "***" }

// WithSource registers a single source. Equivalent to
// [Registry.AddSource] after construction.
func WithSource(s Source) Option {
	return func(o *registryOptions) {
		if s != nil {
			o.initialSources = append(o.initialSources, s)
		}
	}
}

// WithSources registers multiple sources in the given order. The
// first argument is the highest precedence among this batch.
func WithSources(s ...Source) Option {
	return func(o *registryOptions) {
		for _, src := range s {
			if src != nil {
				o.initialSources = append(o.initialSources, src)
			}
		}
	}
}

// WithPrecedence re-orders the registered sources by name after all
// sources have been added. Names not in the list keep their original
// relative order and are appended after the named ones.
func WithPrecedence(order ...string) Option {
	return func(o *registryOptions) { o.precedence = append([]string(nil), order...) }
}

// WithStrict makes every [Registry.Bind] strict by default: each key
// under the bind's prefix that no field consumed is reported as an
// [*UnknownKeyError]. [WithDecodeLenient] turns it off per call.
func WithStrict() Option {
	return func(o *registryOptions) { o.strict = true }
}

// WithLenient turns strict binding off, the default.
func WithLenient() Option {
	return func(o *registryOptions) { o.strict = false }
}

// WithErrorBehavior controls per-field error aggregation during
// [Registry.Bind] and [Registry.Unmarshal]. See [ErrorBehavior].
func WithErrorBehavior(b ErrorBehavior) Option {
	return func(o *registryOptions) { o.errorBehavior = b }
}

// WithReloadDebounce sets how long the engine waits for additional
// change events before firing a reload. Default 50ms.
func WithReloadDebounce(d time.Duration) Option {
	return func(o *registryOptions) { o.reloadDebounce = d }
}

// WithEventBufferSize sets the capacity of the [Registry.Events]
// channel. Default 16; n ≤ 0 is ignored.
func WithEventBufferSize(n int) Option {
	return func(o *registryOptions) {
		if n > 0 {
			o.eventBufSize = n
		}
	}
}

// WithPoll rebuilds the snapshot every interval when the registry has
// a source that is not a [Watcher], first calling Refresh on those
// with a Refresh() int method, such as [OSEnvSource.Refresh]. Off by
// default.
func WithPoll(interval time.Duration) Option {
	return func(o *registryOptions) { o.pollInterval = interval }
}

// WithValidator installs a [SchemaValidator] run on every snapshot
// rebuild; a rejected rebuild keeps the previous snapshot.
func WithValidator(v SchemaValidator) Option {
	return func(o *registryOptions) { o.validator = v }
}

// WithSchema compiles schemaJSON via [NewJSONSchemaValidator] and
// installs the result as the validator. A compile failure is returned
// by [New] or [NewWithCodecs].
//
// Use [NewJSONSchemaValidatorYAML] / [NewJSONSchemaValidatorTOML] /
// [NewJSONSchemaValidatorJSONC] with [WithValidator] for non-JSON
// schema sources.
func WithSchema(schemaJSON []byte) Option {
	return func(o *registryOptions) {
		v, err := NewJSONSchemaValidator(schemaJSON)
		if err != nil {
			o.optionErr = err
			return
		}
		o.validator = v
	}
}

// WithCodec registers (or replaces by name) a [Codec] in the
// registry's codec set: under [New] the new codec joins (rather than
// replaces) the bundled defaults, under [NewWithCodecs] the set passed
// there. Pair with [WithCodecs] for a clean-slate set.
func WithCodec(c Codec) Option {
	return func(o *registryOptions) {
		if c != nil {
			o.editCodecs(func(cs *Codecs) { cs.Register(c) })
		}
	}
}

// WithoutCodec removes a codec by name from the registry's codec set;
// under [New] that means "the defaults except name".
func WithoutCodec(name string) Option {
	return func(o *registryOptions) {
		o.editCodecs(func(cs *Codecs) { cs.Unregister(name) })
	}
}

// WithCodecs replaces the registry's entire codec set.
func WithCodecs(cs *Codecs) Option {
	return func(o *registryOptions) {
		o.codecs = cs
		o.codecEdits = nil
	}
}

// editCodecs applies edit to the codec set now or, while no set is
// chosen yet, once the constructor installs its base set.
func (o *registryOptions) editCodecs(edit func(*Codecs)) {
	if o.codecs == nil {
		o.codecEdits = append(o.codecEdits, edit)
		return
	}
	edit(o.codecs)
}

// finishCodecs installs base when no set was chosen and applies the
// edits queued before one was.
func (o *registryOptions) finishCodecs(base func() *Codecs) {
	if o.codecs == nil {
		o.codecs = base()
	}
	for _, edit := range o.codecEdits {
		edit(o.codecs)
	}
	o.codecEdits = nil
}

// WithWatcher installs a registry-wide [WatcherFactory] for each
// [FileSource] without its own [WithFileWatcher]. There is no default:
// without one, file sources are read once and never watched. Use
// fswatch.New() from github.com/go-rotini/recon/fswatch for kernel
// notifications, or [NewPollWatcher] for a stdlib-only poller.
func WithWatcher(w WatcherFactory) Option {
	return func(o *registryOptions) { o.watcher = w }
}

// WithoutWatch builds a one-shot registry: no watch engine, no
// goroutine, no source subscriptions and no [WithPoll] ticker. Values
// change only through [Registry.Reload], [Registry.Set] and friends;
// [Registry.Events] returns nil, and [NewLive] refuses the registry.
func WithoutWatch() Option {
	return func(o *registryOptions) { o.noWatch = true }
}

// WithSecretRedactor replaces the default redactor, which renders every
// secret value as "***". Nil is ignored.
func WithSecretRedactor(fn func(string) string) Option {
	return func(o *registryOptions) {
		if fn != nil {
			o.secretRedactor = fn
		}
	}
}

// WithLogger installs the logger used for non-fatal diagnostics.
// Default [slog.Default].
func WithLogger(l *slog.Logger) Option {
	return func(o *registryOptions) { o.logger = l }
}

// WithMerge controls how overlapping values from multiple sources
// combine. See [MergeStrategy].
func WithMerge(strategy MergeStrategy) Option {
	return func(o *registryOptions) { o.merge = strategy }
}
