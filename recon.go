package recon

// Package-level entry points and the default codec set.
//
// This file declares the helpers that wire the bundled codecs ([YAML],
// [TOML], [JSONC], [JSON], [Dotenv]) into a fresh [Registry] so that
// FileSource / format-named source constructors resolve their codec by
// extension without the caller having to register anything manually.
//
// User overrides go through the registry-level [WithCodec] /
// [WithoutCodec] / [WithCodecs] options; this file only establishes the
// defaults.

// DefaultCodecs returns a fresh [Codecs] registry pre-populated with
// every bundled format codec ([YAML], [TOML], [JSONC], [JSON], [Dotenv]).
// Mutate the returned registry to customize the codec set; pass it to
// [WithCodecs] to install the customized set on a [Registry].
//
// Each call returns an independent registry — callers that want shared
// state should obtain one DefaultCodecs() and pass it around.
func DefaultCodecs() *Codecs {
	return NewCodecs(YAML, TOML, JSONC, JSON, Dotenv)
}

// installDefaultCodecs gives the registry's options a codec set when the
// caller did not supply one. Called from [New] after Option closures have
// run but before the initial-source bulk-add — sources that resolve their
// codec by extension consult this populated set.
//
// If the caller passed any codec-related option ([WithCodec],
// [WithoutCodec], [WithCodecs]), opts.codecs is non-nil and this
// function is a no-op: the caller owns the codec set, including the
// freedom to omit bundled defaults entirely. Callers wanting "defaults
// plus my override" should start from [DefaultCodecs] and pass the
// modified registry via [WithCodecs].
func installDefaultCodecs(opts *registryOptions) {
	if opts.codecs == nil {
		opts.codecs = DefaultCodecs()
	}
}

// installDefaultWatcher installs [FSWatcher] as the registry's
// [WatcherFactory] when the caller did not supply one via [WithWatcher].
// File-backed sources consult this factory when they need a watch
// subscription; the default keeps the "every file source live-reloads"
// behavior on by default.
func installDefaultWatcher(opts *registryOptions) {
	if opts.watcher == nil {
		opts.watcher = NewFSWatcher()
	}
}
