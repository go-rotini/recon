package recon

import "time"

// Per-source option types. Each source constructor declares its own
// type so the compiler enforces "this option applies only to that
// source".

// FileOption configures [FileSource] / [FileSourceFS] and the
// format-named constructors.
type FileOption func(*fileOptions)

// BufferOption configures [NewBufferSource].
type BufferOption func(*bufferOptions)

// StdinOption configures [NewStdinSource].
type StdinOption func(*stdinOptions)

// EnvOption configures [NewOSEnvSource].
type EnvOption func(*envOptions)

// FlagOption configures [NewFlagSource].
type FlagOption func(*flagOptions)

// RemoteOption configures [NewRemoteSource].
type RemoteOption func(*remoteOptions)

type (
	fileOptions struct {
		codec         Codec
		watcher       WatcherFactory
		searchPaths   []string
		format        string
		pathExpansion bool
		optional      bool
		// pathExpansionSet records whether the caller invoked
		// [WithPathExpansion] so the default-on stays applied only
		// when the caller stayed silent.
		pathExpansionSet bool
	}

	bufferOptions struct {
		codec Codec
	}

	stdinOptions struct {
		codec Codec
	}

	envOptions struct {
		prefix    string
		transform KeyTransform
		parser    func(name string) Path
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

// bufferCfgCodec exposes the codec field for [NewBufferSource]
// without making the field itself exported.
func bufferCfgCodec(o bufferOptions) Codec { return o.codec }

// WithFileCodec pins the codec, bypassing extension-based resolution.
func WithFileCodec(c Codec) FileOption {
	return func(o *fileOptions) { o.codec = c }
}

// WithFileWatcher overrides the registry-wide [WatcherFactory] for
// this source only.
func WithFileWatcher(w WatcherFactory) FileOption {
	return func(o *fileOptions) { o.watcher = w }
}

// WithSearchPaths makes [FileSource] look for the filename in each
// directory in order; first existing file wins. The constructor's
// primary path argument supplies the filename.
func WithSearchPaths(dirs ...string) FileOption {
	return func(o *fileOptions) { o.searchPaths = append(o.searchPaths, dirs...) }
}

// WithPathExpansion controls POSIX-shell expansion of paths (~, $VAR,
// ${VAR:-default}, ${VAR:?msg}, ${VAR:+alt}). Default true.
func WithPathExpansion(enabled bool) FileOption {
	return func(o *fileOptions) {
		o.pathExpansion = enabled
		o.pathExpansionSet = true
	}
}

// WithOptional treats a missing file as a no-op rather than an error.
// Useful for `.env.local` / `.config.local.yaml` overrides.
func WithOptional(optional bool) FileOption {
	return func(o *fileOptions) { o.optional = optional }
}

// WithFileFormat selects the codec by registered name. Equivalent to
// [WithFileCodec] but useful when the codec value is not in scope.
func WithFileFormat(name string) FileOption {
	return func(o *fileOptions) { o.format = name }
}

// WithBufferCodec pins the codec for [NewBufferSource].
func WithBufferCodec(c Codec) BufferOption {
	return func(o *bufferOptions) { o.codec = c }
}

// WithStdinCodec pins the codec for [NewStdinSource].
func WithStdinCodec(c Codec) StdinOption {
	return func(o *stdinOptions) { o.codec = c }
}

// WithEnvPrefix limits an [OSEnvSource] to env vars whose name starts
// with prefix. The default transform then projects "server.port" to
// "<prefix>SERVER_PORT".
func WithEnvPrefix(prefix string) EnvOption {
	return func(o *envOptions) { o.prefix = prefix }
}

// WithEnvTransform overrides the default path → env-var-name
// projection. Pair with [WithEnvKeyParser] for the inverse used by
// [OSEnvSource.Keys]. Nil is silently ignored.
func WithEnvTransform(fn KeyTransform) EnvOption {
	return func(o *envOptions) {
		if fn != nil {
			o.transform = fn
		}
	}
}

// WithEnvKeyParser overrides the env-var-name → [Path] projection
// used by [OSEnvSource.Keys]. The parser receives the name with any
// configured prefix already stripped; returning an empty Path skips
// the variable. Nil is silently ignored.
func WithEnvKeyParser(fn func(name string) Path) EnvOption {
	return func(o *envOptions) {
		if fn != nil {
			o.parser = fn
		}
	}
}

// WithFlagName overrides the default "flags" source name. Useful
// when composing multiple flag adapters into one registry.
func WithFlagName(name string) FlagOption {
	return func(o *flagOptions) {
		if name != "" {
			o.name = name
		}
	}
}

// WithFlagPathTransform replaces the default flag-name → [Path]
// transform. Useful when "--server-port" should resolve to the path
// "server.port" rather than the single-segment "server-port".
func WithFlagPathTransform(fn func(flagName string) Path) FlagOption {
	return func(o *flagOptions) {
		if fn != nil {
			o.transform = fn
		}
	}
}

// WithRemotePrefix scopes a [RemoteSource] to keys under prefix.
// Combine with [WithRemoteTrimPrefix] when the registry should see
// keys without the prefix.
func WithRemotePrefix(prefix string) RemoteOption {
	return func(o *remoteOptions) { o.prefix = prefix }
}

// WithRemotePollInterval enables polling for backends without
// [BackendWatcher]. A zero or negative interval keeps the source
// non-polling. Ignored for backends that already implement
// [BackendWatcher].
func WithRemotePollInterval(d time.Duration) RemoteOption {
	return func(o *remoteOptions) { o.poll = d }
}

// WithRemoteTrimPrefix strips the configured prefix from cached keys
// before they're exposed.
func WithRemoteTrimPrefix(trim bool) RemoteOption {
	return func(o *remoteOptions) { o.trimPrefix = trim }
}
