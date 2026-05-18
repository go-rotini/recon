package recon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- expand ---------------------------------------------------------

func TestBind_ExpandSubstitutesOtherKeys(t *testing.T) {
	type C struct {
		Host     string `recon:"host"`
		Port     int    `recon:"port"`
		Endpoint string `recon:"endpoint,expand"`
	}
	r := newRegistry(t)
	_ = r.Set("host", "example.com")
	_ = r.Set("port", "9090")
	_ = r.Set("endpoint", "https://${host}:${port}/api")

	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Endpoint != "https://example.com:9090/api" {
		t.Fatalf("Endpoint=%q", c.Endpoint)
	}
}

func TestBind_ExpandDefaultModifier(t *testing.T) {
	type C struct {
		Endpoint string `recon:"endpoint,expand"`
	}
	r := newRegistry(t)
	_ = r.Set("endpoint", "${missing:-fallback}")

	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Endpoint != "fallback" {
		t.Fatalf("Endpoint=%q, want fallback", c.Endpoint)
	}
}

func TestBind_ExpandErrorModifier(t *testing.T) {
	type C struct {
		Endpoint string `recon:"endpoint,expand"`
	}
	r := newRegistry(t)
	_ = r.Set("endpoint", "${missing:?must be set}")

	var c C
	err := r.Bind(&c)
	if err == nil {
		t.Fatal("expected error for ${missing:?...}")
	}
	if !strings.Contains(err.Error(), "must be set") {
		t.Fatalf("err=%v, want substring 'must be set'", err)
	}
}

func TestBind_ExpandMissingKeyFails(t *testing.T) {
	type C struct {
		Endpoint string `recon:"endpoint,expand"`
	}
	r := newRegistry(t)
	_ = r.Set("endpoint", "${truly.missing}")

	var c C
	err := r.Bind(&c)
	if err == nil {
		t.Fatal("expected error for unresolved expand reference")
	}
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("err=%v, want wrap of ErrKeyNotFound", err)
	}
}

// ---- fromFile -------------------------------------------------------

func TestBind_FromFileReadsContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("topsecret"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	type C struct {
		Token string `recon:"token,fromFile"`
	}
	r := newRegistry(t)
	_ = r.Set("token", path)

	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Token != "topsecret" {
		t.Fatalf("Token=%q, want topsecret", c.Token)
	}
}

func TestBind_FromFileMissingFails(t *testing.T) {
	type C struct {
		Token string `recon:"token,fromFile"`
	}
	r := newRegistry(t)
	_ = r.Set("token", "/no/such/path/expected")

	var c C
	err := r.Bind(&c)
	if err == nil {
		t.Fatal("expected error for missing fromFile path")
	}
}

// ---- format= -------------------------------------------------------

func TestBind_FormatDecodesJSON(t *testing.T) {
	type Sub struct {
		A int    `recon:"a"`
		B string `recon:"b"`
	}
	type C struct {
		Blob Sub `recon:"blob,format=json"`
	}
	r := newRegistry(t)
	_ = r.Set("blob", `{"a": 42, "b": "hello"}`)

	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	// JSON widens int → float64; Coerce widens int64→int. Verify
	// the decoded payload landed in the struct via re-Bind path —
	// since format= replaces the value with a MapKind, the coerce
	// for the struct field is a fresh nested-bind.
	if c.Blob.B != "hello" {
		t.Fatalf("Blob.B=%q", c.Blob.B)
	}
}

func TestBind_FormatUnknownCodecFails(t *testing.T) {
	type C struct {
		Blob string `recon:"blob,format=xml"`
	}
	r := newRegistry(t)
	_ = r.Set("blob", "<x/>")

	var c C
	err := r.Bind(&c)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err=%v, want ErrUnsupportedFormat", err)
	}
}

// ---- deprecated ----------------------------------------------------

func TestBind_DeprecatedQueuesWarning(t *testing.T) {
	type C struct {
		Old string `recon:"old,deprecated=use 'new' instead"`
	}
	r := newRegistry(t)
	_ = r.Set("old", "value")

	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	warnings := r.DrainWarnings()
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1", len(warnings))
	}
	if !strings.Contains(warnings[0].Message, "use 'new' instead") {
		t.Fatalf("warning=%+v", warnings[0])
	}
	if warnings[0].Path.String() != "old" {
		t.Fatalf("warning.Path=%s, want old", warnings[0].Path)
	}
}

func TestBind_DeprecatedNoWarningWhenAbsent(t *testing.T) {
	type C struct {
		Old string `recon:"old,deprecated"`
	}
	r := newRegistry(t)
	// "old" is not set anywhere — Bind binds zero-value, no warning.

	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if w := r.DrainWarnings(); len(w) > 0 {
		t.Fatalf("unexpected warnings: %v", w)
	}
}

func TestBind_DeprecatedDefaultMessage(t *testing.T) {
	type C struct {
		Old string `recon:"old,deprecated"`
	}
	r := newRegistry(t)
	_ = r.Set("old", "value")

	var c C
	_ = r.Bind(&c)
	w := r.DrainWarnings()
	if len(w) != 1 {
		t.Fatalf("want 1 warning, got %d", len(w))
	}
	if !strings.Contains(w[0].Message, "old") {
		t.Fatalf("default message missing path: %q", w[0].Message)
	}
}

// ---- unset ---------------------------------------------------------

func TestBind_UnsetClearsExplicitAfterRead(t *testing.T) {
	type C struct {
		Token string `recon:"token,unset"`
	}
	r := newRegistry(t)
	_ = r.Set("token", "hunter2")

	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Token != "hunter2" {
		t.Fatalf("Token=%q", c.Token)
	}
	// Explicit-layer entry should be gone after the bind.
	if r.IsSet("token") {
		t.Fatal("token still set after Bind on unset-tagged field")
	}
}

// ---- DrainWarnings -------------------------------------------------

func TestRegistry_DrainWarnings_EmptyWhenNoneQueued(t *testing.T) {
	r := newRegistry(t)
	if w := r.DrainWarnings(); w != nil {
		t.Fatalf("DrainWarnings=%v, want nil", w)
	}
}

func TestRegistry_DrainWarnings_DrainsExactlyOnce(t *testing.T) {
	type C struct {
		Old string `recon:"old,deprecated"`
	}
	r := newRegistry(t)
	_ = r.Set("old", "v")
	var c C
	_ = r.Bind(&c)

	first := r.DrainWarnings()
	if len(first) != 1 {
		t.Fatalf("first drain got %d", len(first))
	}
	second := r.DrainWarnings()
	if len(second) != 0 {
		t.Fatalf("second drain got %d, want 0", len(second))
	}
}

// ---- transforms -----------------------------------------------------

func TestApplyTransform_AllNamedTransforms(t *testing.T) {
	cases := []struct {
		transform string
		in, want  string
	}{
		{"snake", "ServerPort", "server_port"},
		{"kebab", "ServerPort", "server-port"},
		{"camel", "server_port", "serverPort"},
		{"upper", "server.port", "SERVER.PORT"},
		{"lower", "SERVER.PORT", "server.port"},
		{"", "ServerPort", "ServerPort"},        // empty → no-op
		{"unknown", "ServerPort", "ServerPort"}, // unknown → no-op
	}
	for _, tc := range cases {
		t.Run(tc.transform, func(t *testing.T) {
			if got := applyTransform(tc.in, tc.transform); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestToCamelCase(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"server_port", "serverPort"},
		{"already_camel_case", "alreadyCamelCase"},
		{"single", "single"},
		{"", ""},
		{"trailing_", "trailing"},
	}
	for _, tc := range cases {
		got := toCamelCase(tc.in)
		if got != tc.want {
			t.Errorf("toCamelCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBind_TransformCamel(t *testing.T) {
	// transform=camel rewrites a snake_case tag name into camelCase
	// so the source-side spelling can match a camel-cased key.
	type C struct {
		Port int `recon:"server_port,transform=camel"`
	}
	r := newRegistry(t)
	_ = r.Set("serverPort", 8080)
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Port != 8080 {
		t.Fatalf("got %d, want 8080", c.Port)
	}
}

func TestBind_TransformKebab(t *testing.T) {
	type C struct {
		ServerPort int `recon:",transform=kebab"`
	}
	r := newRegistry(t)
	_ = r.Set("server-port", 8080)
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.ServerPort != 8080 {
		t.Fatalf("got %d, want 8080", c.ServerPort)
	}
}

func TestBind_TransformUpperOnMultiSegmentName(t *testing.T) {
	// A tag.Name carrying the delimiter splits, then transform=upper
	// rewrites every segment.
	type C struct {
		X string `recon:"server.port,transform=upper"`
	}
	r := newRegistry(t)
	_ = r.Set("SERVER.PORT", "yes")
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.X != "yes" {
		t.Fatalf("got %q, want yes", c.X)
	}
}
