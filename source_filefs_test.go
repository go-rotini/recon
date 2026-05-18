package recon

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestFileSourceFS_BasicRead(t *testing.T) {
	fsys := fstest.MapFS{
		"config.yaml": &fstest.MapFile{
			Data: []byte("server:\n  port: 8080\n"),
		},
	}
	src, err := NewFileSourceFS("embedded", fsys, "config.yaml")
	if err != nil {
		t.Fatalf("NewFileSourceFS: %v", err)
	}
	defer func() { _ = src.Close() }()

	v, ok, err := src.Get(ParsePath("server.port"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got, _ := v.AsInt64(); got != 8080 {
		t.Fatalf("got %d, want 8080", got)
	}
}

func TestFileSourceFS_PathAndFormat(t *testing.T) {
	fsys := fstest.MapFS{
		"app/config.yaml": &fstest.MapFile{Data: []byte("k: v\n")},
	}
	src, err := NewFileSourceFS("embedded", fsys, "app/config.yaml")
	if err != nil {
		t.Fatalf("NewFileSourceFS: %v", err)
	}
	defer func() { _ = src.Close() }()

	ffs, ok := src.(*FileSourceFS)
	if !ok {
		t.Fatal("expected *FileSourceFS")
	}
	if ffs.Path() != "app/config.yaml" {
		t.Fatalf("Path() = %q, want app/config.yaml", ffs.Path())
	}
	if ffs.Format() != FormatYAML {
		t.Fatalf("Format() = %q, want yaml", ffs.Format())
	}
}

func TestFileSourceFS_LeadingSlashStripped(t *testing.T) {
	// fs.FS paths must be relative; the constructor strips a leading
	// slash so callers can write "/etc/.../config.yaml" in either FS.
	fsys := fstest.MapFS{
		"etc/myapp/config.yaml": &fstest.MapFile{Data: []byte("k: v\n")},
	}
	src, err := NewFileSourceFS("embedded", fsys, "/etc/myapp/config.yaml")
	if err != nil {
		t.Fatalf("NewFileSourceFS: %v", err)
	}
	defer func() { _ = src.Close() }()

	v, ok, err := src.Get(ParsePath("k"))
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got, _ := v.AsString(); got != "v" {
		t.Fatalf("got %q, want v", got)
	}
}

func TestFileSourceFS_MissingNonOptionalErrors(t *testing.T) {
	fsys := fstest.MapFS{}
	_, err := NewFileSourceFS("embedded", fsys, "missing.yaml")
	if err == nil {
		t.Fatal("expected error for missing required file")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want wrapping fs.ErrNotExist", err)
	}
}

func TestFileSourceFS_MissingOptionalSucceeds(t *testing.T) {
	fsys := fstest.MapFS{}
	src, err := NewFileSourceFS(
		"embedded",
		fsys,
		"missing.yaml",
		WithOptional(true),
		WithFileFormat(FormatYAML),
	)
	if err != nil {
		t.Fatalf("NewFileSourceFS (optional): %v", err)
	}
	defer func() { _ = src.Close() }()

	if got := src.Keys(); len(got) != 0 {
		t.Fatalf("expected empty keys for missing optional, got %v", got)
	}
}

func TestFileSourceFS_NilFSRejected(t *testing.T) {
	_, err := NewFileSourceFS("embedded", nil, "foo.yaml")
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err = %v, want wrapping ErrInvalidPath", err)
	}
}

func TestFileSourceFS_EmptyNameRejected(t *testing.T) {
	fsys := fstest.MapFS{}
	_, err := NewFileSourceFS("", fsys, "foo.yaml")
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err = %v, want wrapping ErrInvalidPath", err)
	}
}

func TestFileSourceFS_UnknownExtensionRejected(t *testing.T) {
	fsys := fstest.MapFS{
		"config.unknown": &fstest.MapFile{Data: []byte("x")},
	}
	_, err := NewFileSourceFS("embedded", fsys, "config.unknown")
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want wrapping ErrUnsupportedFormat", err)
	}
}

func TestFileSourceFS_DecodeErrorSurfaces(t *testing.T) {
	fsys := fstest.MapFS{
		"bad.json": &fstest.MapFile{Data: []byte("{not valid json")},
	}
	_, err := NewFileSourceFS("embedded", fsys, "bad.json")
	if err == nil {
		t.Fatal("expected ParseError for invalid JSON")
	}
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *ParseError", err)
	}
	if pe.Source != "embedded" {
		t.Fatalf("ParseError.Source = %q, want embedded", pe.Source)
	}
}

func TestFileSourceFS_WithFileFormatOverridesExtension(t *testing.T) {
	// "data.txt" extension would not resolve a codec; WithFileFormat
	// pins it explicitly.
	fsys := fstest.MapFS{
		"data.txt": &fstest.MapFile{Data: []byte(`{"port": 8080}`)},
	}
	src, err := NewFileSourceFS("embedded", fsys, "data.txt", WithFileFormat(FormatJSON))
	if err != nil {
		t.Fatalf("NewFileSourceFS: %v", err)
	}
	defer func() { _ = src.Close() }()

	v, ok, err := src.Get(ParsePath("port"))
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got, _ := v.AsFloat64(); got != 8080 {
		t.Fatalf("got %v, want 8080", got)
	}
}
