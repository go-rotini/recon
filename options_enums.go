package recon

// ErrorBehavior controls how the decoder accumulates per-field
// errors during a [Registry.Bind] / [Registry.Unmarshal]. The
// default is [FailCollect], which surfaces every problem at once;
// [FailFast] stops at the first.
//
// Choose FailFast when you're driving Bind from a request handler
// where a single bad field already invalidates the whole request;
// stick with FailCollect for boot-time config where surfacing every
// problem at once is more helpful than the first one.
type ErrorBehavior int

// ErrorBehavior values.
const (
	// FailCollect aggregates every per-field error into a [*MultiError]
	// so handlers can surface all problems at once. This is the default.
	FailCollect ErrorBehavior = iota
	// FailFast stops decoding at the first per-field error.
	FailFast
)

// MergeStrategy controls how the registry combines values when
// multiple sources hold the same key. The default — [MergeShadow] —
// replaces lower-precedence values entirely; deep-merge is opt-in
// via [MergeAppend].
//
// MergeShadow gives you the principle-of-least-surprise behavior:
// "the highest-precedence source wins." MergeAppend is for the
// "I want my flag-supplied tag list to extend, not replace, the
// config-file list" use case — pick it deliberately and document
// it in your app's config docs.
type MergeStrategy int

// MergeStrategy values.
const (
	// MergeShadow has the higher-precedence source replace the lower's
	// value in its entirety. No structural merging of maps or slices.
	// This is the default.
	MergeShadow MergeStrategy = iota
	// MergeAppend appends slices and deep-merges maps; scalar values
	// still shadow.
	MergeAppend
	// MergeReplace is an explicit alias for [MergeShadow].
	MergeReplace
)
