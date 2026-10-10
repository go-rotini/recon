package recon

import "context"

// WatcherFactory produces a [SourceChange] channel for a single watched
// path. Implementations must handle atomic-save sequences
// (write-temp-then-rename), debounce rapid bursts, and release resources
// when ctx is canceled.
//
// Bundled implementations: [PollWatcher] here, and the
// notification-based watcher in github.com/go-rotini/recon/fswatch.
type WatcherFactory interface {
	Watch(ctx context.Context, path string) (<-chan SourceChange, error)
}
