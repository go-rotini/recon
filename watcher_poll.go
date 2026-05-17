package recon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"time"
)

// PollWatcher is the stdlib-only [WatcherFactory] fallback. It periodically
// stats the watched path and emits a [SourceChange] when the file's
// mtime, size, or SHA-256 digest changes since the previous tick.
//
// Use PollWatcher when:
//   - the runtime doesn't have a usable native filesystem-notification
//     backend (network mounts, container overlays);
//   - tests want fully-deterministic event timing without leaning on
//     go-rotini/fs's debouncer;
//   - you'd rather not depend on fs.Watcher at all.
//
// The digest check exists because mtime alone is unreliable on some
// platforms (sub-second resolution; touch-without-change). It runs only
// when stat reports a same-size file with a stale mtime — the common
// case of "file rewritten, new size" never opens the file.
type PollWatcher struct {
	interval time.Duration
}

// NewPollWatcher constructs a [PollWatcher] that fires at interval. An
// interval ≤ 0 is clamped to the default (1 second).
func NewPollWatcher(interval time.Duration) *PollWatcher {
	if interval <= 0 {
		interval = time.Second
	}
	return &PollWatcher{interval: interval}
}

// Watch implements [WatcherFactory]. The first tick fires at
// time.Now()+interval; the channel is closed when ctx is canceled. A
// stat error after a previously-successful read surfaces as a
// SourceChange with a non-nil Err so the reload engine can decide
// whether to retain the previous snapshot.
func (w *PollWatcher) Watch(ctx context.Context, path string) (<-chan SourceChange, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: PollWatcher.Watch: empty path", ErrInvalidPath)
	}
	out := make(chan SourceChange, 1)
	go w.poll(ctx, path, out)
	return out, nil
}

// poll is the goroutine body for [PollWatcher.Watch]. It samples the
// path's stat + digest at each interval and emits a SourceChange when
// the sample differs from the previous one.
func (w *PollWatcher) poll(ctx context.Context, path string, out chan<- SourceChange) {
	defer close(out)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// First sample establishes the baseline. A stat error here is
	// silently absorbed — the first real tick will surface the same
	// condition through the regular error path.
	prev, baseErr := pollSnap(path)
	_ = baseErr
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur, err := pollSnap(path)
			if err != nil {
				// Emit an error change once; the reload engine
				// decides what to do (retain previous snapshot).
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

// pollSample captures everything the poll loop needs to decide whether a
// file has changed since the last tick. Stat-derived fields are cheap;
// the digest is computed lazily only when stat alone is ambiguous.
type pollSample struct {
	exists bool
	size   int64
	mtime  time.Time
	digest [sha256.Size]byte
	hashed bool
}

// equal reports whether two samples represent the same file content as
// far as the poll loop can tell. A missing-to-present transition (or
// vice versa) always counts as a change; otherwise size + mtime + digest
// must match.
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

// pollSnap captures the current pollSample for path. A missing file is
// not an error; "not present" is a valid sample value.
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
	// the change detector. Skip the digest for files larger than 1 MiB —
	// at that size the size+mtime signal is reliable enough.
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
