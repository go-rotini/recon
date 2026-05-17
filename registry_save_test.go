package recon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSave_RequiresFormatForWriter(t *testing.T) {
	r := newRegistry(t)
	var buf bytes.Buffer
	err := r.Save(&buf)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err=%v, want ErrUnsupportedFormat", err)
	}
}

func TestSave_JSONEncode(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("server.host", "localhost")
	_ = r.Set("server.port", 8080)

	out, err := r.SaveString(WithSaveFormat(FormatJSON))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Round-trip via the JSON codec to assert structural equivalence
	// without depending on key ordering.
	decoded, derr := JSON.Decode([]byte(out))
	if derr != nil {
		t.Fatalf("re-decode: %v", derr)
	}
	server := decoded["server"].(map[string]any)
	if server["host"] != "localhost" {
		t.Fatalf("server.host=%v", server["host"])
	}
	// JSON widens to float64.
	if server["port"] != float64(8080) {
		t.Fatalf("server.port=%v", server["port"])
	}
}

func TestSave_YAMLEncode(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("k", "v")
	out, err := r.SaveString(WithSaveFormat(FormatYAML))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !strings.Contains(out, "k") || !strings.Contains(out, "v") {
		t.Fatalf("YAML output=%q", out)
	}
	// Round-trip through the YAML codec.
	decoded, derr := YAML.Decode([]byte(out))
	if derr != nil {
		t.Fatalf("re-decode: %v", derr)
	}
	if decoded["k"] != "v" {
		t.Fatalf("decoded[k]=%v", decoded["k"])
	}
}

func TestSave_TOMLEncode(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("server.port", 8080)
	out, err := r.SaveString(WithSaveFormat(FormatTOML))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	decoded, derr := TOML.Decode([]byte(out))
	if derr != nil {
		t.Fatalf("re-decode: %v", derr)
	}
	server := decoded["server"].(map[string]any)
	// TOML preserves int64.
	if server["port"] != int64(8080) {
		t.Fatalf("server.port=%v (%T)", server["port"], server["port"])
	}
}

func TestSave_CrossFormatRoundtrip(t *testing.T) {
	// Set values into a registry, Save as one format, decode with
	// the same codec, re-encode through a second codec, decode
	// again — values must survive the dance.
	r := newRegistry(t)
	_ = r.Set("server.host", "h")
	_ = r.Set("server.port", 8080)

	yamlBytes, err := r.SaveString(WithSaveFormat(FormatYAML))
	if err != nil {
		t.Fatalf("YAML save: %v", err)
	}
	intermediate, err := YAML.Decode([]byte(yamlBytes))
	if err != nil {
		t.Fatalf("decode YAML: %v", err)
	}
	jsonBytes, err := JSON.Encode(intermediate)
	if err != nil {
		t.Fatalf("re-encode JSON: %v", err)
	}
	decoded, err := JSON.Decode(jsonBytes)
	if err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	server := decoded["server"].(map[string]any)
	if server["host"] != "h" {
		t.Fatalf("server.host=%v", server["host"])
	}
}

func TestSave_RedactsSecretsByDefault(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("token", "hunter2")
	r.MarkSecret("token")

	out, err := r.SaveString(WithSaveFormat(FormatJSON))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if strings.Contains(out, "hunter2") {
		t.Fatalf("Save leaked secret: %s", out)
	}
	// The redactor's value ("***" by default) IS in the output.
	if !strings.Contains(out, "***") {
		t.Fatalf("redaction marker missing: %s", out)
	}
}

func TestSave_IncludeSecretsEmitsVerbatim(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("token", "hunter2")
	r.MarkSecret("token")

	out, err := r.SaveString(
		WithSaveFormat(FormatJSON),
		WithSaveIncludeSecrets(),
	)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !strings.Contains(out, "hunter2") {
		t.Fatalf("Save did not include secret with WithSaveIncludeSecrets: %s", out)
	}
}

func TestSave_OmitsDefaultsByDefault(t *testing.T) {
	r := newRegistry(t)
	_ = r.SetDefault("only-default", "d")
	_ = r.Set("explicit", "x")

	out, err := r.SaveString(WithSaveFormat(FormatJSON))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if strings.Contains(out, "only-default") {
		t.Fatalf("default-only key in output by default: %s", out)
	}
	if !strings.Contains(out, "explicit") {
		t.Fatalf("explicit key missing: %s", out)
	}
}

func TestSave_IncludeDefaultsEmitsThem(t *testing.T) {
	r := newRegistry(t)
	_ = r.SetDefault("only-default", "d")

	out, err := r.SaveString(
		WithSaveFormat(FormatJSON),
		WithSaveIncludeDefaults(),
	)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !strings.Contains(out, "only-default") {
		t.Fatalf("default-only key missing under WithSaveIncludeDefaults: %s", out)
	}
}

func TestSave_OnlyPrefixFiltersAndStripsPrefix(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("server.host", "h")
	_ = r.Set("server.port", 8080)
	_ = r.Set("db.dsn", "x")

	out, err := r.SaveString(
		WithSaveFormat(FormatJSON),
		WithSaveOnly("server"),
	)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if strings.Contains(out, "db") || strings.Contains(out, "dsn") {
		t.Fatalf("non-server keys leaked: %s", out)
	}
	// Decoded map's root keys should be `host` / `port`, with the
	// `server.` prefix stripped.
	decoded, _ := JSON.Decode([]byte(out))
	if _, ok := decoded["host"]; !ok {
		t.Fatalf("host missing after prefix strip: %v", decoded)
	}
	if _, ok := decoded["port"]; !ok {
		t.Fatalf("port missing after prefix strip: %v", decoded)
	}
}

func TestSave_UnknownFormatRejected(t *testing.T) {
	r := newRegistry(t)
	var buf bytes.Buffer
	err := r.Save(&buf, WithSaveFormat("xml"))
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err=%v, want ErrUnsupportedFormat", err)
	}
}

func TestSave_ClosedRegistryRejected(t *testing.T) {
	r, _ := New()
	_ = r.Close()
	var buf bytes.Buffer
	err := r.Save(&buf, WithSaveFormat(FormatJSON))
	if !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("err=%v, want ErrRegistryClosed", err)
	}
}

func TestSaveTo_DetectsFormatByExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")

	r := newRegistry(t)
	_ = r.Set("k", "v")
	if err := r.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	decoded, _ := YAML.Decode(bytes)
	if decoded["k"] != "v" {
		t.Fatalf("decoded=%v", decoded)
	}
}

func TestSaveTo_UnknownExtensionRequiresFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.xyz")
	r := newRegistry(t)
	err := r.SaveTo(path)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err=%v, want ErrUnsupportedFormat", err)
	}
}

func TestSaveTo_AtomicRename(t *testing.T) {
	// A SaveTo over an existing file replaces it atomically. The
	// observable assertion: after a successful SaveTo, the file
	// contains exactly the new payload (no truncation, no partial
	// writes from a concurrent reader's perspective).
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	if err := os.WriteFile(path, []byte(`{"old":true}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := newRegistry(t)
	_ = r.Set("new", "yes")
	if err := r.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	decoded, _ := JSON.Decode(bytes)
	if decoded["new"] != "yes" {
		t.Fatalf("new key missing after rewrite: %v", decoded)
	}
	if _, ok := decoded["old"]; ok {
		t.Fatalf("old key survived rewrite: %v", decoded)
	}
}

func TestSave_NestedMapsRoundTrip(t *testing.T) {
	// A registry with a deeply-nested value Set via map literal
	// must encode and decode through Save without flattening.
	r := newRegistry(t)
	_ = r.Set("app", map[string]any{
		"db": map[string]any{
			"host": "localhost",
			"port": 5432,
		},
	})
	out, err := r.SaveString(WithSaveFormat(FormatJSON))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	decoded, _ := JSON.Decode([]byte(out))
	app := decoded["app"].(map[string]any)
	db := app["db"].(map[string]any)
	if db["host"] != "localhost" {
		t.Fatalf("db.host=%v", db["host"])
	}
}
