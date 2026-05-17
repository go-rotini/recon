package recon

import (
	"errors"
	"testing"
)

func TestStdinSource_RejectsUnknownFormat(t *testing.T) {
	_, err := NewStdinSource("xyz")
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err=%v, want wrap of ErrUnsupportedFormat", err)
	}
}

func TestStdinSource_RejectsEmptyFormatWithoutExplicitCodec(t *testing.T) {
	_, err := NewStdinSource("")
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err=%v, want wrap of ErrUnsupportedFormat", err)
	}
}

func TestStdinSource_AcceptsExplicitCodecWithBlankFormat(t *testing.T) {
	// With WithStdinCodec(JSON), the format string is unused.
	// Construction succeeds even when stdin is a TTY (returns an empty
	// source per the TTY-safety note on NewStdinSource).
	src, err := NewStdinSource("", WithStdinCodec(JSON))
	if err != nil {
		t.Fatalf("NewStdinSource: %v", err)
	}
	if src.Name() != "stdin" {
		t.Fatalf("Name()=%q", src.Name())
	}
}

func TestStdinSource_FormatLookup(t *testing.T) {
	src, err := NewStdinSource(FormatYAML)
	if err != nil {
		t.Fatalf("NewStdinSource(yaml): %v", err)
	}
	if src.Name() != "stdin" {
		t.Fatalf("Name()=%q", src.Name())
	}
}
