package fswatch

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	rotinifs "github.com/go-rotini/fs"
	"github.com/go-rotini/recon"
)

// Watcher is a [recon.WatcherFactory] backed by go-rotini/fs's watcher:
// atomic-save aware, debounced, with kernel notifications where the
// platform has them and polling elsewhere. Install it with
// [recon.WithWatcher] or [recon.WithFileWatcher]. Construct via [New].
type Watcher struct {
	debounce time.Duration
	poll     time.Duration
	logger   *slog.Logger
}

// New returns a [Watcher] configured by opts.
func New(opts ...Option) *Watcher {
	w := &Watcher{}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Option configures a [Watcher].
type Option func(*Watcher)

// WithDebounce sets the per-path debounce window applied by the
// underlying fs.Watcher.
func WithDebounce(d time.Duration) Option {
	return func(w *Watcher) { w.debounce = d }
}

// WithPollInterval forces the fs.Watcher's polling backend at the
// supplied interval — useful when kernel notifications are unreliable
// or test timing must stay predictable.
func WithPollInterval(d time.Duration) Option {
	return func(w *Watcher) { w.poll = d }
}

// WithLogger threads a logger into the underlying fs.Watcher.
func WithLogger(l *slog.Logger) Option {
	return func(w *Watcher) { w.logger = l }
}

// Watch implements [recon.WatcherFactory]. The returned channel emits a
// [recon.SourceChange] for every observed fs event until ctx is
// canceled.
func (w *Watcher) Watch(ctx context.Context, path string) (<-chan recon.SourceChange, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: fswatch: empty path", recon.ErrInvalidPath)
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
		return nil, fmt.Errorf("fswatch: watch %q: %w", path, err)
	}
	sub, err := rw.Subscribe(ctx)
	if err != nil {
		_ = rw.Close()
		return nil, fmt.Errorf("fswatch: subscribe %q: %w", path, err)
	}

	out := make(chan recon.SourceChange, 1)
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
	out chan<- recon.SourceChange,
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
			case out <- recon.SourceChange{}:
			case <-ctx.Done():
				return
			}
		}
	}
}
