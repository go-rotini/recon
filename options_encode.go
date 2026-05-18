package recon

// SaveOption configures one [Registry.Save] / [Registry.SaveTo] /
// [Registry.GenerateTemplate] call. Distinct from [Option] because
// Save is per-call: each invocation can pick a different output
// format, secret policy, or sub-tree.
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
// destination path's extension. Pass one of the canonical [Format*]
// constants or any registered codec name.
func WithSaveFormat(format string) SaveOption {
	return func(o *saveOptions) { o.format = format }
}

// WithSaveIncludeSecrets emits secret-tagged values verbatim.
func WithSaveIncludeSecrets() SaveOption {
	return func(o *saveOptions) { o.includeSecrets = true }
}

// WithSaveIncludeDefaults emits keys whose only source is SetDefault.
func WithSaveIncludeDefaults() SaveOption {
	return func(o *saveOptions) { o.includeDefaults = true }
}

// WithSaveOnly scopes Save to a single key prefix.
func WithSaveOnly(prefix string) SaveOption {
	return func(o *saveOptions) { o.onlyPrefix = prefix }
}
