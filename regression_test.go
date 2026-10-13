package recon

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type recursiveNode struct {
	Name string         `recon:"name"`
	Next *recursiveNode `recon:"next"`
}

func TestBind_RecursiveStructTerminates(t *testing.T) {
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("m", map[string]any{"name": "a"})))
	done := make(chan error, 1)
	var n recursiveNode
	go func() { done <- r.Bind(&n) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Bind did not return on a self-referencing struct")
	}
	if n.Name != "a" || n.Next != nil {
		t.Fatalf("got %+v, want Name=a and Next nil", n)
	}
}

func TestBind_MapFieldFromNestedSource(t *testing.T) {
	src := NewMapSource("file", map[string]any{"labels": map[string]any{"a": "x", "b": "y"}})
	r := newRegistry(t, WithoutWatch(), WithSource(src))
	var cfg struct {
		Labels map[string]string `recon:"labels"`
		Any    any               `recon:"labels"`
	}
	if err := r.Bind(&cfg, WithDecodeStrict()); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if cfg.Labels["a"] != "x" || cfg.Labels["b"] != "y" || len(cfg.Labels) != 2 {
		t.Fatalf("Labels = %v", cfg.Labels)
	}
	if m, ok := cfg.Any.(map[string]any); !ok || m["a"] != "x" {
		t.Fatalf("Any = %#v", cfg.Any)
	}
	got, ok, err := r.GetStringMap("labels")
	if err != nil || !ok || got["b"] != "y" {
		t.Fatalf("GetStringMap = %v %v %v", got, ok, err)
	}
	var into map[string]string
	if err := r.UnmarshalKey("labels", &into); err != nil || into["a"] != "x" {
		t.Fatalf("UnmarshalKey = %v %v", into, err)
	}
}

func TestNewValue_TypedCollections(t *testing.T) {
	r := newRegistry(t, WithoutWatch(),
		WithSource(NewMapSource("m", map[string]any{"ports": []int{80, 443}})))
	if err := r.Set("tags", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Set("env", map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	tags, _, err := r.GetStringSlice("tags")
	if err != nil || len(tags) != 2 || tags[1] != "b" {
		t.Fatalf("GetStringSlice = %v %v", tags, err)
	}
	var cfg struct {
		Tags  []string          `recon:"tags"`
		Ports []int             `recon:"ports"`
		Env   map[string]string `recon:"env"`
	}
	if err := r.Bind(&cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tags) != 2 || cfg.Ports[1] != 443 || cfg.Env["k"] != "v" {
		t.Fatalf("Bind = %+v", cfg)
	}
}

func TestBind_SecretTypeRedacted(t *testing.T) {
	src := NewMapSource("f", map[string]any{"token": "hunter2", "pin": "hunter3"})
	r := newRegistry(t, WithoutWatch(), WithSource(src))
	var cfg struct {
		Token Secret[string] `recon:"token"`
		Pin   Secret[int]    `recon:"pin"`
	}
	err := r.Bind(&cfg)
	if err == nil || strings.Contains(err.Error(), "hunter3") {
		t.Fatalf("bind error leaks or is missing: %v", err)
	}
	if !r.IsSecret("token") {
		t.Fatal("IsSecret(token) = false")
	}
	out, _ := r.SaveString(WithSaveFormat(FormatJSON))
	if strings.Contains(out, "hunter") {
		t.Fatalf("Save leaks: %s", out)
	}
}

func TestBind_EnvUnderscoreField(t *testing.T) {
	t.Setenv("RCTU_MAX_CONNS", "9")
	r := newRegistry(t, WithoutWatch(), WithSource(NewOSEnvSource(WithEnvPrefix("RCTU_"))))
	var cfg struct{ MaxConns int }
	if err := r.Bind(&cfg, WithDecodeStrict()); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if cfg.MaxConns != 9 {
		t.Fatalf("MaxConns = %d, want 9", cfg.MaxConns)
	}
	if v, ok, _ := r.Get("max_conns"); !ok || v.String() != "9" {
		t.Fatalf("Get(max_conns) = %v %v", v, ok)
	}
}

func TestSave_OmitsUnscopedEnvironment(t *testing.T) {
	t.Setenv("RCTS_FAKE_SECRET_TOKEN", "s3cr3t")
	t.Setenv("RCTS_BOUND", "kept")
	r := newRegistry(t, WithoutWatch(), WithSource(NewOSEnvSource()))
	var cfg struct {
		Bound string `recon:"rcts.bound"`
	}
	if err := r.Bind(&cfg); err != nil || cfg.Bound != "kept" {
		t.Fatalf("Bind = %q %v", cfg.Bound, err)
	}
	out, err := r.SaveString(WithSaveFormat(FormatJSON))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "s3cr3t") || strings.Contains(out, os.Getenv("HOME")+`"`) {
		t.Fatalf("Save wrote unbound environment: %s", out)
	}
	if !strings.Contains(out, "kept") {
		t.Fatalf("Save dropped a bound key: %s", out)
	}
}

func TestSecret_AliasesAndParents(t *testing.T) {
	src := NewMapSource("f", map[string]any{"db": map[string]any{"password": "hunter2", "user": "u"}})
	r := newRegistry(t, WithoutWatch(), WithSource(src))
	if err := r.RegisterAlias("pw", "db.password"); err != nil {
		t.Fatal(err)
	}
	r.MarkSecret("pw")
	if s := r.Snapshot().String(); strings.Contains(s, "hunter2") {
		t.Fatalf("Snapshot.String leaks through alias: %s", s)
	}
	if d, _ := r.DescribeKey("db.password"); !d.Secret || strings.Contains(d.Value, "hunter2") {
		t.Fatalf("DescribeKey = %+v", d)
	}
	if !r.Snapshot().IsSecret(Path{"pw"}) {
		t.Fatal("Snapshot.IsSecret(pw) = false")
	}

	r2 := newRegistry(t, WithoutWatch(), WithSource(src))
	r2.MarkSecret("db")
	out, _ := r2.SaveString(WithSaveFormat(FormatJSON))
	if strings.Contains(out, "hunter2") || !r2.IsSecret("db.user") {
		t.Fatalf("parent secret does not cover children: %s", out)
	}

	r3 := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("f",
		map[string]any{"creds": map[string]any{"password": "hunter2"}})))
	var cfg struct {
		Creds struct {
			Password string `recon:"password"`
		} `recon:"creds,secret"`
	}
	if err := r3.Bind(&cfg); err != nil {
		t.Fatal(err)
	}
	if out, _ := r3.SaveString(WithSaveFormat(FormatJSON)); strings.Contains(out, "hunter2") {
		t.Fatalf("secret struct tag does not cover its fields: %s", out)
	}
}

func TestBind_MarkSecretRedactsCoercionError(t *testing.T) {
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("f", map[string]any{"pin": "hunter2"})))
	r.MarkSecret("pin")
	var cfg struct {
		Pin int `recon:"pin"`
	}
	err := r.Bind(&cfg)
	if err == nil || strings.Contains(err.Error(), "hunter2") || strings.Contains(FormatError(r, err), "hunter2") {
		t.Fatalf("error leaks the secret: %v", err)
	}
}

type joinValidator struct{}

func (joinValidator) Validate(map[string]any) error {
	return errors.Join(
		&ValidationError{Path: Path{"a"}, Msg: "a=secretA"},
		&ValidationError{Path: Path{"b"}, Msg: "b=secretB"},
	)
}

func TestValidate_RedactsEveryJoinedError(t *testing.T) {
	r, err := New(WithoutWatch(), WithValidator(joinValidator{}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	r.MarkSecret("a")
	r.MarkSecret("b")
	if err := r.Set("a", "x"); err == nil {
		t.Fatal("Set passed a failing validator")
	}
	verr := r.Validate()
	if verr == nil || strings.Contains(verr.Error(), "secret") {
		t.Fatalf("Validate = %v", verr)
	}
}

func TestResolve_AliasValueFromSource(t *testing.T) {
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("f", map[string]any{"old": "v"})))
	if err := r.RegisterAlias("old", "new"); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"new", "old"} {
		if v, ok, _ := r.Get(k); !ok || v.String() != "v" {
			t.Fatalf("Get(%s) = %q %v, want v", k, v.String(), ok)
		}
	}
}

func TestResolve_ExplicitMapBeatsSourceLeaf(t *testing.T) {
	src := NewMapSource("file", map[string]any{"db": map[string]any{"host": "file-host", "port": 5432}})
	r := newRegistry(t, WithoutWatch(), WithSource(src))
	if err := r.Set("db", map[string]any{"host": "explicit-host"}); err != nil {
		t.Fatal(err)
	}
	v, _, _ := r.Get("db.host")
	if v.String() != "explicit-host" || v.Source() != srcExplicit {
		t.Fatalf("db.host = %q from %q", v.String(), v.Source())
	}
	var cfg struct {
		DB struct {
			Host string `recon:"host"`
			Port int    `recon:"port"`
		} `recon:"db"`
	}
	if err := r.Bind(&cfg); err != nil || cfg.DB.Host != "explicit-host" || cfg.DB.Port != 5432 {
		t.Fatalf("Bind = %+v %v", cfg, err)
	}
}

type RegressionBase struct {
	Name string
}

func TestBind_StructElementsUseFieldNaming(t *testing.T) {
	src := NewMapSource("m", map[string]any{
		"servers": []any{map[string]any{"max_conns": 7, "name": "a"}},
		"plain":   []any{map[string]any{"maxconns": 3}},
	})
	r := newRegistry(t, WithoutWatch(), WithSource(src))
	type server struct {
		RegressionBase
		MaxConns int
		Size     int `recon:"size,default=1"`
	}
	var cfg struct {
		Servers []server `recon:"servers"`
		Plain   []server `recon:"plain"`
	}
	if err := r.Bind(&cfg); err != nil {
		t.Fatal(err)
	}
	got := cfg.Servers[0]
	if got.MaxConns != 7 || got.Name != "a" || got.Size != 1 {
		t.Fatalf("servers[0] = %+v", got)
	}
	if cfg.Plain[0].MaxConns != 3 {
		t.Fatalf("lowercased name no longer matches: %+v", cfg.Plain[0])
	}
}

func TestMemoryBackend_ConcurrentPutAndClose(t *testing.T) {
	for i := range 4000 {
		b := NewInMemoryBackend()
		ctx, cancel := context.WithCancel(context.Background())
		if _, err := b.Watch(ctx); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Go(func() { b.Put("k", "v") })
		if i%2 == 0 {
			wg.Go(func() { _ = b.Close() })
		} else {
			wg.Go(cancel)
		}
		wg.Wait()
		cancel()
	}
}

func TestFileSource_WriteBeforeWatchIsSeen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := NewFileSource(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("a: 22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newRegistry(t, WithSource(src), WithWatcher(NewPollWatcher(10*time.Millisecond)),
		WithReloadDebounce(time.Millisecond))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if v, _, _ := r.Get("a"); v.String() == "22" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	v, _, _ := r.Get("a")
	t.Fatalf("a = %s after the watcher started, want 22", v.String())
}

func TestSnapshot_ScalarAndSubtreeConflictIsDeterministic(t *testing.T) {
	hi := NewMapSource("hi", map[string]any{"db": "url"})
	lo := NewMapSource("lo", map[string]any{"db": map[string]any{"host": "h"}})
	r := newRegistry(t, WithoutWatch(), WithSources(hi, lo))
	for range 100 {
		if got := r.Snapshot().AsMap()["db"]; got != "url" {
			t.Fatalf("AsMap db = %v, want the higher-precedence url", got)
		}
	}
	r2 := newRegistry(t, WithoutWatch(), WithSources(lo, hi))
	for range 100 {
		m, ok := r2.Snapshot().AsMap()["db"].(map[string]any)
		if !ok || m["host"] != "h" {
			t.Fatalf("AsMap db = %v, want the higher-precedence subtree", r2.Snapshot().AsMap()["db"])
		}
	}
}

func TestImmutable_RemovalIsAChange(t *testing.T) {
	src := NewMapSource("m", map[string]any{"port": 80})
	r := newRegistry(t, WithoutWatch(), WithSource(src))
	var cfg struct {
		Port int `recon:"port,immutable,default=8080"`
	}
	if err := r.Bind(&cfg); err != nil {
		t.Fatal(err)
	}
	src.Replace(map[string]any{})
	err := r.Reload()
	var ice *ImmutableChangedError
	if !errors.As(err, &ice) || ice.New != unsetText {
		t.Fatalf("Reload = %v, want an ImmutableChangedError to <unset>", err)
	}
}

func TestPath_BracketSegmentsRoundTrip(t *testing.T) {
	for _, p := range []Path{{"a.]b"}, {"[a]"}, {"x[y"}, {""}, {"a", ""}, {"k8s.io/name", "v"}, {"a]]"}} {
		if got := ParsePath(p.String()); !got.Equal(p) {
			t.Errorf("%q -> %q -> %q", []string(p), p.String(), []string(got))
		}
	}
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("m", map[string]any{"a.]b": 1, "[a]": 2})))
	if got := r.AllKeys(); len(got) != 2 {
		t.Fatalf("AllKeys = %q", got)
	}
}

func TestRemoteSource_PrefixWithoutSlash(t *testing.T) {
	b := NewInMemoryBackend()
	b.PutAll(map[string]string{"app/db/host": "h", "apple/x": "no"})
	src, err := NewRemoteSource("remote", b, WithRemotePrefix("app"), WithRemoteTrimPrefix(true))
	if err != nil {
		t.Fatal(err)
	}
	r := newRegistry(t, WithoutWatch(), WithSource(src))
	if v, ok, _ := r.Get("db.host"); !ok || v.String() != "h" {
		t.Fatalf("db.host = %q %v", v.String(), ok)
	}
	if keys := r.AllKeys(); len(keys) != 1 {
		t.Fatalf("AllKeys = %q", keys)
	}
}

func TestBind_CustomDecoderOnDefaultAndByType(t *testing.T) {
	type Level int
	parse := WithCustomDecoder(func(v Value) (Level, error) {
		if v.String() == "debug" {
			return 1, nil
		}
		return 0, errors.New("bad level")
	})
	r := newRegistry(t, WithoutWatch())
	var cfg struct {
		L Level `recon:"level,default=debug"`
	}
	if err := r.Bind(&cfg, parse); err != nil || cfg.L != 1 {
		t.Fatalf("default through decoder: %v %v", cfg.L, err)
	}
	otherLevel := func() DecodeOption {
		type Level string // same reflect String as the Level above
		return WithCustomDecoder(func(Value) (Level, error) { return "x", nil })
	}()
	if err := r.Bind(&cfg, parse, otherLevel); err != nil || cfg.L != 1 {
		t.Fatalf("decoders for same-named types collide: %v %v", cfg.L, err)
	}
}

func TestBind_FailFastStopsInsideNestedStruct(t *testing.T) {
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("m",
		map[string]any{"a": map[string]any{"x": "bad"}, "y": "bad"})))
	var cfg struct {
		A struct {
			X int `recon:"x"`
		} `recon:"a"`
		Y int `recon:"y"`
	}
	err := r.Bind(&cfg, WithDecodeErrorBehavior(FailFast))
	var multi *MultiError
	if err == nil || errors.As(err, &multi) {
		t.Fatalf("FailFast = %v, want exactly one error", err)
	}
}

func TestDeprecation_WarningsNotDuplicatedAndOnLiveEvent(t *testing.T) {
	src := NewMapSource("m", map[string]any{"old": "v"})
	r := newRegistry(t, WithoutWatch(), WithSource(src))
	type cfg struct {
		Old string `recon:"old,deprecated"`
	}
	for range 3 {
		var c cfg
		if err := r.Bind(&c); err != nil {
			t.Fatal(err)
		}
	}
	if w := r.DrainWarnings(); len(w) != 1 {
		t.Fatalf("queued %d warnings after 3 binds, want 1", len(w))
	}

	lr := newRegistry(t, WithSource(src), WithPoll(20*time.Millisecond), WithReloadDebounce(time.Millisecond))
	live, err := NewLive[cfg](lr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live.Close() }()
	_ = lr.DrainWarnings()
	src.Replace(map[string]any{"old": "v2"})
	select {
	case evt := <-live.Events():
		if len(evt.Warnings) == 0 {
			t.Fatal("the event of the rebind that raised the deprecation carries no warning")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no Live event")
	}
}

func TestExpand_ReferencesAreRelativeToSub(t *testing.T) {
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("m", map[string]any{
		"host": "root-host",
		"svc":  map[string]any{"host": "svc-host", "url": "http://${host}/x"},
	})))
	var cfg struct {
		URL string `recon:"url,expand"`
	}
	if err := r.Sub("svc").Bind(&cfg); err != nil || cfg.URL != "http://svc-host/x" {
		t.Fatalf("Sub bind = %q %v", cfg.URL, err)
	}
}

func TestBind_NotEmptyListAndArrays(t *testing.T) {
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("m", map[string]any{
		"tags": []any{}, "key": "abcd", "pair": []any{1, 2},
	})))
	var c1 struct {
		Tags []string `recon:"tags,notEmpty"`
	}
	if err := r.Bind(&c1); !errors.Is(err, ErrEmptyValue) {
		t.Fatalf("notEmpty on [] = %v", err)
	}
	var c2 struct {
		Key  [4]byte `recon:"key"`
		Pair [2]int  `recon:"pair"`
	}
	if err := r.Bind(&c2); err != nil || string(c2.Key[:]) != "abcd" || c2.Pair[1] != 2 {
		t.Fatalf("arrays = %+v %v", c2, err)
	}
	var c3 struct {
		Pair [3]int `recon:"pair"`
	}
	if err := r.Bind(&c3); err == nil {
		t.Fatal("a 2-element list filled a [3]int")
	}
}

type bigFS struct{}

type bigFile struct{}

func (bigFS) Open(string) (fs.File, error) { return bigFile{}, nil }
func (bigFile) Stat() (fs.FileInfo, error) { return bigInfo{}, nil }
func (bigFile) Read(p []byte) (int, error) { return len(p), nil }
func (bigFile) Close() error               { return nil }

type bigInfo struct{ fs.FileInfo }

func (bigInfo) Mode() fs.FileMode { return 0 }
func (bigInfo) Size() int64       { return MaxFileSize + 1 }

func TestFileSourceFS_SizeCap(t *testing.T) {
	_, err := NewFileSourceFS("big", bigFS{}, "c.json")
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("err = %v, want ErrFileTooLarge", err)
	}
}

type extCodec struct{ name string }

func (c extCodec) Name() string                        { return c.name }
func (extCodec) Extensions() []string                  { return []string{".x"} }
func (extCodec) Decode([]byte) (map[string]any, error) { return map[string]any{}, nil }
func (extCodec) Encode(map[string]any) ([]byte, error) { return nil, nil }

func TestCodecs_ByExtensionPrefersLatest(t *testing.T) {
	for range 50 {
		c := NewCodecs(extCodec{"a"}, extCodec{"b"}, extCodec{"c"})
		if got, _ := c.ByExtension(".x"); got.Name() != "c" {
			t.Fatalf("ByExtension = %s, want the latest registration c", got.Name())
		}
	}
}

func TestJSON_IntegersStayExact(t *testing.T) {
	for _, codec := range []Codec{JSON, JSONC} {
		m, err := codec.Decode([]byte(`{"n": 1000000, "big": 9007199254740993, "huge": 100000000000000000000, "f": 1.5, "e": 1e6}`))
		if err != nil {
			t.Fatal(err)
		}
		r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("j", m)))
		if n, _, err := Get[int](r, "n"); err != nil || n != 1000000 {
			t.Fatalf("%s: Get[int](n) = %d %v", codec.Name(), n, err)
		}
		var cfg struct {
			Big  int64  `recon:"big"`
			Huge string `recon:"huge"`
			E    string `recon:"e"`
			N    string `recon:"n"`
		}
		if err := r.Bind(&cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Big != 9007199254740993 || cfg.Huge != "100000000000000000000" || cfg.E != "1000000" || cfg.N != "1000000" {
			t.Fatalf("%s: %+v", codec.Name(), cfg)
		}
		if f, _, _ := Get[float64](r, "f"); f != 1.5 {
			t.Fatalf("f = %v", f)
		}
	}
}

func TestTOML_DateTimes(t *testing.T) {
	m, err := TOML.Decode([]byte("at = 2024-01-02T03:04:05Z\nlocal = 2024-01-02T03:04:05\nday = 2024-01-02\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("t", m)))
	if v, _, _ := r.Get("at"); v.String() != "2024-01-02T03:04:05Z" {
		t.Fatalf("at.String() = %q", v.String())
	}
	var cfg struct {
		AtText string    `recon:"at"`
		Local  time.Time `recon:"local"`
	}
	if err := r.Bind(&cfg); err != nil || cfg.AtText != "2024-01-02T03:04:05Z" || cfg.Local.Hour() != 3 {
		t.Fatalf("Bind = %+v %v", cfg, err)
	}
	for _, k := range []string{"local", "day"} {
		if _, _, err := Get[time.Time](r, k); err != nil {
			t.Fatalf("Get[time.Time](%s): %v", k, err)
		}
	}
}

func TestNull_BindsAsZeroValue(t *testing.T) {
	m, err := YAML.Decode([]byte("mode: null\nname:\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("y", m)))
	mode := "preset"
	cfg := struct {
		Mode *string `recon:"mode"`
		Name string  `recon:"name"`
	}{Mode: &mode, Name: "preset"}
	if err := r.Bind(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != nil || cfg.Name != "" {
		t.Fatalf("null bound as %v / %q, want nil / empty", cfg.Mode, cfg.Name)
	}
	if s, ok, err := Get[string](r, "mode"); err != nil || !ok || s != "" {
		t.Fatalf("Get[string](null) = %q %v %v", s, ok, err)
	}
}

func TestSaveTo_KeepsModeAndSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes and symlinks differ on Windows")
	}
	dir := t.TempDir()
	realFile := filepath.Join(dir, "realFile.json")
	link := filepath.Join(dir, "link.json")
	if err := os.WriteFile(realFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("realFile.json", link); err != nil {
		t.Fatal(err)
	}
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("m", map[string]any{"a": 1})))
	if err := r.SaveTo(link); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("SaveTo replaced the symlink")
	}
	info, err := os.Stat(realFile)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want 0644", info.Mode().Perm())
	}
	var got map[string]any
	data, _ := os.ReadFile(realFile)
	if err := json.Unmarshal(data, &got); err != nil || got["a"] != float64(1) {
		t.Fatalf("target holds %s", data)
	}
}

// JSON integers reach other formats as integers, not floats.
func TestJSON_IntegersSaveAsIntegersInYAML(t *testing.T) {
	m, err := JSON.Decode([]byte(`{"port": 8080, "ratio": 0.5}`))
	if err != nil {
		t.Fatal(err)
	}
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("j", m)))
	out, err := r.SaveString(WithSaveFormat(FormatYAML))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "port: 8080\n") {
		t.Fatalf("YAML = %q, want port: 8080", out)
	}
}
