package recon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	rotinifs "github.com/go-rotini/fs"
)

// FSWatcher is the [WatcherFactory] backed by [rotinifs.Watcher]. It is
// the default factory installed by [New]. The zero value is unusable;
// construct via [NewFSWatcher].
type FSWatcher struct {
	debounce time.Duration
	poll     time.Duration
	logger   *slog.Logger
}

// NewFSWatcher returns an [FSWatcher] configured by opts.
func NewFSWatcher(opts ...FSWatcherOption) *FSWatcher {
	w := &FSWatcher{}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// FSWatcherOption configures an [FSWatcher].
type FSWatcherOption func(*FSWatcher)

// WithFSWatcherDebounce sets the per-path debounce window applied by the
// underlying fs.Watcher.
func WithFSWatcherDebounce(d time.Duration) FSWatcherOption {
	return func(w *FSWatcher) { w.debounce = d }
}

// WithFSWatcherPollInterval forces the fs.Watcher's polling backend at
// the supplied interval — useful when kernel notifications are
// unreliable or test timing must stay predictable.
func WithFSWatcherPollInterval(d time.Duration) FSWatcherOption {
	return func(w *FSWatcher) { w.poll = d }
}

// WithFSWatcherLogger threads a logger into the underlying fs.Watcher.
func WithFSWatcherLogger(l *slog.Logger) FSWatcherOption {
	return func(w *FSWatcher) { w.logger = l }
}

// Watch implements [WatcherFactory]. The returned channel emits a
// [SourceChange] for every observed fs event until ctx is canceled.
// Errors surface as a SourceChange with non-nil Err.
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
	// NewLazyWatcher tolerates a path that does not yet exist, which
	// matches the optional-file pattern (e.g. `.env.local`).
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

// fanFSEvents forwards fs events to the SourceChange channel. The
// emitted change has an empty Keys slice — the registry's engine
// re-reads the source rather than computing a per-key delta.
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
		case _, ok := <-sub:
			if !ok {
				return
			}
			select {
			case out <- SourceChange{}:
			case <-ctx.Done():
				return
			}
		}
	}
}
