package recon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPollWatcher_EmitsOnContentChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "poll.yaml")
	if err := writeAll(path, []byte("k: v1\n")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := NewPollWatcher(50 * time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	ch, err := w.Watch(ctx, path)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	// Let the watcher take its baseline sample.
	time.Sleep(120 * time.Millisecond)
	if err := writeAll(path, []byte("k: v2-different\n")); err != nil {
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
		t.Fatal("PollWatcher did not emit within 2s")
	}
}

func TestPollWatcher_NoEventWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "poll.yaml")
	if err := writeAll(path, []byte("k: v\n")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := NewPollWatcher(40 * time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	ch, err := w.Watch(ctx, path)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	// Wait through several poll intervals; no rewrite means no event.
	select {
	case change := <-ch:
		t.Fatalf("unexpected event for unchanged file: %+v", change)
	case <-time.After(300 * time.Millisecond):
		// expected — no event
	}
}

func TestPollWatcher_DetectsCreateAndDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lazy.yaml")
	// File starts missing.

	w := NewPollWatcher(40 * time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	ch, err := w.Watch(ctx, path)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	// Create the file — should trip the change detector.
	if err := writeAll(path, []byte("k: v\n")); err != nil {
		t.Fatalf("create: %v", err)
	}

	select {
	case <-ch:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("create event not emitted")
	}
}

func TestPollWatcher_RejectsEmptyPath(t *testing.T) {
	w := NewPollWatcher(time.Second)
	_, err := w.Watch(t.Context(), "")
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err=%v, want ErrInvalidPath", err)
	}
}

func TestPollWatcher_IntervalClamping(t *testing.T) {
	// Negative / zero intervals clamp to the 1s default; the watcher
	// still constructs without panicking.
	w := NewPollWatcher(-time.Second)
	if w.interval <= 0 {
		t.Fatalf("interval=%v not clamped", w.interval)
	}
}
