package recon

// Per-source option types. Each source constructor (FileSource, BufferSource,
// StdinSource, OSEnvSource, …) declares its own option type so the compiler
// can enforce "this option only applies to that source." A future source
// addition adds a new option type rather than overloading a single Option.
//
// The concrete option types are intentionally simple func-mutation closures
// over an unexported settings struct — same shape as registry-level Option.
// The actual settings structs live in the per-source files that land in
// Phase 4 (source_file.go, source_buffer.go, etc.). The types declared here
// are the public seam; their internal mutators are introduced alongside
// each source's implementation.

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

// RemoteOption configures a [RemoteSource] constructor.
type RemoteOption func(*remoteOptions)

// fileOptions / bufferOptions / stdinOptions / envOptions / remoteOptions
// are forward-declared structs; their concrete fields are populated in
// Phase 4 alongside each source's implementation. Declaring them as empty
// here lets the public option types compile and lets downstream test code
// import-and-call options without waiting for Phase 4.
type (
	fileOptions   struct{}
	bufferOptions struct{}
	stdinOptions  struct{}
	envOptions    struct{}
	remoteOptions struct{}
)

//
// These are declared up-front so Phase 4's source_file.go implementation has
// a stable public surface to populate. Each Phase-4 implementation appends
// fields to fileOptions and wires them through to the constructor.

// WithFileCodec overrides codec resolution for this source. By default a
// [FileSource] resolves the codec via [Codecs.ByExtension] on the file
// path; passing WithFileCodec short-circuits that and uses the supplied
// [Codec] directly.
func WithFileCodec(c Codec) FileOption {
	return func(o *fileOptions) { setFileCodec(o, c) }
}

// WithFileWatcher overrides the registry-wide [WatcherFactory] for this
// source only. Useful when a single file needs a different watching
// strategy (polling instead of fsnotify, say) than the rest of the
// configuration.
func WithFileWatcher(w WatcherFactory) FileOption {
	return func(o *fileOptions) { setFileWatcher(o, w) }
}

// WithSearchPaths configures [FileSource] to look for the given filename
// across the named directories in order; the first existing file wins. The
// constructor's primary `path` argument is used as the filename; the
// search-paths option supplies the directories. Combine with
// [WithPathExpansion] for shell-style expansion of each directory.
func WithSearchPaths(dirs ...string) FileOption {
	return func(o *fileOptions) { setFileSearchPaths(o, dirs) }
}

// WithPathExpansion controls whether [FileSource] runs paths through POSIX
// shell-style expansion (`~`, `$VAR`, `${VAR-default}`, `${VAR:-default}`,
// `${VAR:?msg}`). Default: true.
func WithPathExpansion(enabled bool) FileOption {
	return func(o *fileOptions) { setFilePathExpansion(o, enabled) }
}

// WithOptional treats a missing file as a no-op rather than an error. The
// resulting [Source] is registered but holds no keys. Useful for `.env.local`
// / `.config.local.yaml`-style optional overrides.
func WithOptional(optional bool) FileOption {
	return func(o *fileOptions) { setFileOptional(o, optional) }
}

// WithFileFormat overrides extension-based codec selection by an explicit
// codec [Name]. Equivalent to [WithFileCodec] but referenced by name instead
// of by codec value — useful when the caller doesn't have the codec in scope
// but knows it's registered.
func WithFileFormat(name string) FileOption {
	return func(o *fileOptions) { setFileFormat(o, name) }
}

// WithBufferCodec overrides codec resolution for a [BufferSource].
func WithBufferCodec(c Codec) BufferOption {
	return func(o *bufferOptions) { setBufferCodec(o, c) }
}

// WithStdinCodec overrides codec resolution for a [StdinSource].
func WithStdinCodec(c Codec) StdinOption {
	return func(o *stdinOptions) { setStdinCodec(o, c) }
}

// WithEnvPrefix limits an [OSEnvSource] to environment variables whose name
// starts with prefix (e.g., "APP_" → only `APP_*` vars are visible).
func WithEnvPrefix(prefix string) EnvOption {
	return func(o *envOptions) { setEnvPrefix(o, prefix) }
}

//
// The setters below are deliberately written as separate package-level
// helpers (not direct field assignments) so Phase 4's source_*.go files can
// re-declare them with real bodies without breaking Phase 2's public surface.
// In Phase 2 every setter is a no-op — fileOptions et al. are empty structs.

func setFileCodec(_ *fileOptions, _ Codec)            {}
func setFileWatcher(_ *fileOptions, _ WatcherFactory) {}
func setFileSearchPaths(_ *fileOptions, _ []string)   {}
func setFilePathExpansion(_ *fileOptions, _ bool)     {}
func setFileOptional(_ *fileOptions, _ bool)          {}
func setFileFormat(_ *fileOptions, _ string)          {}

func setBufferCodec(_ *bufferOptions, _ Codec) {}
func setStdinCodec(_ *stdinOptions, _ Codec)   {}

func setEnvPrefix(_ *envOptions, _ string) {}
