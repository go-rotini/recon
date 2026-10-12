package recon

// SaveOption configures one [Registry.Save], [Registry.SaveTo],
// [Registry.SaveString] or [Registry.GenerateTemplate] call.
type SaveOption func(*saveOptions)

type saveOptions struct {
	// format, when set, overrides path-based detection.
	format string

	// includeSecrets emits secret-tagged values verbatim instead of
	// redacting. Off by default; enable only when the destination is
	// private.
	includeSecrets bool

	// includeDefaults emits keys whose only source is SetDefault. Off
	// by default.
	includeDefaults bool

	// onlyPrefix scopes Save to the named sub-tree.
	onlyPrefix string
}

// WithSaveFormat forces the output format regardless of the
// destination path's extension. Pass a codec name from the registry's
// codec set, such as [FormatYAML].
func WithSaveFormat(format string) SaveOption {
	return func(o *saveOptions) { o.format = format }
}

// WithSaveIncludeSecrets emits secret-tagged values verbatim.
func WithSaveIncludeSecrets() SaveOption {
	return func(o *saveOptions) { o.includeSecrets = true }
}

// WithSaveIncludeDefaults emits keys whose only source is
// [Registry.SetDefault].
func WithSaveIncludeDefaults() SaveOption {
	return func(o *saveOptions) { o.includeDefaults = true }
}

// WithSaveOnly limits output to the keys under prefix, written relative
// to it so the document is the sub-tree on its own.
func WithSaveOnly(prefix string) SaveOption {
	return func(o *saveOptions) { o.onlyPrefix = prefix }
}
