package recon

import (
	"errors"
	"slices"
	"testing"
)

func TestNewBufferSource_DecodesViaCodec(t *testing.T) {
	// JSON decodes integers as float64 — the test uses string and float
	// payloads to stay independent of int-coercion rules under tests.
	data := []byte(`{"server": {"port": 8080, "host": "localhost"}}`)
	s, err := NewBufferSource("buf", "json", data, WithBufferCodec(jsonTestCodec{}))
	if err != nil {
		t.Fatalf("NewBufferSource: %v", err)
	}
	if got := s.Name(); got != "buf" {
		t.Fatalf("Name() = %q, want %q", got, "buf")
	}
	v, ok, err := s.Get(MakePath("server", "host"))
	if err != nil || !ok {
		t.Fatalf("Get(server.host) ok=%v err=%v", ok, err)
	}
	host, err := v.AsString()
	if err != nil {
		t.Fatalf("AsString: %v", err)
	}
	if host != "localhost" {
		t.Fatalf("server.host = %q, want localhost", host)
	}
	v, ok, err = s.Get(MakePath("server", "port"))
	if err != nil || !ok {
		t.Fatalf("Get(server.port) ok=%v err=%v", ok, err)
	}
	f, err := v.AsFloat64()
	if err != nil {
		t.Fatalf("AsFloat64: %v", err)
	}
	if f != 8080 {
		t.Fatalf("server.port = %v, want 8080", f)
	}

	keys := s.Keys()
	got := make([]string, len(keys))
	for i, p := range keys {
		got[i] = p.String()
	}
	want := []string{"server.host", "server.port"}
	if !slices.Equal(got, want) {
		t.Fatalf("Keys() = %v, want %v", got, want)
	}
}

func TestNewBufferSource_MissingCodec(t *testing.T) {
	_, err := NewBufferSource("buf", "json", []byte(`{}`))
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err=%v, want wrap of ErrUnsupportedFormat", err)
	}
}

func TestNewBufferSource_DecodeFailure(t *testing.T) {
	bad := []byte(`{`)
	_, err := NewBufferSource("buf", "json", bad, WithBufferCodec(jsonTestCodec{}))
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
	var pErr *ParseError
	if !errors.As(err, &pErr) {
		t.Fatalf("err is %T, want *ParseError (%v)", err, err)
	}
	if pErr.Source != "buf" {
		t.Fatalf("ParseError.Source = %q, want %q", pErr.Source, "buf")
	}
}

func TestNewBufferSource_FormatAndCodecAccessors(t *testing.T) {
	s, err := NewBufferSource("buf", "json", []byte(`{}`), WithBufferCodec(jsonTestCodec{}))
	if err != nil {
		t.Fatal(err)
	}
	bs, ok := s.(*BufferSource)
	if !ok {
		t.Fatalf("Source is %T, want *BufferSource", s)
	}
	if got := bs.Format(); got != "json" {
		t.Fatalf("Format() = %q, want %q", got, "json")
	}
	if bs.Codec() == nil {
		t.Fatal("Codec() returned nil")
	}
}

func TestNewBufferSource_EmptyData(t *testing.T) {
	s, err := NewBufferSource("buf", "json", nil, WithBufferCodec(jsonTestCodec{}))
	if err != nil {
		t.Fatalf("NewBufferSource(nil data): %v", err)
	}
	if keys := s.Keys(); len(keys) != 0 {
		t.Fatalf("Keys() on empty buffer = %v, want empty", keys)
	}
}
