package recon

import "time"

// Per-source option types. Each source constructor (FileSource, BufferSource,
// StdinSource, OSEnvSource, …) declares its own option type so the compiler
// can enforce "this option only applies to that source." A future source
// addition adds a new option type rather than overloading a single Option.
//
// The concrete option types are intentionally simple func-mutation closures
// over an unexported settings struct — same shape as registry-level Option.

// FileOption configures a [FileSource] / [FileSourceFS] /
// format-named-source (YAMLSource, TOMLSource, JSONCSource, JSONSource,
// DotenvSource) constructor.
type FileOption func(*fileOptions)

// BufferOption configures a [BufferSource] constructor.
type BufferOption func(*bufferOptions)

// StdinOption configures a [StdinSource] constructor.
type StdinOption func(*stdinOptions)

// EnvOption configures an [OSEnvSource] constructor.
type EnvOption func(*envOptions)

// FlagOption configures a [FlagSource] constructor.
type FlagOption func(*flagOptions)

// RemoteOption configures a [RemoteSource] constructor.
type RemoteOption func(*remoteOptions)

// fileOptions / bufferOptions / stdinOptions / envOptions / remoteOptions
// are the internal settings structs each source's With* option closures
// mutate.
type (
	fileOptions struct {
		codec         Codec
		watcher       WatcherFactory
		searchPaths   []string
		format        string
		pathExpansion bool
		optional      bool
		// pathExpansionSet records whether the caller explicitly invoked
		// WithPathExpansion so the constructor can apply the default-true
		// only when the caller stayed silent.
		pathExpansionSet bool
	}

	bufferOptions struct {
		codec Codec
	}

	stdinOptions struct {
		codec Codec
	}

	envOptions struct {
		prefix string
	}

	flagOptions struct {
		name      string
		transform func(flagName string) Path
	}

	remoteOptions struct {
		prefix     string
		poll       time.Duration
		trimPrefix bool
	}
)

// bufferCfgCodec is the read-side accessor used by [NewBufferSource] to
// extract the codec a caller passed via [WithBufferCodec]. Kept package-
// private so the field on bufferOptions stays unexported.
func bufferCfgCodec(o bufferOptions) Codec { return o.codec }

// WithFileCodec overrides codec resolution for this source. By default a
// [FileSource] resolves the codec via [Codecs.ByExtension] on the file
// path; passing WithFileCodec short-circuits that and uses the supplied
// [Codec] directly.
func WithFileCodec(c Codec) FileOption {
	return func(o *fileOptions) { o.codec = c }
}

// WithFileWatcher overrides the registry-wide [WatcherFactory] for this
// source only. Useful when a single file needs a different watching
// strategy (polling instead of fsnotify, say) than the rest of the
// configuration.
func WithFileWatcher(w WatcherFactory) FileOption {
	return func(o *fileOptions) { o.watcher = w }
}

// WithSearchPaths configures [FileSource] to look for the given filename
// across the named directories in order; the first existing file wins. The
// constructor's primary `path` argument is used as the filename; the
// search-paths option supplies the directories. Combine with
// [WithPathExpansion] for shell-style expansion of each directory.
func WithSearchPaths(dirs ...string) FileOption {
	return func(o *fileOptions) { o.searchPaths = append(o.searchPaths, dirs...) }
}

// WithPathExpansion controls whether [FileSource] runs paths through POSIX
// shell-style expansion (`~`, `$VAR`, `${VAR-default}`, `${VAR:-default}`,
// `${VAR:?msg}`). Default: true.
func WithPathExpansion(enabled bool) FileOption {
	return func(o *fileOptions) {
		o.pathExpansion = enabled
		o.pathExpansionSet = true
	}
}

// WithOptional treats a missing file as a no-op rather than an error. The
// resulting [Source] is registered but holds no keys. Useful for `.env.local`
// / `.config.local.yaml`-style optional overrides.
func WithOptional(optional bool) FileOption {
	return func(o *fileOptions) { o.optional = optional }
}

// WithFileFormat overrides extension-based codec selection by an explicit
// codec [Name]. Equivalent to [WithFileCodec] but referenced by name instead
// of by codec value — useful when the caller doesn't have the codec in scope
// but knows it's registered.
func WithFileFormat(name string) FileOption {
	return func(o *fileOptions) { o.format = name }
}

// WithBufferCodec overrides codec resolution for a [BufferSource].
func WithBufferCodec(c Codec) BufferOption {
	return func(o *bufferOptions) { o.codec = c }
}

// WithStdinCodec overrides codec resolution for a [StdinSource].
func WithStdinCodec(c Codec) StdinOption {
	return func(o *stdinOptions) { o.codec = c }
}

// WithEnvPrefix limits an [OSEnvSource] to environment variables whose name
// starts with prefix (e.g., "APP_" → only `APP_*` vars are visible).
func WithEnvPrefix(prefix string) EnvOption {
	return func(o *envOptions) { o.prefix = prefix }
}

// WithFlagName overrides the default source name ("flags") a
// [FlagSource] reports. Useful when a registry composes more than
// one flag adapter — global flags + subcommand flags, say.
func WithFlagName(name string) FlagOption {
	return func(o *flagOptions) {
		if name != "" {
			o.name = name
		}
	}
}

// WithFlagPathTransform replaces the default flag-name → [Path]
// transform with a caller-supplied function. Useful when the flag
// parser uses a naming convention recon should rewrite — e.g.,
// `--server-port` should resolve to the path `server.port` instead
// of the single-segment `server-port`.
//
// A nil transform is silently ignored; the previous transform is
// retained.
func WithFlagPathTransform(fn func(flagName string) Path) FlagOption {
	return func(o *flagOptions) {
		if fn != nil {
			o.transform = fn
		}
	}
}

// WithRemotePrefix scopes a [RemoteSource] to keys under prefix. The
// prefix is supplied verbatim to [RemoteBackend.List]; the cache and
// every subsequent [Source.Get] are populated against this filtered
// set.
//
// Pair with [WithRemoteTrimPrefix] when the path the registry should
// surface differs from the prefix the backend stores.
func WithRemotePrefix(prefix string) RemoteOption {
	return func(o *remoteOptions) { o.prefix = prefix }
}

// WithRemotePollInterval enables polling for backends that do NOT
// implement [BackendWatcher]. A zero / negative interval keeps the
// source non-polling (the watcher returns a closed channel —
// "nothing to listen to"). Has no effect on backends that already
// implement BackendWatcher; the push path is preferred.
func WithRemotePollInterval(d time.Duration) RemoteOption {
	return func(o *remoteOptions) { o.poll = d }
}

// WithRemoteTrimPrefix strips the configured prefix from cached
// keys before they're exposed to the registry. Useful when the
// backend stores under "/myapp/" but the registry should see paths
// without the prefix.
func WithRemoteTrimPrefix(trim bool) RemoteOption {
	return func(o *remoteOptions) { o.trimPrefix = trim }
}
