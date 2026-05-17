package recon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- path expansion (POSIX shell forms) ---------------------------

func TestPathExpansion_TildeHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	got, err := expandShellPath("~/foo")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(home, "foo") && got != home+"/foo" {
		t.Fatalf("~/foo=%q, want %q", got, home+"/foo")
	}
}

func TestPathExpansion_BareDollarVar(t *testing.T) {
	t.Setenv("RECON_TEST_DIR", "/tmp/recon")
	got, err := expandShellPath("$RECON_TEST_DIR/x")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/recon/x" {
		t.Fatalf("got=%q", got)
	}
}

func TestPathExpansion_BracedVar(t *testing.T) {
	t.Setenv("RECON_TEST_DIR", "/var/recon")
	got, err := expandShellPath("${RECON_TEST_DIR}/y")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/var/recon/y" {
		t.Fatalf("got=%q", got)
	}
}

func TestPathExpansion_DefaultModifier(t *testing.T) {
	os.Unsetenv("RECON_TEST_UNSET")
	got, err := expandShellPath("${RECON_TEST_UNSET:-/fallback}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/fallback" {
		t.Fatalf("got=%q", got)
	}
}

func TestPathExpansion_DashDefaultRespectsEmpty(t *testing.T) {
	t.Setenv("RECON_TEST_EMPTY", "")
	// `${VAR-default}` returns "" because VAR IS set (even though empty).
	got, err := expandShellPath("${RECON_TEST_EMPTY-/fallback}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("${VAR-default} with empty VAR=%q, want empty", got)
	}
	// `${VAR:-default}` returns the default because VAR is empty.
	got, err = expandShellPath("${RECON_TEST_EMPTY:-/fallback}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/fallback" {
		t.Fatalf("${VAR:-default} with empty VAR=%q, want /fallback", got)
	}
}

func TestPathExpansion_QuestionModifier(t *testing.T) {
	os.Unsetenv("RECON_TEST_REQUIRED")
	_, err := expandShellPath("${RECON_TEST_REQUIRED:?must be set}")
	if err == nil {
		t.Fatal("expected error for :? on unset var")
	}
	if !strings.Contains(err.Error(), "must be set") {
		t.Fatalf("err=%v, want substring 'must be set'", err)
	}
}

func TestPathExpansion_PlusAltModifier(t *testing.T) {
	t.Setenv("RECON_TEST_SET", "x")
	got, err := expandShellPath("${RECON_TEST_SET:+alt}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "alt" {
		t.Fatalf("${VAR:+alt} with set VAR=%q", got)
	}
}

// ---- Registry.Validator + Registry.Validate -----------------------

func TestRegistry_Validator_NilWhenNotSet(t *testing.T) {
	r := newRegistry(t)
	if r.Validator() != nil {
		t.Fatal("Validator() non-nil when no validator installed")
	}
}

func TestRegistry_Validator_ReturnsInstalled(t *testing.T) {
	schema := []byte(`{"type":"object"}`)
	r, err := New(WithSchema(schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if r.Validator() == nil {
		t.Fatal("Validator() nil after WithSchema")
	}
}

func TestRegistry_Validate_NoValidatorReturnsNil(t *testing.T) {
	r := newRegistry(t)
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate()=%v, want nil", err)
	}
}

func TestRegistry_Validate_ReturnsValidatorResult(t *testing.T) {
	schema := []byte(`{
		"type":"object","required":["port"],
		"properties":{"port":{"type":"integer"}}
	}`)
	r, err := New(WithSchema(schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if verr := r.Validate(); !errors.Is(verr, ErrValidation) {
		t.Fatalf("Validate err=%v, want ErrValidation", verr)
	}
}

func TestRegistry_Validate_ClosedRegistry(t *testing.T) {
	r, _ := New()
	_ = r.Close()
	if err := r.Validate(); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("err=%v, want ErrRegistryClosed", err)
	}
}

// ---- UnmarshalKey -------------------------------------------------

func TestRegistry_UnmarshalKey(t *testing.T) {
	type Server struct {
		Host string `recon:"host"`
		Port int    `recon:"port"`
	}
	r := newRegistry(t)
	_ = r.Set("server.host", "localhost")
	_ = r.Set("server.port", 8080)

	var s Server
	if err := r.UnmarshalKey("server", &s); err != nil {
		t.Fatalf("UnmarshalKey: %v", err)
	}
	if s.Host != "localhost" || s.Port != 8080 {
		t.Fatalf("s=%+v", s)
	}
}

func TestRegistry_UnmarshalKey_EmptyKeyIsBind(t *testing.T) {
	type C struct {
		K string `recon:"k"`
	}
	r := newRegistry(t)
	_ = r.Set("k", "v")
	var c C
	if err := r.UnmarshalKey("", &c); err != nil {
		t.Fatal(err)
	}
	if c.K != "v" {
		t.Fatalf("K=%q", c.K)
	}
}

// ---- PerSourceForPath ---------------------------------------------

func TestPerSourceForPath_EquivalentToPerSourceFor(t *testing.T) {
	src := NewMapSource("s", map[string]any{
		"server": map[string]any{"port": 8080},
	})
	r := newRegistry(t, WithSource(src))

	a, err := PerSourceFor[int](r, "server.port")
	if err != nil {
		t.Fatal(err)
	}
	b, err := PerSourceForPath[int](r, MakePath("server", "port"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Resolved.Value != b.Resolved.Value {
		t.Fatalf("Resolved=%v vs %v", a.Resolved, b.Resolved)
	}
}

// ---- FormatErrorColor ---------------------------------------------

func TestFormatErrorColor_EmitsANSI(t *testing.T) {
	err := &MissingRequiredError{Path: MakePath("k")}
	got := FormatErrorColor(nil, err)
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("got=%q, want ANSI sequences", got)
	}
}

func TestFormatError_DefaultNoColor(t *testing.T) {
	err := &MissingRequiredError{Path: MakePath("k")}
	got := FormatError(nil, err)
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("got=%q, want plain text", got)
	}
}

func TestFormatError_ExplicitColorTrue(t *testing.T) {
	err := &MissingRequiredError{Path: MakePath("k")}
	got := FormatError(nil, err, true)
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("got=%q, want ANSI sequences", got)
	}
}

// ---- snake-case field-name fallback -------------------------------

func TestBind_SnakeCaseFieldFallback(t *testing.T) {
	type C struct {
		ServerPort int // no tag; should fall back to "server_port"
	}
	r := newRegistry(t)
	_ = r.Set("server_port", 8080)
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.ServerPort != 8080 {
		t.Fatalf("ServerPort=%d", c.ServerPort)
	}
}
