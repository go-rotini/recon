package recon

import (
	"log/slog"
	"time"
)

// Option configures a [Registry] at construction time. Options are applied
// in the order passed to [New]; later options override earlier ones when
// they touch the same setting (e.g., two WithReloadDebounce calls — the last
// wins).
type Option func(*registryOptions)

// registryOptions is the internal struct Option closures mutate. The fields
// are intentionally unexported so the option surface — not the struct — is
// the public contract. Defaults are applied by [defaultRegistryOptions].
type registryOptions struct {
	// Sources & precedence
	initialSources []Source
	precedence     []string // explicit order by source name, when set

	// Key handling
	keyDelimiter  string
	caseSensitive bool
	keyNormalizer func(Path) Path

	// Decoding / strictness
	strict        bool
	errorBehavior ErrorBehavior

	// Live reload
	reloadDebounce time.Duration
	eventBufSize   int
	pollInterval   time.Duration

	// Validation
	validator  SchemaValidator
	requireAll bool

	// Codec / watcher overrides
	codecs  *Codecs
	watcher WatcherFactory

	// Secrets
	secretRedactor func(string) string

	// Observability
	logger  *slog.Logger
	metrics MetricsRecorder

	// Merge strategy
	merge MergeStrategy
}

// defaultRegistryOptions returns the registryOptions a fresh [Registry] uses
// before user-supplied options are applied. Defaults are documented in §2.2,
// §2.5, and §4.15 of the requirements doc.
//
//nolint:unused // consumed by Registry.New in Phase 3 (registry.go).
func defaultRegistryOptions() registryOptions {
	return registryOptions{
		keyDelimiter:   DefaultDelimiter,
		caseSensitive:  true,
		errorBehavior:  FailCollect,
		reloadDebounce: 50 * time.Millisecond,
		eventBufSize:   16,
		merge:          MergeShadow,
		secretRedactor: defaultSecretRedactor,
	}
}

// defaultSecretRedactor is the registry-wide fallback for redacting secret
// values in [Describe] / [Snapshot.String] / error messages. Replace via
// [WithSecretRedactor].
//
//nolint:unused // installed as the default by defaultRegistryOptions; both surface in Phase 3.
func defaultSecretRedactor(string) string { return "***" }

// ErrorBehavior controls how the decoder accumulates per-field errors during
// a Bind / Unmarshal. Mirrors the rotini spec's `validation.behavior` key
// (see §5.7 of the requirements doc).
type ErrorBehavior int

// ErrorBehavior values.
const (
	// FailCollect aggregates every per-field error into a *MultiError so
	// handlers can surface all problems at once. This is the default.
	FailCollect ErrorBehavior = iota
	// FailFast stops decoding at the first per-field error.
	FailFast
)

// MergeStrategy controls how the registry combines values when multiple
// sources hold the same key. The default — MergeShadow — replaces lower-
// precedence values entirely; deep-merge is opt-in.
type MergeStrategy int

// MergeStrategy values.
const (
	// MergeShadow has the higher-precedence source replace the lower's value
	// in its entirety. No structural merging of maps or slices. This is the
	// default and what Viper does.
	MergeShadow MergeStrategy = iota
	// MergeAppend appends slices and deep-merges maps; scalar values still
	// shadow.
	MergeAppend
	// MergeReplace is an explicit alias for MergeShadow.
	MergeReplace
)

// MetricsRecorder is an optional sink for counters / timers the registry
// emits internally. Implementations should be cheap (no I/O on the hot
// path); the registry calls these methods inline from the resolution and
// reload paths.
type MetricsRecorder interface {
	IncCounter(name string)
	ObserveDuration(name string, d time.Duration)
}

// WithSource registers a single source. Equivalent to [Registry.AddSource]
// called after construction.
func WithSource(s Source) Option {
	return func(o *registryOptions) {
		if s != nil {
			o.initialSources = append(o.initialSources, s)
		}
	}
}

// WithSources registers multiple sources in the given order. Each is
// appended at a lower precedence than the previous (first argument = highest
// precedence among this batch).
func WithSources(s ...Source) Option {
	return func(o *registryOptions) {
		for _, src := range s {
			if src != nil {
				o.initialSources = append(o.initialSources, src)
			}
		}
	}
}

// WithPrecedence re-orders the registered sources by name. Names not in the
// list keep their original relative order, appended after the named ones.
// Applied after every source has been added so the call site does not depend
// on option ordering.
func WithPrecedence(order ...string) Option {
	return func(o *registryOptions) { o.precedence = append([]string(nil), order...) }
}

// WithKeyDelimiter overrides the registry-wide path delimiter. Default: ".".
func WithKeyDelimiter(d string) Option {
	return func(o *registryOptions) { o.keyDelimiter = d }
}

// WithCaseInsensitive enables Viper-style case-insensitive key matching.
// Off by default — recon prefers case-sensitive lookups so `Server.Port` and
// `server.port` are distinct keys.
func WithCaseInsensitive() Option {
	return func(o *registryOptions) { o.caseSensitive = false }
}

// WithKeyNormalizer installs a transform applied to every Path before it
// reaches the resolution pipeline. Use sparingly — most key-spelling
// concerns are better handled by per-source key transforms or aliases.
func WithKeyNormalizer(fn func(Path) Path) Option {
	return func(o *registryOptions) { o.keyNormalizer = fn }
}

// WithStrict enables strict-mode decoding: unknown keys and ambiguous
// coercions become errors instead of silently being ignored / accepted.
func WithStrict() Option {
	return func(o *registryOptions) { o.strict = true }
}

// WithLenient is the explicit opt-out from strict mode. Default behavior
// is already lenient; this option exists for symmetry.
func WithLenient() Option {
	return func(o *registryOptions) { o.strict = false }
}

// WithErrorBehavior controls how per-field errors accumulate during a Bind /
// Unmarshal. See [ErrorBehavior].
func WithErrorBehavior(b ErrorBehavior) Option {
	return func(o *registryOptions) { o.errorBehavior = b }
}

// WithReloadDebounce sets how long the engine waits for additional change
// events before firing a reload. Default: 50ms.
func WithReloadDebounce(d time.Duration) Option {
	return func(o *registryOptions) { o.reloadDebounce = d }
}

// WithEventBufferSize sets the capacity of the public Events channel. Default: 16.
func WithEventBufferSize(n int) Option {
	return func(o *registryOptions) {
		if n > 0 {
			o.eventBufSize = n
		}
	}
}

// WithPoll polls non-Watcher sources at the given interval. Off by default —
// sources that don't watch (OSEnv, MapSource) typically don't change for a
// running process, so polling them is unnecessary.
func WithPoll(interval time.Duration) Option {
	return func(o *registryOptions) { o.pollInterval = interval }
}

// WithValidator installs a [SchemaValidator] the registry runs after every
// load. The bundled implementation is [JSONSchemaValidator]; users can plug
// in any other validator behind the same interface.
func WithValidator(v SchemaValidator) Option {
	return func(o *registryOptions) { o.validator = v }
}

// WithRequireAll declares that every key the registry sees must come from
// some source — there are no implicit "missing key is fine" lookups. Used
// when a CLI's spec mandates that every input be supplied.
func WithRequireAll() Option {
	return func(o *registryOptions) { o.requireAll = true }
}

// WithCodec registers (or replaces) a [Codec] in the registry's codec set.
// Registration is keyed by [Codec.Name] so a user-supplied "yaml" codec
// shadows the bundled default.
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

// WithoutCodec removes a codec by name. Useful for "I want the defaults
// except YAML" scenarios.
func WithoutCodec(name string) Option {
	return func(o *registryOptions) {
		if o.codecs == nil {
			o.codecs = NewCodecs()
		}
		o.codecs.Unregister(name)
	}
}

// WithCodecs replaces the registry's entire codec set with the supplied one.
// Combine with [WithCodec] / [WithoutCodec] for surgical adjustments.
func WithCodecs(cs *Codecs) Option {
	return func(o *registryOptions) { o.codecs = cs }
}

// WithWatcher installs a registry-wide [WatcherFactory]. File-backed sources
// that opt into watching without a per-source factory use this one. Default:
// FSWatcher (backed by go-rotini/fs.Watcher).
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

// WithLogger installs a logger the registry uses for non-fatal diagnostics
// (dropped events, source-reload warnings). Default: slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(o *registryOptions) { o.logger = l }
}

// WithMetrics installs a [MetricsRecorder] for the registry's internal
// counters / timers.
func WithMetrics(m MetricsRecorder) Option {
	return func(o *registryOptions) { o.metrics = m }
}

// WithMerge controls how overlapping values from multiple sources are
// combined. See [MergeStrategy].
func WithMerge(strategy MergeStrategy) Option {
	return func(o *registryOptions) { o.merge = strategy }
}
