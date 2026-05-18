package recon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func TestFSWatcher_EmitsOnFileWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watched.yaml")
	if err := writeAll(path, []byte("k: v1\n")); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	w := NewFSWatcher(WithFSWatcherPollInterval(50 * time.Millisecond))
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	ch, err := w.Watch(ctx, path)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	// Give the watcher a tick to establish its baseline before the
	// rewrite — the polling backend must observe a real before→after
	// transition, not the first stat ever taken.
	time.Sleep(100 * time.Millisecond)
	if err := writeAll(path, []byte("k: v2\n")); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	select {
	case change, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before event arrived")
		}
		if change.Err != nil {
			t.Fatalf("change.Err=%v", change.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event within 2s")
	}
}

func TestFSWatcher_ClosesOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watched.yaml")
	if err := writeAll(path, []byte("k: v\n")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := NewFSWatcher(WithFSWatcherPollInterval(50 * time.Millisecond))
	ctx, cancel := context.WithCancel(t.Context())

	ch, err := w.Watch(ctx, path)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	cancel()

	// Drain the channel until it closes; allow a short grace period for
	// the goroutine to observe the cancel.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case _, ok := <-ch:
			if !ok {
				return // closed — pass
			}
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	t.Fatal("channel not closed after ctx cancel")
}

func TestFSWatcher_EmptyPathRejected(t *testing.T) {
	w := NewFSWatcher()
	_, err := w.Watch(t.Context(), "")
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err=%v, want ErrInvalidPath", err)
	}
}

func TestFSWatcher_DefaultFactoryInstalledByNew(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if r.state.opts.watcher == nil {
		t.Fatal("default factory not installed")
	}
	if _, ok := r.state.opts.watcher.(*FSWatcher); !ok {
		t.Fatalf("default factory is %T, want *FSWatcher", r.state.opts.watcher)
	}
}

func TestFSWatcher_OverrideViaWithWatcher(t *testing.T) {
	custom := NewPollWatcher(100 * time.Millisecond)
	r, err := New(WithWatcher(custom))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if r.state.opts.watcher != custom {
		t.Fatalf("WithWatcher override not applied: got %T", r.state.opts.watcher)
	}
}

func TestFSWatcher_WithDebounce(t *testing.T) {
	d := 250 * time.Millisecond
	w := NewFSWatcher(WithFSWatcherDebounce(d))
	if w.debounce != d {
		t.Fatalf("debounce = %v, want %v", w.debounce, d)
	}
}

func TestFSWatcher_WithLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	w := NewFSWatcher(WithFSWatcherLogger(logger))
	if w.logger != logger {
		t.Fatal("logger not installed by WithFSWatcherLogger")
	}
}

func TestFSWatcher_OptionsThreadIntoSubscription(t *testing.T) {
	// Construct an FSWatcher with all three options set and verify
	// Watch wires through without error. The internal go-rotini/fs
	// watcher accepts the options; this catches plumbing breaks.
	dir := t.TempDir()
	path := filepath.Join(dir, "watched.yaml")
	if err := writeAll(path, []byte("k: v\n")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	w := NewFSWatcher(
		WithFSWatcherDebounce(10*time.Millisecond),
		WithFSWatcherPollInterval(50*time.Millisecond),
		WithFSWatcherLogger(logger),
	)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	ch, err := w.Watch(ctx, path)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if ch == nil {
		t.Fatal("Watch returned nil channel")
	}
}
