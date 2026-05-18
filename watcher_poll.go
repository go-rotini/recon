package recon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"time"
)

// PollWatcher is the stdlib-only [WatcherFactory] fallback. It stats the
// watched path on a tick and emits a [SourceChange] when size, mtime, or
// SHA-256 digest differs from the previous sample. Use it when a native
// fs-notification backend is unavailable or undesirable.
type PollWatcher struct {
	interval time.Duration
}

// NewPollWatcher returns a [PollWatcher] that ticks at interval. An
// interval ≤ 0 is clamped to one second.
func NewPollWatcher(interval time.Duration) *PollWatcher {
	if interval <= 0 {
		interval = time.Second
	}
	return &PollWatcher{interval: interval}
}

// Watch implements [WatcherFactory]. The first tick fires after
// interval; the channel is closed when ctx is canceled. Stat errors are
// surfaced as a [SourceChange] with non-nil Err.
func (w *PollWatcher) Watch(ctx context.Context, path string) (<-chan SourceChange, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: PollWatcher.Watch: empty path", ErrInvalidPath)
	}
	out := make(chan SourceChange, 1)
	go w.poll(ctx, path, out)
	return out, nil
}

func (w *PollWatcher) poll(ctx context.Context, path string, out chan<- SourceChange) {
	defer close(out)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// First sample establishes the baseline. A stat error here is
	// absorbed; the next real tick will surface the same condition.
	prev, baseErr := pollSnap(path)
	_ = baseErr
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur, err := pollSnap(path)
			if err != nil {
				select {
				case out <- SourceChange{Err: err}:
				case <-ctx.Done():
					return
				}
				continue
			}
			if !cur.equal(prev) {
				prev = cur
				select {
				case out <- SourceChange{}:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// pollSample captures the per-tick file state. Digest is computed only
// for small files (see pollSnap) so the common "rewritten, new size"
// case never opens the file.
type pollSample struct {
	exists bool
	size   int64
	mtime  time.Time
	digest [sha256.Size]byte
	hashed bool
}

func (s pollSample) equal(o pollSample) bool {
	if s.exists != o.exists {
		return false
	}
	if !s.exists {
		return true
	}
	if s.size != o.size {
		return false
	}
	if !s.mtime.Equal(o.mtime) {
		return false
	}
	if s.hashed && o.hashed && s.digest != o.digest {
		return false
	}
	return true
}

// pollSnap returns the current [pollSample] for path. A missing file is
// reported as a valid {exists: false} sample, not an error.
func pollSnap(path string) (pollSample, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return pollSample{exists: false}, nil
		}
		return pollSample{}, fmt.Errorf("recon: PollWatcher stat %q: %w", path, err)
	}
	s := pollSample{
		exists: true,
		size:   info.Size(),
		mtime:  info.ModTime(),
	}
	// Hash small files so a same-size, same-mtime rewrite still trips
	// the change detector. Skip above 1 MiB where size+mtime is
	// sufficient.
	const hashCutoff = 1 << 20
	if info.Size() <= hashCutoff {
		data, err := os.ReadFile(path)
		if err == nil {
			s.digest = sha256.Sum256(data)
			s.hashed = true
		}
	}
	return s, nil
}
