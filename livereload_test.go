package recon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

// TestLiveReload_FileSourceEndToEnd is the §8.4 live-reload harness
// scenario, in its compact form: create a registry with a watched file
// source, atomic-rewrite the file, assert a SourceChange arrives, and
// assert the registry's Get returns the new value after invoking the
// reload.
//
// Phase 5 wires the watcher backend into FileSource.Watch but stops
// short of the full watch engine — there's no automatic registry
// reload on every event yet (that's Phase 8). The test therefore
// drives the reload manually once the SourceChange is observed.
func TestLiveReload_FileSourceEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watched.yaml")
	if err := writeAll(path, []byte("k: before\n")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	src, err := YAMLSource(path)
	if err != nil {
		t.Fatalf("YAMLSource: %v", err)
	}

	// Use PollWatcher to keep the test independent of fs.Watcher's
	// debounce defaults — the assertion shape stays the same either way.
	r, err := New(
		WithWatcher(NewPollWatcher(40*time.Millisecond)),
		WithSource(src),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	v, ok, _ := r.Get("k")
	if !ok {
		t.Fatal("initial k not set")
	}
	if s, _ := v.AsString(); s != "before" {
		t.Fatalf("initial k=%q, want before", s)
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	ch, err := src.(*FileSource).Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // baseline

	// Atomic rewrite (write-temp + rename) so the watcher observes a
	// single complete content swap.
	tmp := path + ".tmp"
	if err := writeAll(tmp, []byte("k: after\n")); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// An Event should arrive within the deadline.
	select {
	case change, ok := <-ch:
		if !ok {
			t.Fatal("watch channel closed before event")
		}
		if change.Err != nil {
			t.Fatalf("change.Err=%v", change.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no live-reload event within 2s")
	}

	// FileSource.Reload was driven by the fan goroutine; rebuild the
	// registry snapshot so the new value surfaces through Get.
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	v, _, _ = r.Get("k")
	if s, _ := v.AsString(); s != "after" {
		t.Fatalf("post-reload k=%q, want after", s)
	}
}

// TestLiveReload_VirtualFSImmutable confirms that [FileSourceFS] holds
// the bytes it was constructed with — virtual-FS live reload is a
// Phase-8+ concern, so a mutation to the underlying fs.FS after
// construction is intentionally NOT visible via the registry.
func TestLiveReload_VirtualFSImmutable(t *testing.T) {
	fsys := fstest.MapFS{
		"config.yaml": &fstest.MapFile{Data: []byte("k: v1\n")},
	}
	src, err := NewFileSourceFS("vfs", fsys, "config.yaml")
	if err != nil {
		t.Fatalf("NewFileSourceFS: %v", err)
	}
	r := newRegistry(t, WithSource(src))
	if s, _, _ := r.GetString("k"); s != "v1" {
		t.Fatalf("initial k=%q", s)
	}

	fsys["config.yaml"] = &fstest.MapFile{Data: []byte("k: v2\n")}
	_ = r.Reload()
	if s, _, _ := r.GetString("k"); s != "v1" {
		t.Fatalf("FileSourceFS unexpectedly re-read: k=%q", s)
	}
}

// TestLiveReload_AutomaticReloadViaWatchEngine is the Phase-8
// strengthening of the harness: with the watch engine wired,
// modifying a watched file MUST update the registry without any
// manual r.Reload() call. The test asserts that Get returns the new
// value purely as a consequence of the file change.
func TestLiveReload_AutomaticReloadViaWatchEngine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watched.yaml")
	if err := writeAll(path, []byte("k: before\n")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	src, err := YAMLSource(path)
	if err != nil {
		t.Fatalf("YAMLSource: %v", err)
	}
	r, err := New(
		WithWatcher(NewPollWatcher(40*time.Millisecond)),
		WithReloadDebounce(30*time.Millisecond),
		WithSource(src),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if s, _, _ := r.GetString("k"); s != "before" {
		t.Fatalf("initial k=%q, want before", s)
	}

	time.Sleep(100 * time.Millisecond) // watcher baseline
	tmp := path + ".tmp"
	if err := writeAll(tmp, []byte("k: after\n")); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Wait for the Event the engine emits.
	select {
	case evt, ok := <-r.Events():
		if !ok {
			t.Fatal("Events channel closed")
		}
		if evt.Err != nil {
			t.Fatalf("evt.Err=%v", evt.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch engine did not emit a reload event within 2s")
	}

	// And — crucially — Get must observe the new value WITHOUT
	// having to call r.Reload().
	if s, _, _ := r.GetString("k"); s != "after" {
		t.Fatalf("post-auto-reload k=%q, want after", s)
	}
}

// TestLiveReload_WatcherInjectedByRegistry verifies that the registry
// threads its [WatcherFactory] into newly-added [FileSource] instances
// that did not get a per-source factory of their own.
func TestLiveReload_WatcherInjectedByRegistry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := writeAll(path, []byte("k: v\n")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	src, err := YAMLSource(path)
	if err != nil {
		t.Fatalf("YAMLSource: %v", err)
	}
	fileSrc := src.(*FileSource)
	if fileSrc.watcher != nil {
		t.Fatal("FileSource pre-construction has a watcher set; should be nil")
	}

	custom := NewPollWatcher(50 * time.Millisecond)
	r, err := New(WithWatcher(custom), WithSource(src))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if fileSrc.watcher != custom {
		t.Fatalf("watcher not injected: got %T, want *PollWatcher", fileSrc.watcher)
	}
}
