package recon

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// newRegistry is a tiny constructor used throughout the registry tests.
// Failing to construct is a programmer error in the test, not a runtime
// condition we want to assert on — so we t.Fatal on any unexpected error.
func newRegistry(t *testing.T, opts ...Option) *Registry {
	t.Helper()
	r, err := New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestRegistry_New_Empty(t *testing.T) {
	r := newRegistry(t)
	if r.Snapshot() == nil {
		t.Fatal("initial snapshot is nil")
	}
	if got := r.AllKeys(); len(got) != 0 {
		t.Fatalf("AllKeys() = %v, want empty", got)
	}
	if got := r.Sources(); len(got) != 0 {
		t.Fatalf("Sources() = %v, want empty", got)
	}
}

func TestRegistry_SetAndGet(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("server.port", 8080); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, ok, err := r.Get("server.port")
	if err != nil || !ok {
		t.Fatalf("Get(server.port) ok=%v err=%v", ok, err)
	}
	i, _ := v.AsInt64()
	if i != 8080 {
		t.Fatalf("port=%d, want 8080", i)
	}
	if v.Source() != srcExplicit {
		t.Fatalf("Source()=%q, want %q", v.Source(), srcExplicit)
	}
}

func TestRegistry_Set_NilClears(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("k", "v")
	_ = r.Set("k", nil)
	if r.IsSet("k") {
		t.Fatal("IsSet(k) true after Set(nil)")
	}
}

func TestRegistry_Unset(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("k", "v")
	if err := r.Unset("k"); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	if r.IsSet("k") {
		t.Fatal("IsSet(k) true after Unset")
	}
	// Idempotent.
	if err := r.Unset("nope"); err != nil {
		t.Fatalf("Unset of unset key: %v", err)
	}
}

func TestRegistry_Defaults(t *testing.T) {
	r := newRegistry(t)
	if err := r.SetDefault("server.port", 3000); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	v, ok, _ := r.Get("server.port")
	if !ok {
		t.Fatal("default not visible via Get")
	}
	if v.Source() != srcDefault {
		t.Fatalf("Source()=%q, want %q", v.Source(), srcDefault)
	}
	i, _ := v.AsInt64()
	if i != 3000 {
		t.Fatalf("default=%d, want 3000", i)
	}
}

func TestRegistry_AddRemoveSource(t *testing.T) {
	r := newRegistry(t)
	src := NewMapSource("file", map[string]any{"a": 1})
	if err := r.AddSource(src); err != nil {
		t.Fatalf("AddSource: %v", err)
	}
	if names := r.Sources(); !slices.Equal(names, []string{"file"}) {
		t.Fatalf("Sources()=%v, want [file]", names)
	}
	v, ok, _ := r.Get("a")
	if !ok {
		t.Fatal("a not visible after AddSource")
	}
	if v.Source() != "file" {
		t.Fatalf("a.Source()=%q, want %q", v.Source(), "file")
	}

	if err := r.RemoveSource("file"); err != nil {
		t.Fatalf("RemoveSource: %v", err)
	}
	if r.IsSet("a") {
		t.Fatal("a still present after RemoveSource")
	}
	// Removing an unknown source is a no-op.
	if err := r.RemoveSource("nope"); err != nil {
		t.Fatalf("RemoveSource(nope): %v", err)
	}
}

func TestRegistry_AddSource_RejectsReservedName(t *testing.T) {
	r := newRegistry(t)
	for _, name := range []string{srcExplicit, srcDefault} {
		err := r.AddSource(NewMapSource(name, nil))
		if !errors.Is(err, ErrSourceConflict) {
			t.Fatalf("AddSource(%q): err=%v, want ErrSourceConflict", name, err)
		}
	}
}

func TestRegistry_AddSource_RejectsDuplicate(t *testing.T) {
	r := newRegistry(t)
	if err := r.AddSource(NewMapSource("a", nil)); err != nil {
		t.Fatalf("first AddSource: %v", err)
	}
	err := r.AddSource(NewMapSource("a", nil))
	if !errors.Is(err, ErrSourceConflict) {
		t.Fatalf("second AddSource: err=%v, want ErrSourceConflict", err)
	}
}

func TestRegistry_AddSource_NilRejected(t *testing.T) {
	r := newRegistry(t)
	err := r.AddSource(nil)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("AddSource(nil): err=%v, want ErrInvalidPath", err)
	}
}

func TestRegistry_InsertSource_Precedence(t *testing.T) {
	r := newRegistry(t)
	low := NewMapSource("low", map[string]any{"k": "low"})
	high := NewMapSource("high", map[string]any{"k": "high"})

	if err := r.AddSource(low); err != nil {
		t.Fatal(err)
	}
	if err := r.InsertSource(0, high); err != nil {
		t.Fatal(err)
	}

	v, _, _ := r.Get("k")
	s, _ := v.AsString()
	if s != "high" {
		t.Fatalf("k=%q, want %q (high should win)", s, "high")
	}
	if names := r.Sources(); !slices.Equal(names, []string{"high", "low"}) {
		t.Fatalf("Sources()=%v", names)
	}
}

func TestRegistry_WithPrecedence(t *testing.T) {
	a := NewMapSource("a", map[string]any{"k": "a"})
	b := NewMapSource("b", map[string]any{"k": "b"})
	c := NewMapSource("c", map[string]any{"k": "c"})

	r, err := New(WithSources(a, b, c), WithPrecedence("c", "a"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if names := r.Sources(); !slices.Equal(names, []string{"c", "a", "b"}) {
		t.Fatalf("Sources()=%v, want [c a b]", names)
	}
	v, _, _ := r.Get("k")
	s, _ := v.AsString()
	if s != "c" {
		t.Fatalf("k=%q, want %q", s, "c")
	}
}

func TestRegistry_PinSource(t *testing.T) {
	high := NewMapSource("high", map[string]any{"k": "high"})
	low := NewMapSource("low", map[string]any{"k": "low"})
	r := newRegistry(t, WithSources(high, low))

	if err := r.PinSource("k", "low"); err != nil {
		t.Fatalf("PinSource: %v", err)
	}
	v, _, _ := r.Get("k")
	s, _ := v.AsString()
	if s != "low" {
		t.Fatalf("pinned k=%q, want %q", s, "low")
	}

	if err := r.Unpin("k"); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	v, _, _ = r.Get("k")
	s, _ = v.AsString()
	if s != "high" {
		t.Fatalf("after Unpin k=%q, want %q", s, "high")
	}
}

func TestRegistry_PinSource_UnknownSource(t *testing.T) {
	r := newRegistry(t)
	err := r.PinSource("k", "nope")
	if !errors.Is(err, ErrSourceConflict) {
		t.Fatalf("err=%v, want ErrSourceConflict", err)
	}
	var srcErr *SourceError
	if !errors.As(err, &srcErr) {
		t.Fatalf("err=%v, want *SourceError", err)
	}
}

func TestRegistry_RegisterAlias(t *testing.T) {
	r := newRegistry(t)
	_ = r.SetDefault("server.port", 8080)
	if err := r.RegisterAlias("port", "server.port"); err != nil {
		t.Fatalf("RegisterAlias: %v", err)
	}
	v, ok, _ := r.Get("port")
	if !ok {
		t.Fatal("alias not resolvable")
	}
	i, _ := v.AsInt64()
	if i != 8080 {
		t.Fatalf("alias port=%d, want 8080", i)
	}
}

func TestRegistry_RegisterAlias_CycleRejected(t *testing.T) {
	r := newRegistry(t)
	if err := r.RegisterAlias("a", "b"); err != nil {
		t.Fatalf("a→b: %v", err)
	}
	if err := r.RegisterAlias("b", "c"); err != nil {
		t.Fatalf("b→c: %v", err)
	}
	err := r.RegisterAlias("c", "a")
	if !errors.Is(err, ErrAliasCycle) {
		t.Fatalf("cycle err=%v, want ErrAliasCycle", err)
	}
	var cyc *AliasCycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("err=%v, want *AliasCycleError", err)
	}
	// Registry left intact — the original aliases should still work.
	_ = r.Set("c", "x")
	v, ok, _ := r.Get("a")
	if !ok {
		t.Fatal("a→b→c chain broken after cycle rejection")
	}
	s, _ := v.AsString()
	if s != "x" {
		t.Fatalf("a→...→c=%q, want %q", s, "x")
	}
}

func TestRegistry_AllKeysAndIsSet(t *testing.T) {
	r := newRegistry(t)
	src := NewMapSource("m", map[string]any{
		"a": 1,
		"b": map[string]any{"c": 2},
	})
	_ = r.AddSource(src)
	_ = r.Set("explicit", "x")
	_ = r.SetDefault("d", "default")

	keys := r.AllKeys()
	want := []string{"a", "b.c", "d", "explicit"}
	if !slices.Equal(keys, want) {
		t.Fatalf("AllKeys()=%v, want %v", keys, want)
	}

	for _, k := range []string{"a", "b.c", "d", "explicit"} {
		if !r.IsSet(k) {
			t.Fatalf("IsSet(%q) false", k)
		}
	}
	if r.IsSet("missing") {
		t.Fatal("IsSet(missing) true")
	}
}

func TestRegistry_Reload(t *testing.T) {
	src := NewMapSource("m", map[string]any{"k": 1})
	r := newRegistry(t, WithSource(src))
	src.Replace(map[string]any{"k": 2})

	// Snapshot was taken at construction — until Reload it still says 1.
	v, _, _ := r.Get("k")
	if i, _ := v.AsInt64(); i != 1 {
		t.Fatalf("pre-reload k=%d, want 1", i)
	}

	if err := r.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	v, _, _ = r.Get("k")
	if i, _ := v.AsInt64(); i != 2 {
		t.Fatalf("post-reload k=%d, want 2", i)
	}
}

func TestRegistry_ReloadContext_Canceled(t *testing.T) {
	r := newRegistry(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.ReloadContext(ctx)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want wrap of context.Canceled", err)
	}
}

func TestRegistry_ReloadContext_NilContext(t *testing.T) {
	r := newRegistry(t)
	//nolint:SA1012 // explicitly verifying the nil-context guard
	err := r.ReloadContext(nil)
	if !errors.Is(err, ErrNilContext) {
		t.Fatalf("err=%v, want ErrNilContext", err)
	}
}

func TestRegistry_Close_BlocksOperations(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Idempotent.
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	for _, fn := range []func() error{
		func() error { return r.Set("k", "v") },
		func() error { return r.SetDefault("k", "v") },
		func() error { return r.Unset("k") },
		func() error { return r.RegisterAlias("a", "b") },
		func() error { return r.AddSource(NewMapSource("m", nil)) },
		func() error { return r.Reload() },
	} {
		if err := fn(); !errors.Is(err, ErrRegistryClosed) {
			t.Fatalf("expected ErrRegistryClosed, got %v", err)
		}
	}
	// Reads return zero / false after Close (no panic).
	if r.IsSet("anything") {
		t.Fatal("IsSet true on closed registry")
	}
	if got := r.AllKeys(); got != nil {
		t.Fatalf("AllKeys()=%v on closed registry", got)
	}
}

func TestRegistry_Close_PropagatesSourceErrors(t *testing.T) {
	bad := &errCloseSource{name: "boom", err: errors.New("close failed")}
	r, err := New(WithSource(bad))
	if err != nil {
		t.Fatal(err)
	}
	closeErr := r.Close()
	var multi *MultiError
	if !errors.As(closeErr, &multi) {
		t.Fatalf("Close err=%v, want *MultiError", closeErr)
	}
	if len(multi.Errors) == 0 {
		t.Fatal("MultiError is empty")
	}
}

// errCloseSource is a minimal Source for the close-error test.
type errCloseSource struct {
	name string
	err  error
}

func (s *errCloseSource) Name() string                  { return s.name }
func (s *errCloseSource) Get(Path) (Value, bool, error) { return Value{}, false, nil }
func (s *errCloseSource) Keys() []Path                  { return nil }
func (s *errCloseSource) Close() error                  { return s.err }

func TestRegistry_GeneticGet(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("port", 8080)
	_ = r.Set("name", "rotini")
	_ = r.Set("debug", true)
	_ = r.Set("rate", 1.5)
	_ = r.Set("timeout", 5*time.Second)

	port, ok, err := Get[int](r, "port")
	if err != nil || !ok || port != 8080 {
		t.Fatalf("Get[int](port) = %d ok=%v err=%v", port, ok, err)
	}
	name, ok, err := Get[string](r, "name")
	if err != nil || !ok || name != "rotini" {
		t.Fatalf("Get[string](name) = %q ok=%v err=%v", name, ok, err)
	}
	dbg, ok, err := Get[bool](r, "debug")
	if err != nil || !ok || !dbg {
		t.Fatalf("Get[bool](debug) = %v ok=%v err=%v", dbg, ok, err)
	}
	rate, ok, err := Get[float64](r, "rate")
	if err != nil || !ok || rate != 1.5 {
		t.Fatalf("Get[float64](rate) = %v ok=%v err=%v", rate, ok, err)
	}
	to, ok, err := Get[time.Duration](r, "timeout")
	if err != nil || !ok || to != 5*time.Second {
		t.Fatalf("Get[time.Duration](timeout) = %v ok=%v err=%v", to, ok, err)
	}
}

func TestRegistry_MustGet(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("k", 42)
	if got := MustGet[int](r, "k"); got != 42 {
		t.Fatalf("MustGet=%d", got)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("MustGet on missing key did not panic")
		}
	}()
	MustGet[int](r, "missing")
}

func TestRegistry_TypedAccessors(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("s", "hi")
	_ = r.Set("i", 1)
	_ = r.Set("f", 2.5)
	_ = r.Set("b", true)
	_ = r.Set("d", 3*time.Second)

	s, ok, err := r.GetString("s")
	if err != nil || !ok || s != "hi" {
		t.Fatalf("GetString=%q ok=%v err=%v", s, ok, err)
	}
	i, ok, err := r.GetInt("i")
	if err != nil || !ok || i != 1 {
		t.Fatalf("GetInt=%d ok=%v err=%v", i, ok, err)
	}
	f, ok, err := r.GetFloat("f")
	if err != nil || !ok || f != 2.5 {
		t.Fatalf("GetFloat=%v ok=%v err=%v", f, ok, err)
	}
	b, ok, err := r.GetBool("b")
	if err != nil || !ok || !b {
		t.Fatalf("GetBool=%v ok=%v err=%v", b, ok, err)
	}
	d, ok, err := r.GetDuration("d")
	if err != nil || !ok || d != 3*time.Second {
		t.Fatalf("GetDuration=%v ok=%v err=%v", d, ok, err)
	}
}

func TestRegistry_SubView_PrefixedReadWrite(t *testing.T) {
	r := newRegistry(t)
	src := NewMapSource("m", map[string]any{
		"server": map[string]any{"port": 8080, "host": "localhost"},
		"db":     map[string]any{"dsn": "postgres://"},
	})
	_ = r.AddSource(src)

	server := r.Sub("server")
	if got := server.Prefix().String(); got != "server" {
		t.Fatalf("Prefix=%q, want %q", got, "server")
	}

	v, ok, _ := server.Get("port")
	if !ok {
		t.Fatal("sub.Get(port) not found")
	}
	if i, _ := v.AsInt64(); i != 8080 {
		t.Fatalf("port=%d", i)
	}

	// Sub-write must reach the parent's keyspace.
	if err := server.Set("port", 9000); err != nil {
		t.Fatal(err)
	}
	pv, _, _ := r.Get("server.port")
	if i, _ := pv.AsInt64(); i != 9000 {
		t.Fatalf("parent server.port=%d, want 9000", i)
	}

	// AllKeys on a sub strips the prefix.
	keys := server.AllKeys()
	want := []string{"host", "port"}
	if !slices.Equal(keys, want) {
		t.Fatalf("sub.AllKeys()=%v, want %v", keys, want)
	}

	// Sub("") returns the same registry.
	if r.Sub("") != r {
		t.Fatal("Sub(\"\") did not return the parent unchanged")
	}

	// Concatenated subs.
	deep := r.Sub("server").Sub("nested").Prefix().String()
	if deep != "server.nested" {
		t.Fatalf("nested prefix=%q, want server.nested", deep)
	}
}

func TestRegistry_ConcurrentReadsWithWrites(t *testing.T) {
	r := newRegistry(t)
	src := NewMapSource("m", map[string]any{"k": 0})
	_ = r.AddSource(src)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers: bump k via Set.
	for i := range 4 {
		wg.Go(func() {
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
					_ = r.Set("k", i*1000+n)
				}
			}
		})
	}

	// Readers: hammer the snapshot.
	for range 8 {
		wg.Go(func() {
			for range 1000 {
				_, _, _ = r.Get("k")
				_ = r.AllKeys()
				_ = r.IsSet("k")
			}
		})
	}

	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()
}
