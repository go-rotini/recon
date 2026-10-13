package recon

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/yaml"
)

// decodePosition decodes data with c and returns the position and
// message a [*ParseError] built from the failure would carry.
func decodePosition(t *testing.T, c Codec, data string) (Position, string) {
	t.Helper()
	_, err := c.Decode([]byte(data))
	if err == nil {
		t.Fatalf("%s: decode of %q succeeded, want an error", c.Name(), data)
	}
	pe := newCodecParseError("cfg", "cfg", []byte(data), err)
	return pe.Position, pe.Msg
}

func TestCodecs_DuplicateKeysFail(t *testing.T) {
	cases := []struct {
		codec Codec
		data  string
		want  Position
	}{
		{JSON, "{\n  \"a\": 1,\n  \"a\": 2\n}", Position{Line: 3, Column: 3}},
		{JSON, "{\"s\": {\"b\": 1, \"b\": 2}}", Position{Line: 1, Column: 16}},
		{JSONC, "{\n  \"a\": 1,\n  \"a\": 2\n}", Position{Line: 3, Column: 3}},
		{TOML, "a = 1\nb = 2\na = 3\n", Position{Line: 3}},
	}
	for _, tc := range cases {
		pos, msg := decodePosition(t, tc.codec, tc.data)
		if pos.Line != tc.want.Line || (tc.want.Column != 0 && pos.Column != tc.want.Column) {
			t.Errorf("%s %q: position %v, want %v", tc.codec.Name(), tc.data, pos, tc.want)
		}
		if !strings.Contains(msg, "duplicate key") || strings.Contains(msg, "line") {
			t.Errorf("%s %q: message %q, want the duplicate key without a position prefix", tc.codec.Name(), tc.data, msg)
		}
	}
	if m, err := JSON.Decode([]byte(`{"a": {"b": 1}, "c": [1, {"d": null}], "e": 9007199254740993}`)); err != nil ||
		m["e"] != int64(9007199254740993) || m["c"].([]any)[1].(map[string]any)["d"] != nil {
		t.Errorf("JSON decode of a valid document = %#v, %v", m, err)
	}

	var probe any
	if yaml.UnmarshalWithOptions([]byte("a: 1\na: 2\n"), &probe, yaml.WithDisallowDuplicateKey()) == nil {
		t.Skip("the required yaml version does not report duplicate keys when decoding into any")
	}
	for _, data := range []string{"a: 1\na: 2\n", "s:\n  a: 1\n  a: 2\n"} {
		pos, _ := decodePosition(t, YAML, data)
		if pos.Line != strings.Count(data, "\n") {
			t.Errorf("yaml %q: position %v, want the last line", data, pos)
		}
	}
}

func TestCodecs_DocumentWithoutValueIsEmpty(t *testing.T) {
	cases := []struct {
		codec Codec
		data  string
	}{
		{YAML, "# all commented out\n# port: 8080\n"},
		{YAML, "   \n\n"},
		{YAML, "~\n"},
		{YAML, "---\n"},
		{YAML, "---\n...\n"},
		{JSONC, "// all commented out\n/* \"port\": 8080 */\n"},
		{JSONC, "\ufeff  \n"},
		{JSON, " \n\t\n"},
		{JSON, "\ufeff\n"},
	}
	for _, tc := range cases {
		m, err := tc.codec.Decode([]byte(tc.data))
		if err != nil || m == nil || len(m) != 0 {
			t.Errorf("%s %q: got %v, %v; want an empty map", tc.codec.Name(), tc.data, m, err)
		}
	}
	for _, tc := range []struct {
		codec Codec
		data  string
	}{{YAML, "- a\n"}, {JSONC, "// c\n[1]\n"}, {JSON, "[1]"}, {JSONC, "/* unterminated\n"}} {
		if _, err := tc.codec.Decode([]byte(tc.data)); err == nil {
			t.Errorf("%s %q: decoded, want an error", tc.codec.Name(), tc.data)
		}
	}
}

func TestYAML_SecondDocumentFails(t *testing.T) {
	pos, msg := decodePosition(t, YAML, "a: 1\n---\na: 2\nb: 3\n")
	if pos.Line < 2 || pos.Line > 3 || !strings.Contains(msg, "second document") {
		t.Errorf("got %v %q, want the second document's position", pos, msg)
	}
	for _, data := range []string{"a: 1\n---\n", "---\na: 1\n", "a: 1\n...\n", "a: 1\n--- ~\n"} {
		m, err := YAML.Decode([]byte(data))
		if err != nil || m["a"] != int64(1) {
			t.Errorf("%q: got %v, %v; want a=1", data, m, err)
		}
	}
}

func TestYAML_TypeErrorIsPositioned(t *testing.T) {
	err := yamlDecodeError(&yaml.TypeError{Errors: []string{"line 1, column 3: cannot use !!seq as a key"}})
	pe := newCodecParseError("cfg", "cfg", nil, err)
	pos, msg := pe.Position, pe.Msg
	if pos != (Position{Line: 1, Column: 3}) || msg != "cannot use !!seq as a key" {
		t.Errorf("got %v %q, want 1:N and a message without the position", pos, msg)
	}
}

func TestPosition_StringWithoutColumn(t *testing.T) {
	if got := (Position{Line: 3}).String(); got != "3" {
		t.Errorf("Position{Line: 3} = %q, want 3", got)
	}
	if got := (Position{Line: 3, Column: 4}).String(); got != "3:4" {
		t.Errorf("Position{3, 4} = %q, want 3:4", got)
	}
}

func TestBind_BoolWords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.env")
	if err := os.WriteFile(path, []byte("A=yes\nB=OFF\nC=True\nD=on\nE=t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := NewFileSource(path)
	if err != nil {
		t.Fatal(err)
	}
	r := newRegistry(t, WithoutWatch(), WithSource(src))
	var cfg struct {
		A bool `recon:"A"`
		B bool `recon:"B"`
		C bool `recon:"C"`
		D bool `recon:"D"`
		E bool `recon:"E"`
	}
	if err := r.Bind(&cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.A || cfg.B || !cfg.C || !cfg.D || !cfg.E {
		t.Errorf("got %+v", cfg)
	}
	var bad struct {
		X bool `recon:"X"`
	}
	r2 := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("m", map[string]any{"X": "maybe"})))
	if err := r2.Bind(&bad); !errors.Is(err, ErrCoercion) {
		t.Errorf("maybe: err %v, want ErrCoercion", err)
	}
}

func TestFileSource_UnreadableDirectoryIsNotMissing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory search permission is not enforced here")
	}
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	for _, optional := range []bool{false, true} {
		_, err := NewFileSource(path, WithOptional(optional))
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("optional=%v: err %v, want fs.ErrPermission", optional, err)
		}
	}
	if _, err := NewFileSource(filepath.Join(t.TempDir(), "absent.yaml"), WithOptional(true)); err != nil {
		t.Errorf("missing optional file: %v", err)
	}
	if _, err := NewFileSource(filepath.Join(t.TempDir(), "absent.yaml")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing required file: %v, want fs.ErrNotExist", err)
	}
}

func TestValidationError_UnwrapsSchemaError(t *testing.T) {
	v, err := NewJSONSchemaValidator([]byte(`{"properties": {"port": {"anyOf": [{"type": "integer"}, {"type": "null"}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	err = v.Validate(map[string]any{"port": "x"})
	se, ok := errors.AsType[*jsonschema.ValidationError](err)
	if !ok || len(se.Causes) == 0 {
		t.Fatalf("errors.As to *jsonschema.ValidationError: %v (%T), causes %v", ok, err, se)
	}
	ve, _ := errors.AsType[*ValidationError](err)
	ve.Secret = true
	if _, ok := errors.AsType[*jsonschema.ValidationError](ve); ok {
		t.Error("a secret ValidationError unwraps to the schema error, which may quote the value")
	}
}

func TestDotenv_TakesReconSizeCap(t *testing.T) {
	line := "A=" + strings.Repeat("x", 1021) + "\n"
	data := []byte(strings.Repeat(line, 12<<10))
	m, err := Dotenv.Decode(data)
	if err != nil || len(m["A"].(string)) != 1021 {
		t.Fatalf("12 MiB .env: %v", err)
	}
}

func TestExpandPath_OperatorFollowsName(t *testing.T) {
	t.Setenv("RECON_SET", "v")
	for in, want := range map[string]string{
		"${RECON_NOPE-a:-b}":   "a:-b",
		"${RECON_NOPE:-a-b}":   "a-b",
		"${RECON_SET+x:-y}":    "x:-y",
		"${RECON_SET:-a}/c":    "v/c",
		"${RECON_NOPE-}x":      "x",
		"${RECON_NOPE:+a-b}/d": "/d",
	} {
		got, err := expandShellPath(in)
		if err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", in, got, err, want)
		}
	}
}
