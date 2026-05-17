package recon

// SaveOption configures a single [Registry.Save] / [Registry.SaveTo] /
// [Registry.GenerateTemplate] call, or a [Writer.Save] call on a source.
//
// SaveOption is a separate type from [Option] because Save is a per-call
// operation — every Save can pick a different output format, include /
// exclude secrets, scope to a sub-tree, etc., without re-configuring the
// registry.
type SaveOption func(*saveOptions)

// saveOptions is the internal struct SaveOption closures mutate.
type saveOptions struct {
	// format, when set, overrides path-based detection. Useful when the
	// destination file extension doesn't reflect its content (e.g., writing
	// YAML to a `.txt` file).
	format string

	// includeSecrets emits secret-tagged values verbatim instead of
	// redacting. Default off — Save is meant to be safe-to-pipe-anywhere by
	// default. Turn on only when the destination is known to be private.
	includeSecrets bool

	// includeDefaults emits keys whose only source is SetDefault. Default
	// off — Save typically reports the "explicit" view, not the
	// defaults-plus-overrides view.
	includeDefaults bool

	// onlyPrefix scopes Save to the named sub-tree.
	onlyPrefix string
}

// WithSaveFormat forces the output format regardless of the destination
// path's extension. Pass one of the canonical format constants
// (FormatYAML, FormatTOML, FormatJSONC, FormatJSON, FormatDotenv) or any
// codec name registered in the [Codecs] set.
func WithSaveFormat(format string) SaveOption {
	return func(o *saveOptions) { o.format = format }
}

// WithSaveIncludeSecrets emits secret-tagged values verbatim. Off by
// default — only enable when the destination is private.
func WithSaveIncludeSecrets() SaveOption {
	return func(o *saveOptions) { o.includeSecrets = true }
}

// WithSaveIncludeDefaults emits keys whose only source is SetDefault. Off
// by default.
func WithSaveIncludeDefaults() SaveOption {
	return func(o *saveOptions) { o.includeDefaults = true }
}

// WithSaveOnly scopes the Save to a single sub-tree (a key prefix). Useful
// for dumping just `server.*` or `db.*` to a file.
func WithSaveOnly(prefix string) SaveOption {
	return func(o *saveOptions) { o.onlyPrefix = prefix }
}
