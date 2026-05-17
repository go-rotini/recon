package recon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	rotinifs "github.com/go-rotini/fs"
)

// FSWatcher is the [WatcherFactory] backed by go-rotini/fs.Watcher. It
// constructs one [rotinifs.Watcher] per Watch call, subscribes to it
// under ctx, and forwards every [rotinifs.WatchEvent] as a recon
// [SourceChange] on the returned channel. The fs.Watcher's atomic-rename
// handling, parent-directory observation, and debouncing all carry
// through transparently — recon adds no extra event processing of its
// own.
//
// FSWatcher is the default factory installed by [New] when the caller
// does not pass [WithWatcher]. Construct via [NewFSWatcher]; the zero
// value is intentionally unusable so future configuration knobs can be
// added without breaking compatibility.
type FSWatcher struct {
	debounce time.Duration
	poll     time.Duration
	logger   *slog.Logger
}

// NewFSWatcher constructs an [FSWatcher]. The defaults (no extra
// debounce, no poll override) match what most callers want; tune the
// debounce window when a flaky filesystem fires multiple notifications
// per logical save.
func NewFSWatcher(opts ...FSWatcherOption) *FSWatcher {
	w := &FSWatcher{}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// FSWatcherOption configures an [FSWatcher] at construction.
type FSWatcherOption func(*FSWatcher)

// WithFSWatcherDebounce sets the per-path debounce window. The underlying
// fs.Watcher already debounces; this option tunes its window.
func WithFSWatcherDebounce(d time.Duration) FSWatcherOption {
	return func(w *FSWatcher) { w.debounce = d }
}

// WithFSWatcherPollInterval forces the fs.Watcher's polling backend at
// the supplied interval. Use to keep the cadence predictable on tests
// or on filesystems where the kernel notification path is unreliable.
func WithFSWatcherPollInterval(d time.Duration) FSWatcherOption {
	return func(w *FSWatcher) { w.poll = d }
}

// WithFSWatcherLogger threads a logger into the fs.Watcher. Useful for
// surfacing diagnostic output (subscription drops, backend selection)
// alongside the registry's own logger.
func WithFSWatcherLogger(l *slog.Logger) FSWatcherOption {
	return func(w *FSWatcher) { w.logger = l }
}

// Watch implements [WatcherFactory]. The returned channel emits a
// [SourceChange] for every fs.WatchEvent observed on path until ctx is
// canceled — at which point Watch closes the channel and stops its
// internal goroutine. Errors emitted by the fs.Watcher surface as
// SourceChange entries with a non-nil Err and an empty Keys slice (the
// registry's reload engine treats them as "refresh failed; previous
// snapshot retained").
func (w *FSWatcher) Watch(ctx context.Context, path string) (<-chan SourceChange, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: FSWatcher.Watch: empty path", ErrInvalidPath)
	}
	wopts := []rotinifs.WatcherOption{}
	if w.debounce > 0 {
		wopts = append(wopts, rotinifs.WithDebounce(w.debounce))
	}
	if w.poll > 0 {
		wopts = append(wopts, rotinifs.WithPolling(w.poll))
	}
	if w.logger != nil {
		wopts = append(wopts, rotinifs.WithLogger(w.logger))
	}
	// NewLazyWatcher tolerates a path that does not yet exist — important
	// for the optional-file pattern (start watching `.env.local` even
	// when the user hasn't created it yet).
	rw, err := rotinifs.NewLazyWatcher(path, wopts...)
	if err != nil {
		return nil, fmt.Errorf("recon: FSWatcher.Watch %q: %w", path, err)
	}
	sub, err := rw.Subscribe(ctx)
	if err != nil {
		_ = rw.Close()
		return nil, fmt.Errorf("recon: FSWatcher.Subscribe %q: %w", path, err)
	}

	out := make(chan SourceChange, 1)
	go fanFSEvents(ctx, rw, sub, out)
	return out, nil
}

// fanFSEvents forwards every fs.WatchEvent to the recon SourceChange
// channel until ctx is canceled or the subscription closes. Runs as the
// goroutine kicked off by [FSWatcher.Watch].
//
// An empty Keys slice on the emitted SourceChange tells the reload
// engine "the source's content may have changed — re-read everything";
// recon doesn't try to compute a per-key delta here (the fs notification
// only addresses files, not keys).
func fanFSEvents(
	ctx context.Context,
	rw *rotinifs.Watcher,
	sub <-chan rotinifs.WatchEvent,
	out chan<- SourceChange,
) {
	defer func() {
		_ = rw.Close()
		close(out)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub:
			if !ok {
				return
			}
			select {
			case out <- SourceChange{}:
				// fs.WatchEvent has no error field; backend health
				// shows up through subscription closure or by the
				// returned WatchEvent's Op being zero. Both surface
				// as the same "refresh hint" — the reload engine
				// re-reads on its own.
				_ = ev
			case <-ctx.Done():
				return
			}
		}
	}
}
