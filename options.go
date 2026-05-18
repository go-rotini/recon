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

	codecs  *Codecs
	watcher WatcherFactory

	secretRedactor func(string) string

	logger *slog.Logger

	merge MergeStrategy

	// optionErr captures a failure produced by an Option that needs to
	// surface an error (e.g. [WithSchema] when schema compilation
	// fails). [New] returns it as its error.
	optionErr error
}

// defaultRegistryOptions returns the construction defaults:
// case-sensitive keys, [FailCollect], 50ms reload debounce, 16-event
// buffer, [MergeShadow], "***" secret redactor.
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

// WithStrict enables strict-mode decoding: unknown keys and ambiguous
// coercions become errors.
func WithStrict() Option {
	return func(o *registryOptions) { o.strict = true }
}

// WithLenient is the explicit opt-out from strict mode (default).
func WithLenient() Option {
	return func(o *registryOptions) { o.strict = false }
}

// WithErrorBehavior controls per-field error aggregation during
// [Bind] / [Unmarshal]. See [ErrorBehavior].
func WithErrorBehavior(b ErrorBehavior) Option {
	return func(o *registryOptions) { o.errorBehavior = b }
}

// WithReloadDebounce sets how long the engine waits for additional
// change events before firing a reload. Default 50ms.
func WithReloadDebounce(d time.Duration) Option {
	return func(o *registryOptions) { o.reloadDebounce = d }
}

// WithEventBufferSize sets the capacity of the public Events channel.
// Default 16.
func WithEventBufferSize(n int) Option {
	return func(o *registryOptions) {
		if n > 0 {
			o.eventBufSize = n
		}
	}
}

// WithPoll polls non-[Watcher] sources at interval. Off by default.
func WithPoll(interval time.Duration) Option {
	return func(o *registryOptions) { o.pollInterval = interval }
}

// WithValidator installs a [SchemaValidator] run after every load.
func WithValidator(v SchemaValidator) Option {
	return func(o *registryOptions) { o.validator = v }
}

// WithSchema compiles schemaJSON via [NewJSONSchemaValidator] and
// installs the result as the validator. A compile failure rides on
// the options struct and is surfaced by [New]'s error return.
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
// registry's codec set.
func WithCodec(c Codec) Option {
	return func(o *registryOptions) {
		if c == nil {
			return
		}
		if o.codecs == nil {
			o.codecs = NewCodecs()
		}
		o.codecs.Register(c)
	}
}

// WithoutCodec removes a codec by name.
func WithoutCodec(name string) Option {
	return func(o *registryOptions) {
		if o.codecs == nil {
			o.codecs = NewCodecs()
		}
		o.codecs.Unregister(name)
	}
}

// WithCodecs replaces the registry's entire codec set.
func WithCodecs(cs *Codecs) Option {
	return func(o *registryOptions) { o.codecs = cs }
}

// WithWatcher installs a registry-wide [WatcherFactory] used by
// file-backed sources that don't ship their own. Default is
// [FSWatcher].
func WithWatcher(w WatcherFactory) Option {
	return func(o *registryOptions) { o.watcher = w }
}

// WithSecretRedactor replaces the default "***" redactor.
func WithSecretRedactor(fn func(string) string) Option {
	return func(o *registryOptions) {
		if fn != nil {
			o.secretRedactor = fn
		}
	}
}

// WithLogger installs the logger used for non-fatal diagnostics.
// Default slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(o *registryOptions) { o.logger = l }
}

// WithMerge controls how overlapping values from multiple sources
// combine. See [MergeStrategy].
func WithMerge(strategy MergeStrategy) Option {
	return func(o *registryOptions) { o.merge = strategy }
}
