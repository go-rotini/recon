package recon

import (
	"context"
	"slices"
	"testing"
	"time"
)

// newCtx returns a context with a 5-second deadline tied to t. The
// deadline keeps watch tests from hanging the suite when a producer
// goroutine never fires.
func newCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(t.Context(), 5*time.Second)
}

func TestMemoryBackend_PutGetList(t *testing.T) {
	b := NewInMemoryBackend()
	b.Put("k1", "v1")
	b.Put("k2", "v2")

	v, ok, err := b.Get(t.Context(), "k1")
	if err != nil || !ok || v != "v1" {
		t.Fatalf("Get(k1) v=%q ok=%v err=%v", v, ok, err)
	}

	keys, err := b.List(t.Context(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !slices.Equal(keys, []string{"k1", "k2"}) {
		t.Fatalf("List=%v, want [k1 k2]", keys)
	}
}

func TestMemoryBackend_ListPrefix(t *testing.T) {
	b := NewInMemoryBackend()
	b.PutAll(map[string]string{
		"app/port": "8080",
		"app/host": "localhost",
		"db/dsn":   "postgres://x",
	})

	keys, err := b.List(t.Context(), "app/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !slices.Equal(keys, []string{"app/host", "app/port"}) {
		t.Fatalf("List=%v", keys)
	}
}

func TestMemoryBackend_Delete(t *testing.T) {
	b := NewInMemoryBackend()
	b.Put("k", "v")
	b.Delete("k")
	if _, ok, _ := b.Get(t.Context(), "k"); ok {
		t.Fatal("Get(k) ok=true after Delete")
	}
}

func TestMemoryBackend_Watch_FiresOnPut(t *testing.T) {
	b := NewInMemoryBackend()
	ctx, cancel := newCtx(t)
	t.Cleanup(cancel)

	sub, err := b.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	b.Put("k", "v")

	select {
	case <-sub:
		// pass
	case <-time.After(time.Second):
		t.Fatal("Watch did not fire within 1s of Put")
	}
}

func TestMemoryBackend_Watch_CloseOnCtxCancel(t *testing.T) {
	b := NewInMemoryBackend()
	ctx, cancel := newCtx(t)
	sub, _ := b.Watch(ctx)
	cancel()
	select {
	case _, ok := <-sub:
		if !ok {
			return
		}
	case <-time.After(time.Second):
		t.Fatal("Watch channel not closed after ctx cancel")
	}
}

func TestMemoryBackend_Close_Idempotent(t *testing.T) {
	b := NewInMemoryBackend()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	// Operations on a closed backend are silent no-ops.
	b.Put("k", "v")
	if v, ok, _ := b.Get(t.Context(), "k"); ok {
		t.Fatalf("Put on closed backend stored %q", v)
	}
}

func TestRemoteSource_NilBackendRejected(t *testing.T) {
	_, err := NewRemoteSource("r", nil)
	if err == nil {
		t.Fatal("nil backend accepted")
	}
}

func TestRemoteSource_EmptyNameRejected(t *testing.T) {
	b := NewInMemoryBackend()
	_, err := NewRemoteSource("", b)
	if err == nil {
		t.Fatal("empty name accepted")
	}
}

func TestRemoteSource_InitialLoadPopulatesCache(t *testing.T) {
	b := NewInMemoryBackend()
	b.PutAll(map[string]string{
		"app/port": "8080",
		"app/host": "localhost",
	})

	src, err := NewRemoteSource("remote", b)
	if err != nil {
		t.Fatalf("NewRemoteSource: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	v, ok, err := src.Get(MakePath("app", "port"))
	if err != nil || !ok {
		t.Fatalf("Get(app.port) ok=%v err=%v", ok, err)
	}
	if s, _ := v.AsString(); s != "8080" {
		t.Fatalf("app.port=%q", s)
	}
}

func TestRemoteSource_PrefixFilter(t *testing.T) {
	b := NewInMemoryBackend()
	b.PutAll(map[string]string{
		"app/port": "8080",
		"db/dsn":   "x",
	})

	src, err := NewRemoteSource("remote", b, WithRemotePrefix("app/"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })

	// db/dsn must NOT surface.
	if _, ok, _ := src.Get(MakePath("db", "dsn")); ok {
		t.Fatal("db/dsn leaked through prefix filter")
	}
	if _, ok, _ := src.Get(MakePath("app", "port")); !ok {
		t.Fatal("app/port not surfaced")
	}
}

func TestRemoteSource_TrimPrefix(t *testing.T) {
	b := NewInMemoryBackend()
	b.Put("myapp/server/port", "8080")

	src, err := NewRemoteSource("remote", b,
		WithRemotePrefix("myapp/"),
		WithRemoteTrimPrefix(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })

	v, ok, _ := src.Get(MakePath("server", "port"))
	if !ok {
		t.Fatal("trimmed path not resolvable")
	}
	if s, _ := v.AsString(); s != "8080" {
		t.Fatalf("server.port=%q", s)
	}
}

func TestRemoteSource_Keys(t *testing.T) {
	b := NewInMemoryBackend()
	b.PutAll(map[string]string{
		"a/b": "1",
		"a/c": "2",
		"z":   "3",
	})
	src, _ := NewRemoteSource("r", b)
	t.Cleanup(func() { _ = src.Close() })

	keys := src.Keys()
	got := make([]string, len(keys))
	for i, p := range keys {
		got[i] = p.String()
	}
	want := []string{"a.b", "a.c", "z"}
	if !slices.Equal(got, want) {
		t.Fatalf("Keys=%v, want %v", got, want)
	}
}

func TestRemoteSource_Watch_PushPath(t *testing.T) {
	b := NewInMemoryBackend()
	b.Put("k", "before")
	src, _ := NewRemoteSource("r", b)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := newCtx(t)
	t.Cleanup(cancel)
	sub, err := src.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	b.Put("k", "after")
	select {
	case change, ok := <-sub:
		if !ok {
			t.Fatal("Watch channel closed before event")
		}
		if change.Err != nil {
			t.Fatalf("change.Err=%v", change.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("Watch did not fire within 1s")
	}

	v, _, _ := src.Get(MakePath("k"))
	if s, _ := v.AsString(); s != "after" {
		t.Fatalf("k=%q, want after", s)
	}
}

// pollOnlyBackend wraps a [MemoryBackend] in a struct that exposes
// only the [RemoteBackend] surface — not the [BackendWatcher]
// capability. Type-assertions to BackendWatcher therefore fail and
// [RemoteSource] falls back to its polling path.
type pollOnlyBackend struct{ mem *MemoryBackend }

func (p pollOnlyBackend) List(ctx context.Context, prefix string) ([]string, error) {
	return p.mem.List(ctx, prefix)
}
func (p pollOnlyBackend) Get(ctx context.Context, key string) (string, bool, error) {
	return p.mem.Get(ctx, key)
}
func (p pollOnlyBackend) Close() error { return p.mem.Close() }

// Compile-time assertion: pollOnlyBackend MUST satisfy RemoteBackend
// but NOT BackendWatcher.
var _ RemoteBackend = pollOnlyBackend{}

func TestRemoteSource_Watch_PollFallback(t *testing.T) {
	mem := NewInMemoryBackend()
	mem.Put("k", "before")
	po := pollOnlyBackend{mem: mem}

	src, err := NewRemoteSource("r", po, WithRemotePollInterval(40*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := newCtx(t)
	t.Cleanup(cancel)
	sub, err := src.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}

	mem.Put("k", "after")
	select {
	case _, ok := <-sub:
		if !ok {
			t.Fatal("poll Watch channel closed before event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("poll Watch did not fire within 2s")
	}
}

func TestRemoteSource_Watch_NoSignalSource(t *testing.T) {
	po := pollOnlyBackend{mem: NewInMemoryBackend()}
	src, _ := NewRemoteSource("r", po)
	t.Cleanup(func() { _ = src.Close() })

	sub, _ := src.Watch(t.Context())
	select {
	case _, ok := <-sub:
		if ok {
			t.Fatal("expected closed channel")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel not closed when neither watch nor poll configured")
	}
}

func TestRemoteSource_RegistryIntegration(t *testing.T) {
	b := NewInMemoryBackend()
	b.Put("app/port", "8080")

	src, err := NewRemoteSource("remote", b)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(
		WithSource(src),
		WithReloadDebounce(20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	v, ok, _ := r.Get("app.port")
	if !ok || v.String() != "8080" {
		t.Fatalf("initial app.port=%v ok=%v", v, ok)
	}

	// Trigger a backend change; the watch engine should pick it up.
	b.Put("app/port", "9090")
	select {
	case evt := <-r.Events():
		if evt.Err != nil {
			t.Fatalf("evt.Err=%v", evt.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no Event from remote backend update within 2s")
	}

	v, _, _ = r.Get("app.port")
	if v.String() != "9090" {
		t.Fatalf("post-reload app.port=%v, want 9090", v)
	}
}

func TestRemoteSource_RefreshAfterDelete(t *testing.T) {
	b := NewInMemoryBackend()
	b.Put("k", "v")
	src, _ := NewRemoteSource("r", b)
	t.Cleanup(func() { _ = src.Close() })

	if _, ok, _ := src.Get(MakePath("k")); !ok {
		t.Fatal("k not initially loaded")
	}

	b.Delete("k")
	if err := src.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, ok, _ := src.Get(MakePath("k")); ok {
		t.Fatal("k still present after delete + refresh")
	}
}

func TestMemoryBackend_Snapshot(t *testing.T) {
	b := NewInMemoryBackend()
	b.PutAll(map[string]string{
		"a": "1",
		"b": "2",
	})
	snap := b.Snapshot()
	if snap["a"] != "1" || snap["b"] != "2" {
		t.Fatalf("got %+v, want a:1 b:2", snap)
	}
	// Snapshot must be independent — mutating the returned map must
	// not affect the backend.
	snap["a"] = "999"
	if v, _, _ := b.Get(t.Context(), "a"); v != "1" {
		t.Fatalf("backend mutated via snapshot copy: got %q", v)
	}
}

func TestMemoryBackend_PutOnClosedBackendIsNoop(t *testing.T) {
	b := NewInMemoryBackend()
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Must not panic and must not change state.
	b.Put("k", "v")
	b.PutAll(map[string]string{"a": "1"})
	b.Delete("k")
	if got := b.Snapshot(); len(got) != 0 {
		t.Fatalf("closed backend accepted writes: %+v", got)
	}
}

func TestMemoryBackend_DeleteUnknownEmitsNoNotification(t *testing.T) {
	b := NewInMemoryBackend()
	ch, err := b.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	b.Delete("never-there") // no-op
	select {
	case <-ch:
		t.Fatal("Delete on unknown key emitted a notification")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestMemoryBackend_WatchOnClosedBackendReturnsClosedChan(t *testing.T) {
	b := NewInMemoryBackend()
	_ = b.Close()
	ch, err := b.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel after Close")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Watch on closed backend did not return a closed channel")
	}
}
