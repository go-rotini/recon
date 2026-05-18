package recon

import "context"

// WatcherFactory produces a [SourceChange] channel for a single watched
// path. The registry hands a WatcherFactory to file-backed sources at
// construction; the source calls Watch(ctx, path) and forwards every
// emitted [SourceChange] to the registry's reload engine.
//
// Implementations are responsible for:
//   - atomic-save handling (write-temp-then-rename),
//   - debouncing rapid event bursts (most file systems emit several events
//     for one logical save),
//   - clean shutdown when ctx is canceled (close the channel, stop any
//     goroutines, release file descriptors).
//
// The bundled implementations are [FSWatcher] (backed by
// go-rotini/fs) and [PollWatcher] (stdlib-only periodic poll). Both
// satisfy this interface; users provide their own by implementing
// the same one-method shape.
type WatcherFactory interface {
	Watch(ctx context.Context, path string) (<-chan SourceChange, error)
}
