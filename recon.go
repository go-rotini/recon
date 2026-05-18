package recon

// DefaultCodecs returns a fresh [Codecs] registry populated with the
// bundled format codecs ([YAML], [TOML], [JSONC], [JSON], [Dotenv]).
// Each call returns an independent registry; callers that need
// shared state should obtain one and pass it around.
func DefaultCodecs() *Codecs {
	return NewCodecs(YAML, TOML, JSONC, JSON, Dotenv)
}

// installDefaultCodecs gives opts a codec set when the caller did
// not supply one. A non-nil opts.codecs is left alone — the caller
// owns the set, including the freedom to omit bundled defaults.
func installDefaultCodecs(opts *registryOptions) {
	if opts.codecs == nil {
		opts.codecs = DefaultCodecs()
	}
}

// installDefaultWatcher installs [FSWatcher] when the caller did
// not supply a [WatcherFactory].
func installDefaultWatcher(opts *registryOptions) {
	if opts.watcher == nil {
		opts.watcher = NewFSWatcher()
	}
}
