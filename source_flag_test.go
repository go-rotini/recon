package recon

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// fakeFlagAdapter is the synthetic [FlagAdapter] every Phase 7 test
// drives. It models the distinction every real adapter must surface:
// `set` keys came from the command line; the rest are defaults the
// parser holds but did NOT observe being passed.
type fakeFlagAdapter struct {
	values  map[string]any
	setKeys map[string]bool
}

func newFakeFlagAdapter() *fakeFlagAdapter {
	return &fakeFlagAdapter{
		values:  map[string]any{},
		setKeys: map[string]bool{},
	}
}

// set records the value as having been explicitly passed on the
// command line. unset records a default — the adapter knows about
// the flag but the user did not provide it.
func (a *fakeFlagAdapter) set(name string, v any)   { a.values[name] = v; a.setKeys[name] = true }
func (a *fakeFlagAdapter) unset(name string, v any) { a.values[name] = v; a.setKeys[name] = false }

func (a *fakeFlagAdapter) Names() []string {
	out := make([]string, 0, len(a.setKeys))
	for k, set := range a.setKeys {
		if set {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func (a *fakeFlagAdapter) Lookup(name string) (any, bool) {
	v, present := a.values[name]
	if !present {
		return nil, false
	}
	return v, a.setKeys[name]
}

func TestFlagAdapter_FakeBehavior(t *testing.T) {
	// Sanity check the test double itself before driving FlagSource.
	a := newFakeFlagAdapter()
	a.set("port", 8080)
	a.unset("host", "localhost")

	if names := a.Names(); !slices.Equal(names, []string{"port"}) {
		t.Fatalf("Names()=%v, want [port]", names)
	}
	if _, ok := a.Lookup("port"); !ok {
		t.Fatal("Lookup(port) ok=false")
	}
	if _, ok := a.Lookup("host"); ok {
		t.Fatal("Lookup(host) ok=true; default-only flag must report set=false")
	}
}

func TestFlagSource_NilAdapterRejected(t *testing.T) {
	_, err := NewFlagSource(nil)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err=%v, want ErrInvalidPath", err)
	}
}

func TestFlagSource_NameDefaultsAndOverride(t *testing.T) {
	adapter := newFakeFlagAdapter()

	def, err := NewFlagSource(adapter)
	if err != nil {
		t.Fatal(err)
	}
	if def.Name() != "flags" {
		t.Fatalf("Name()=%q, want flags", def.Name())
	}

	custom, err := NewFlagSource(adapter, WithFlagName("cli"))
	if err != nil {
		t.Fatal(err)
	}
	if custom.Name() != "cli" {
		t.Fatalf("Name()=%q, want cli", custom.Name())
	}

	// Empty name is rejected (keeps the default).
	keepDefault, err := NewFlagSource(adapter, WithFlagName(""))
	if err != nil {
		t.Fatal(err)
	}
	if keepDefault.Name() != "flags" {
		t.Fatalf("WithFlagName(\"\") clobbered default: got %q", keepDefault.Name())
	}
}

func TestFlagSource_GetReturnsSetValues(t *testing.T) {
	adapter := newFakeFlagAdapter()
	adapter.set("port", 9090)

	s, err := NewFlagSource(adapter)
	if err != nil {
		t.Fatal(err)
	}
	v, ok, err := s.Get(MakePath("port"))
	if err != nil || !ok {
		t.Fatalf("Get(port) ok=%v err=%v", ok, err)
	}
	i, _ := v.AsInt64()
	if i != 9090 {
		t.Fatalf("port=%d, want 9090", i)
	}
}

func TestFlagSource_GetIgnoresDefaultsOnly(t *testing.T) {
	// A flag whose value came only from a compile-time default must
	// NOT surface through Get — otherwise it would shadow lower-
	// precedence sources that should win.
	adapter := newFakeFlagAdapter()
	adapter.unset("port", 8080)

	s, _ := NewFlagSource(adapter)
	if _, ok, _ := s.Get(MakePath("port")); ok {
		t.Fatal("Get(port) ok=true for a default-only flag")
	}
	if keys := s.Keys(); len(keys) != 0 {
		t.Fatalf("Keys()=%v, want empty when all flags are defaults", keys)
	}
}

func TestFlagSource_KeysSorted(t *testing.T) {
	adapter := newFakeFlagAdapter()
	adapter.set("z", 1)
	adapter.set("a", 2)
	adapter.set("m", 3)

	s, _ := NewFlagSource(adapter)
	keys := s.Keys()
	got := make([]string, len(keys))
	for i, k := range keys {
		got[i] = k.String()
	}
	if !slices.IsSorted(got) {
		t.Fatalf("Keys()=%v, want sorted", got)
	}
}

func TestFlagSource_DefaultTransform_StripsDashPrefix(t *testing.T) {
	adapter := newFakeFlagAdapter()
	adapter.set("--server.port", 9000)
	adapter.set("-verbose", true)
	adapter.set("plain", "x")

	s, _ := NewFlagSource(adapter)
	v1, ok1, _ := s.Get(MakePath("server", "port"))
	if !ok1 {
		t.Fatal("server.port not found")
	}
	if i, _ := v1.AsInt64(); i != 9000 {
		t.Fatalf("server.port=%d", i)
	}

	_, ok2, _ := s.Get(MakePath("verbose"))
	if !ok2 {
		t.Fatal("verbose not found")
	}

	_, ok3, _ := s.Get(MakePath("plain"))
	if !ok3 {
		t.Fatal("plain not found")
	}
}

func TestFlagSource_CustomTransform_DashToDot(t *testing.T) {
	adapter := newFakeFlagAdapter()
	adapter.set("server-port", 9000)

	transform := func(name string) Path {
		name = strings.TrimPrefix(name, "--")
		name = strings.TrimPrefix(name, "-")
		name = strings.ReplaceAll(name, "-", ".")
		return ParsePath(name)
	}
	s, err := NewFlagSource(adapter, WithFlagPathTransform(transform))
	if err != nil {
		t.Fatal(err)
	}
	v, ok, _ := s.Get(MakePath("server", "port"))
	if !ok {
		t.Fatal("server.port via dash-to-dot transform not found")
	}
	if i, _ := v.AsInt64(); i != 9000 {
		t.Fatalf("server.port=%d", i)
	}
}

func TestFlagSource_NilTransformIgnored(t *testing.T) {
	adapter := newFakeFlagAdapter()
	adapter.set("k", "v")
	// nil transform must NOT overwrite the default.
	s, err := NewFlagSource(adapter, WithFlagPathTransform(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get(MakePath("k")); !ok {
		t.Fatal("nil transform broke the default path resolution")
	}
}

func TestFlagSource_PrecedenceFlagsWin(t *testing.T) {
	// "CLI → Env → Config → Default" — flags registered first must
	// shadow every lower-precedence source.
	flagAdapter := newFakeFlagAdapter()
	flagAdapter.set("port", 9000)

	flags, _ := NewFlagSource(flagAdapter)
	envSrc := NewMapSource("env", map[string]any{"port": 8080})
	cfgSrc := NewMapSource("config", map[string]any{"port": 7070})

	r := newRegistry(t, WithSources(flags, envSrc, cfgSrc))
	_ = r.SetDefault("port", 3000)

	v, ok, _ := r.Get("port")
	if !ok {
		t.Fatal("port not resolved")
	}
	i, _ := v.AsInt64()
	if i != 9000 {
		t.Fatalf("port=%d, want 9000 (flag should win)", i)
	}
	if v.Source() != "flags" {
		t.Fatalf("Source()=%q, want flags", v.Source())
	}
}

func TestFlagSource_PrecedenceDefaultOnlyDoesNotShadow(t *testing.T) {
	// When the flag holds only a parser-side default, the source
	// chain must fall through to env / config.
	flagAdapter := newFakeFlagAdapter()
	flagAdapter.unset("port", 9999) // default only

	flags, _ := NewFlagSource(flagAdapter)
	envSrc := NewMapSource("env", map[string]any{"port": 8080})

	r := newRegistry(t, WithSources(flags, envSrc))
	v, ok, _ := r.Get("port")
	if !ok {
		t.Fatal("port not resolved")
	}
	i, _ := v.AsInt64()
	if i != 8080 {
		t.Fatalf("port=%d, want 8080 (env should win when flag is default-only)", i)
	}
	if v.Source() != "env" {
		t.Fatalf("Source()=%q, want env", v.Source())
	}
}

func TestFlagSource_PrecedenceExplicitOverrideStillWins(t *testing.T) {
	// Registry.Set sits above every source — flags included.
	flagAdapter := newFakeFlagAdapter()
	flagAdapter.set("port", 9000)

	flags, _ := NewFlagSource(flagAdapter)
	r := newRegistry(t, WithSource(flags))
	_ = r.Set("port", 1234)

	v, _, _ := r.Get("port")
	i, _ := v.AsInt64()
	if i != 1234 {
		t.Fatalf("port=%d, want 1234 (Set must beat flag)", i)
	}
	if v.Source() != srcExplicit {
		t.Fatalf("Source()=%q, want %q", v.Source(), srcExplicit)
	}
}

func TestFlagSource_PrecedenceProvenanceListsFlag(t *testing.T) {
	flagAdapter := newFakeFlagAdapter()
	flagAdapter.set("port", 9000)

	flags, _ := NewFlagSource(flagAdapter)
	envSrc := NewMapSource("env", map[string]any{"port": 8080})

	r := newRegistry(t, WithSources(flags, envSrc))
	srcs := r.Snapshot().SourceFor(MakePath("port"))
	if !slices.Equal(srcs, []string{"flags", "env"}) {
		t.Fatalf("SourceFor(port)=%v, want [flags env]", srcs)
	}
}

func TestFlagSource_CloseIsNoop(t *testing.T) {
	adapter := newFakeFlagAdapter()
	s, _ := NewFlagSource(adapter)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Second Close is still a no-op.
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestFlagSource_PostConstructionFlagChangesVisible(t *testing.T) {
	// Real parsers can have flags re-parsed (e.g., on subcommand
	// dispatch). The adapter's Names/Lookup are consulted on every
	// snapshot rebuild, so a Reload after the adapter changes
	// surfaces the new value.
	adapter := newFakeFlagAdapter()
	flags, _ := NewFlagSource(adapter)

	r := newRegistry(t, WithSource(flags))
	if _, ok, _ := r.Get("k"); ok {
		t.Fatal("k visible before adapter saw it")
	}

	adapter.set("k", "post")
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	v, ok, _ := r.Get("k")
	if !ok {
		t.Fatal("k not visible after Reload")
	}
	s, _ := v.AsString()
	if s != "post" {
		t.Fatalf("k=%q, want post", s)
	}
}
