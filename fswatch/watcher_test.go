package fswatch

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-rotini/recon"
)

func TestWatcher_EmitsOnFileWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watched.yaml")
	if err := writeAll(path, []byte("k: v1\n")); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	w := New(WithPollInterval(50 * time.Millisecond))
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

func TestWatcher_ClosesOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watched.yaml")
	if err := writeAll(path, []byte("k: v\n")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := New(WithPollInterval(50 * time.Millisecond))
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

func TestWatcher_EmptyPathRejected(t *testing.T) {
	w := New()
	_, err := w.Watch(t.Context(), "")
	if !errors.Is(err, recon.ErrInvalidPath) {
		t.Fatalf("err=%v, want ErrInvalidPath", err)
	}
}

func TestWatcher_WithDebounce(t *testing.T) {
	d := 250 * time.Millisecond
	w := New(WithDebounce(d))
	if w.debounce != d {
		t.Fatalf("debounce = %v, want %v", w.debounce, d)
	}
}

func TestWatcher_WithLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	w := New(WithLogger(logger))
	if w.logger != logger {
		t.Fatal("logger not installed by WithLogger")
	}
}

func TestWatcher_OptionsThreadIntoSubscription(t *testing.T) {
	// Construct a Watcher with all three options set and verify
	// Watch wires through without error. The internal go-rotini/fs
	// watcher accepts the options; this catches plumbing breaks.
	dir := t.TempDir()
	path := filepath.Join(dir, "watched.yaml")
	if err := writeAll(path, []byte("k: v\n")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	w := New(
		WithDebounce(10*time.Millisecond),
		WithPollInterval(50*time.Millisecond),
		WithLogger(logger),
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

// TestWatcher_DrivesRegistryReload wires the watcher into a registry and
// checks that rewriting a watched file produces a reload event.
func TestWatcher_DrivesRegistryReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := writeAll(path, []byte("k: v1\n")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	src, err := recon.NewFileSource(path)
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	r, err := recon.New(
		recon.WithWatcher(New(WithPollInterval(20*time.Millisecond))),
		recon.WithReloadDebounce(10*time.Millisecond),
		recon.WithSource(src),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	time.Sleep(100 * time.Millisecond)
	if err := writeAll(path, []byte("k: v2\n")); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	select {
	case evt := <-r.Events():
		if evt.Err != nil {
			t.Fatalf("event err: %v", evt.Err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no reload event within 3s")
	}
	got, _, err := r.GetString("k")
	if err != nil {
		t.Fatalf("GetString: %v", err)
	}
	if got != "v2" {
		t.Errorf("k = %q, want v2", got)
	}
}

func writeAll(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
