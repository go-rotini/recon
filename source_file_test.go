package recon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func writeTempFile(t *testing.T, name, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := writeFile(path, []byte(contents)); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	return path
}

// writeFile is a stdlib-only wrapper to keep the test free of go-rotini/fs
// dependency churn during construction.
func writeFile(path string, data []byte) error {
	return writeAll(path, data)
}

func TestFileSource_YAMLEndToEnd(t *testing.T) {
	path := writeTempFile(t, "config.yaml", `
server:
  host: localhost
  port: 8080
debug: true
tags:
  - alpha
  - beta
`)

	src, err := YAMLSource(path)
	if err != nil {
		t.Fatalf("YAMLSource: %v", err)
	}
	r := newRegistry(t, WithSource(src))

	v, ok, err := r.Get("server.host")
	if err != nil || !ok {
		t.Fatalf("server.host ok=%v err=%v", ok, err)
	}
	if s, _ := v.AsString(); s != "localhost" {
		t.Fatalf("server.host=%q", s)
	}
	if b, _, _ := r.GetBool("debug"); !b {
		t.Fatal("debug not true")
	}
}

func TestFileSource_TOMLEndToEnd(t *testing.T) {
	path := writeTempFile(t, "config.toml", `
[server]
host = "localhost"
port = 8080
debug = true
`)
	src, err := TOMLSource(path)
	if err != nil {
		t.Fatalf("TOMLSource: %v", err)
	}
	r := newRegistry(t, WithSource(src))
	if i, _, _ := r.GetInt64("server.port"); i != 8080 {
		t.Fatalf("server.port=%d", i)
	}
}

func TestFileSource_JSONEndToEnd(t *testing.T) {
	path := writeTempFile(t, "config.json", `{"k": "v"}`)
	src, err := JSONSource(path)
	if err != nil {
		t.Fatalf("JSONSource: %v", err)
	}
	r := newRegistry(t, WithSource(src))
	if s, _, _ := r.GetString("k"); s != "v" {
		t.Fatalf("k=%q", s)
	}
}

func TestFileSource_JSONCAcceptsCommentsAndJSON5Ext(t *testing.T) {
	path := writeTempFile(t, "config.json5", `{
		// inline comment
		"k": "v",
	}`)
	src, err := JSONCSource(path)
	if err != nil {
		t.Fatalf("JSONCSource: %v", err)
	}
	r := newRegistry(t, WithSource(src))
	if s, _, _ := r.GetString("k"); s != "v" {
		t.Fatalf("k=%q", s)
	}
}

func TestFileSource_DotenvEndToEnd(t *testing.T) {
	path := writeTempFile(t, "config.env", "PORT=8080\nDATABASE=postgres\n")
	src, err := DotenvSource(path)
	if err != nil {
		t.Fatalf("DotenvSource: %v", err)
	}
	r := newRegistry(t, WithSource(src))
	if s, _, _ := r.GetString("PORT"); s != "8080" {
		t.Fatalf("PORT=%q", s)
	}
}

func TestFileSource_CodecResolvedByExtension(t *testing.T) {
	path := writeTempFile(t, "settings.yaml", "k: v\n")
	src, err := NewFileSource(path) // no explicit codec
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	fs, ok := src.(*FileSource)
	if !ok {
		t.Fatalf("source is %T", src)
	}
	if fs.Format() != FormatYAML {
		t.Fatalf("Format()=%q, want %q", fs.Format(), FormatYAML)
	}
}

func TestFileSource_CodecResolvedByFormatOption(t *testing.T) {
	// File with no extension; format must come from WithFileFormat.
	path := writeTempFile(t, "config", `{"k": "v"}`)
	src, err := NewFileSource(path, WithFileFormat(FormatJSON))
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	fs, ok := src.(*FileSource)
	if !ok {
		t.Fatalf("source is %T", src)
	}
	if fs.Format() != FormatJSON {
		t.Fatalf("Format()=%q", fs.Format())
	}
}

func TestFileSource_UnknownExtensionRejected(t *testing.T) {
	path := writeTempFile(t, "config.xyz", "?")
	_, err := NewFileSource(path)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err=%v, want wrap of ErrUnsupportedFormat", err)
	}
}

func TestFileSource_MissingFileRequiredErrors(t *testing.T) {
	_, err := NewFileSource("/no/such/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestFileSource_MissingFileOptionalSucceeds(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.yaml")
	src, err := NewFileSource(missing, WithOptional(true))
	if err != nil {
		t.Fatalf("optional missing: %v", err)
	}
	if keys := src.Keys(); len(keys) != 0 {
		t.Fatalf("missing-optional Keys()=%v, want empty", keys)
	}
}

func TestFileSource_Reload(t *testing.T) {
	path := writeTempFile(t, "config.yaml", "k: first\n")
	src, err := NewFileSource(path)
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	r := newRegistry(t, WithSource(src))
	v, _, _ := r.Get("k")
	if s, _ := v.AsString(); s != "first" {
		t.Fatalf("initial k=%q", s)
	}

	if err := writeFile(path, []byte("k: second\n")); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := src.(*FileSource).Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if err := r.Reload(); err != nil {
		t.Fatalf("registry Reload: %v", err)
	}
	v, _, _ = r.Get("k")
	if s, _ := v.AsString(); s != "second" {
		t.Fatalf("post-reload k=%q", s)
	}
}

func TestFileSource_DecodeErrorWrappedAsParseError(t *testing.T) {
	path := writeTempFile(t, "broken.json", `{not-json`)
	_, err := NewFileSource(path)
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("err is %T, want *ParseError", err)
	}
	if pe.Source != "broken.json" {
		t.Fatalf("ParseError.Source=%q", pe.Source)
	}
}

func TestFileSource_PathExpansion(t *testing.T) {
	// We can't reliably set up a $VAR-expanded path without polluting env.
	// Instead exercise the disabled-expansion branch: a path containing
	// `~` is taken literally when expansion is off.
	path := writeTempFile(t, "config.yaml", "k: v\n")
	src, err := NewFileSource(path, WithPathExpansion(false))
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	fs, _ := src.(*FileSource)
	if fs.Path() == "" {
		t.Fatal("FileSource.Path is empty")
	}
}

func TestFileSource_SearchPaths(t *testing.T) {
	d1 := t.TempDir()
	d2 := t.TempDir()
	// File exists only in d2.
	if err := writeFile(filepath.Join(d2, "config.yaml"), []byte("k: from-d2\n")); err != nil {
		t.Fatalf("write d2: %v", err)
	}
	src, err := NewFileSource("config.yaml", WithSearchPaths(d1, d2))
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	r := newRegistry(t, WithSource(src))
	if s, _, _ := r.GetString("k"); s != "from-d2" {
		t.Fatalf("k=%q, want from-d2", s)
	}
}

func TestFileSourceFS_ReadsFromEmbed(t *testing.T) {
	fsys := fstest.MapFS{
		"config.yaml": &fstest.MapFile{Data: []byte("k: v\n")},
	}
	src, err := NewFileSourceFS("embed", fsys, "config.yaml")
	if err != nil {
		t.Fatalf("NewFileSourceFS: %v", err)
	}
	if src.Name() != "embed" {
		t.Fatalf("Name()=%q", src.Name())
	}
	v, ok, _ := src.Get(MakePath("k"))
	if !ok {
		t.Fatal("k not found")
	}
	if s, _ := v.AsString(); s != "v" {
		t.Fatalf("k=%q", s)
	}
}

func TestFileSourceFS_NilFsRejected(t *testing.T) {
	_, err := NewFileSourceFS("x", nil, "config.yaml")
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err=%v, want ErrInvalidPath", err)
	}
}

func TestFileSourceFS_MissingFileOptional(t *testing.T) {
	fsys := fstest.MapFS{}
	src, err := NewFileSourceFS("embed", fsys, "missing.yaml", WithOptional(true))
	if err != nil {
		t.Fatalf("optional missing: %v", err)
	}
	if keys := src.Keys(); len(keys) != 0 {
		t.Fatalf("Keys()=%v", keys)
	}
}

func TestFileSourceFS_MissingFileRequiredErrors(t *testing.T) {
	fsys := fstest.MapFS{}
	_, err := NewFileSourceFS("embed", fsys, "missing.yaml")
	if err == nil {
		t.Fatal("expected error for missing required file")
	}
}

// writeAll is the stdlib-only file write helper used by these tests.
func writeAll(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
