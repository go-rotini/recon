package recon

// ErrorBehavior controls how [Registry.Bind] / [Registry.Unmarshal]
// accumulates per-field errors. [FailCollect] (the default) surfaces
// every problem at once; [FailFast] stops at the first.
type ErrorBehavior int

const (
	// FailCollect aggregates every per-field error into a [*MultiError].
	FailCollect ErrorBehavior = iota
	// FailFast stops decoding at the first per-field error.
	FailFast
)

// MergeStrategy controls how the registry combines values when
// multiple sources hold the same key. The default [MergeShadow]
// replaces lower-precedence values entirely; [MergeAppend] enables
// slice-and-map deep merge.
type MergeStrategy int

const (
	// MergeShadow has the higher-precedence source replace the lower
	// in its entirety. The default.
	MergeShadow MergeStrategy = iota
	// MergeAppend concatenates slices and deep-merges maps; scalars
	// still shadow.
	MergeAppend
	// MergeReplace is an explicit alias for [MergeShadow].
	MergeReplace
)
