package recon_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rotini/recon"
)

// Acceptance tests drive the public API end to end with real files on
// disk, the way a CLI or service loads its configuration. Each test name
// starts with TestAcceptance so `make test-acceptance` (which runs
// `-run TestAcceptance`) selects them.

func writeConfig(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// A CLI layers flags over environment variables over a config file, then
// binds the result into a typed struct and can say where each value came
// from.
func TestAcceptance_CLILayeredConfig(t *testing.T) {
	path := writeConfig(t, "app.yaml", `server:
  host: example.internal
  port: 8080
log:
  level: info
`)
	t.Setenv("APP_SERVER_PORT", "9090")

	file, err := recon.NewFileSource(path)
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	env := recon.NewOSEnvSource(recon.WithEnvPrefix("APP_"))
	flags := recon.NewMapSource("flags", map[string]any{"log": map[string]any{"level": "debug"}})

	r, err := recon.New(recon.WithoutWatch(), recon.WithSources(flags, env, file))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()

	type config struct {
		Host     string        `recon:"server.host,required"`
		Port     int           `recon:"server.port,default=80"`
		LogLevel string        `recon:"log.level"`
		Timeout  time.Duration `recon:"timeout,default=30s"`
	}
	var cfg config
	if err := r.Bind(&cfg); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	want := config{Host: "example.internal", Port: 9090, LogLevel: "debug", Timeout: 30 * time.Second}
	if cfg != want {
		t.Errorf("Bind = %+v, want %+v", cfg, want)
	}

	for key, src := range map[string]string{
		"server.host": "app.yaml",
		"server.port": "osenv",
		"log.level":   "flags",
	} {
		v, ok, err := r.Get(key)
		if err != nil || !ok {
			t.Fatalf("Get(%q) = _, %v, %v", key, ok, err)
		}
		if v.Source() != src {
			t.Errorf("%s came from %q, want %q", key, v.Source(), src)
		}
	}
}

// The same settings written in every bundled file format bind to the
// same struct.
func TestAcceptance_EveryFormatBindsTheSame(t *testing.T) {
	t.Parallel()
	docs := map[string]string{
		"app.yaml":  "name: widget\nport: 8443\ndebug: true\ntimeout: 5s\n",
		"app.json":  `{"name": "widget", "port": 8443, "debug": true, "timeout": "5s"}`,
		"app.jsonc": "{\n  // service settings\n  \"name\": \"widget\",\n  \"port\": 8443,\n  \"debug\": true,\n  \"timeout\": \"5s\",\n}\n",
		"app.toml":  "name = \"widget\"\nport = 8443\ndebug = true\ntimeout = \"5s\"\n",
		"app.env":   "name=widget\nport=8443\ndebug=true\ntimeout=5s\n",
	}
	type config struct {
		Name    string        `recon:"name,required"`
		Port    int           `recon:"port"`
		Debug   bool          `recon:"debug"`
		Timeout time.Duration `recon:"timeout"`
	}
	want := config{Name: "widget", Port: 8443, Debug: true, Timeout: 5 * time.Second}
	for name, body := range docs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src, err := recon.NewFileSource(writeConfig(t, name, body))
			if err != nil {
				t.Fatalf("NewFileSource: %v", err)
			}
			r, err := recon.New(recon.WithoutWatch(), recon.WithSource(src))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer func() { _ = r.Close() }()
			var cfg config
			if err := r.Bind(&cfg); err != nil {
				t.Fatalf("Bind: %v", err)
			}
			if cfg != want {
				t.Errorf("Bind = %+v, want %+v", cfg, want)
			}
		})
	}
}

// A malformed config file is reported with its path, line and column so
// the user can jump straight to the mistake.
func TestAcceptance_BrokenConfigReportsWhere(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "app.toml", "name = \"widget\"\nport = = 8443\n")
	_, err := recon.NewFileSource(path)
	pe, ok := errors.AsType[*recon.ParseError](err)
	if !ok {
		t.Fatalf("err = %v, want *recon.ParseError", err)
	}
	if pe.Path != path || pe.Position.Line != 2 || pe.Msg == "" {
		t.Errorf("ParseError = %+v, want path %s, line 2 and a message", pe, path)
	}
	if !strings.Contains(err.Error(), path+":2:") {
		t.Errorf("Error() = %q, want it to name %s:2:<col>", err.Error(), path)
	}
}

// An edit that breaks the schema is refused and the last good config
// stays in effect; a missing required key renders as a readable summary.
func TestAcceptance_SchemaAndRequiredKeys(t *testing.T) {
	t.Parallel()
	schema := []byte(`{
		"type": "object",
		"properties": {"port": {"type": "integer", "minimum": 1, "maximum": 65535}}
	}`)
	path := writeConfig(t, "app.yaml", "port: 8080\n")
	src, err := recon.NewFileSource(path)
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	r, err := recon.New(recon.WithoutWatch(), recon.WithSchema(schema), recon.WithSource(src))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()

	if err := os.WriteFile(path, []byte("port: 70000\n"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := src.(*recon.FileSource).Reload(); err != nil {
		t.Fatalf("FileSource.Reload: %v", err)
	}
	if err := r.Reload(); !errors.Is(err, recon.ErrValidation) {
		t.Fatalf("Reload err = %v, want ErrValidation", err)
	}
	if port, _, _ := r.GetInt("port"); port != 8080 {
		t.Errorf("port = %d after a rejected edit, want the last good 8080", port)
	}

	var cfg struct {
		Port  int    `recon:"port"`
		Token string `recon:"token,required"`
	}
	err = r.Bind(&cfg)
	if !errors.Is(err, recon.ErrMissingRequired) {
		t.Fatalf("Bind err = %v, want ErrMissingRequired", err)
	}
	if msg := recon.FormatError(r, err); !strings.Contains(msg, "token") {
		t.Errorf("FormatError = %q, want it to name the missing key", msg)
	}
}

// A long-running service watches its config file and sees an edit
// without restarting.
func TestAcceptance_LiveReloadsEditedFile(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "app.yaml", "port: 8080\n")
	src, err := recon.NewFileSource(path)
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	r, err := recon.New(
		recon.WithWatcher(recon.NewPollWatcher(10*time.Millisecond)),
		recon.WithReloadDebounce(time.Millisecond),
		recon.WithSource(src),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()

	type config struct {
		Port int `recon:"port"`
	}
	live, err := recon.NewLive[config](r)
	if err != nil {
		t.Fatalf("NewLive: %v", err)
	}
	defer func() { _ = live.Close() }()
	if got := live.Get().Port; got != 8080 {
		t.Fatalf("initial port = %d, want 8080", got)
	}

	// Each edit differs, so the change is seen even if the watcher takes
	// its baseline after the first one.
	deadline := time.Now().Add(10 * time.Second)
	for edit := 0; live.Get().Port != 9090; edit++ {
		if time.Now().After(deadline) {
			t.Fatalf("port still %d after editing the file, want 9090", live.Get().Port)
		}
		body := fmt.Sprintf("port: 9090 # edit %d\n", edit)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
