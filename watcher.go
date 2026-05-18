package recon

import "context"

// WatcherFactory produces a [SourceChange] channel for a single watched
// path. Implementations must handle atomic-save sequences
// (write-temp-then-rename), debounce rapid bursts, and release resources
// when ctx is canceled.
//
// Bundled implementations: [FSWatcher] and [PollWatcher].
type WatcherFactory interface {
	Watch(ctx context.Context, path string) (<-chan SourceChange, error)
}
