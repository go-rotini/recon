package recon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFileSource_WithFileName(t *testing.T) {
	a := filepath.Join(t.TempDir(), "config.yaml")
	b := filepath.Join(t.TempDir(), "config.yaml")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("k: v\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sa, err := NewFileSource(a, WithFileName("system"))
	if err != nil {
		t.Fatal(err)
	}
	sb, err := NewFileSource(b)
	if err != nil {
		t.Fatal(err)
	}
	r := newRegistry(t, WithoutWatch(), WithSources(sa, sb))
	if got := r.Sources(); !slices.Equal(got, []string{"system", "config.yaml"}) {
		t.Fatalf("Sources = %q", got)
	}
}

func TestDotenvSource_WithFileEnvKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.local")
	if err := os.WriteFile(path, []byte("APP_SERVER_PORT=9999\nAPP_MAX_CONNS=3\nOTHER=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := NewDotenvSource(path, WithFileEnvKeys("APP_"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRegistry(t, WithoutWatch(), WithSource(src))
	var cfg struct {
		Port     int `recon:"server.port,default=8080"`
		MaxConns int
	}
	if err := r.Bind(&cfg, WithDecodeStrict()); err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9999 || cfg.MaxConns != 3 {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestRegistry_SetDefaultsAndSetValues(t *testing.T) {
	r := newRegistry(t, WithoutWatch())
	if err := r.SetDefaults(map[string]any{"a": 1, "b": 2}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetValues(map[string]any{"b": 3, "c": "x"}); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"a": "1", "b": "3", "c": "x"} {
		if v, _, _ := r.Get(k); v.String() != want {
			t.Fatalf("%s = %q, want %q", k, v.String(), want)
		}
	}
	if err := r.SetValues(map[string]any{"b": nil}); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := r.Get("b"); v.String() != "2" {
		t.Fatalf("b after clearing the override = %q, want the default 2", v.String())
	}
}

type listerBackend struct {
	*MemoryBackend
	gets int
}

func (b *listerBackend) Get(ctx context.Context, key string) (string, bool, error) {
	b.gets++
	return b.MemoryBackend.Get(ctx, key)
}

func TestRemoteSource_ListerAndContext(t *testing.T) {
	b := &listerBackend{MemoryBackend: NewInMemoryBackend()}
	b.PutAll(map[string]string{"a": "1", "b": "2"})
	src, err := NewRemoteSourceContext(context.Background(), "remote", b)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := src.Get(Path{"b"}); !ok || v.String() != "2" {
		t.Fatalf("b = %v %v", v, ok)
	}
	if b.gets != 0 {
		t.Fatalf("refresh made %d Get calls, want 0 with a BackendLister", b.gets)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewRemoteSourceContext(ctx, "remote", stalledBackend{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

type stalledBackend struct{}

func (stalledBackend) List(ctx context.Context, _ string) ([]string, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (stalledBackend) Get(context.Context, string) (string, bool, error) { return "", false, nil }
func (stalledBackend) Close() error                                      { return nil }

func TestBind_OmitEmptyPointerStruct(t *testing.T) {
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("m", map[string]any{"db": map[string]any{"host": "h"}})))
	type section struct {
		Cert string `recon:"cert,default=c.pem"`
		Host string `recon:"host"`
	}
	var cfg struct {
		TLS *section `recon:"tls,omitempty"`
		DB  *section `recon:"db,omitempty"`
	}
	if err := r.Bind(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.TLS != nil || cfg.DB == nil || cfg.DB.Host != "h" {
		t.Fatalf("TLS=%v DB=%+v", cfg.TLS, cfg.DB)
	}
}

func TestEnv_PrefixFoldsCaseWhereNamesDo(t *testing.T) {
	prev := envNamesFoldCase
	t.Cleanup(func() { envNamesFoldCase = prev })
	envNamesFoldCase = true
	if !hasEnvPrefix("App_Port", "APP_") || hasEnvPrefix("AP", "APP_") {
		t.Fatal("case-insensitive prefix match failed")
	}
	envNamesFoldCase = false
	if hasEnvPrefix("App_Port", "APP_") {
		t.Fatal("case-sensitive prefix matched")
	}
}

func TestSortPaths_MatchesStringOrder(t *testing.T) {
	ps := []Path{{"b"}, {"a", "z"}, {"a.b"}, {"a"}, {"[x"}, {"a", ""}}
	sortPaths(ps)
	if !slices.IsSortedFunc(ps, func(a, b Path) int { return strings.Compare(a.String(), b.String()) }) {
		t.Fatalf("not sorted by String: %v", ps)
	}
}

type dupFlags struct{}

func (dupFlags) Names() []string { return []string{"--port", "-port"} }
func (dupFlags) Lookup(name string) (any, bool) {
	if name == "-port" {
		return "9", true
	}
	return nil, false
}

func TestFlagSource_SkipsUnsetFlagWithSamePath(t *testing.T) {
	src, err := NewFlagSource(dupFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := src.Get(Path{"port"}); !ok || v.String() != "9" {
		t.Fatalf("port = %v %v, want 9 from the set flag", v, ok)
	}
}
