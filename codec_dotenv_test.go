package recon

import (
	"errors"
	"testing"
)

func TestDotenvCodec_FlatRoundtrip(t *testing.T) {
	src := map[string]any{
		"PORT":     "8080",
		"DATABASE": "postgres://x",
	}
	b, err := Dotenv.Encode(src)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Dotenv.Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for k, want := range src {
		if got[k] != want {
			t.Fatalf("%s=%v, want %v", k, got[k], want)
		}
	}
}

func TestDotenvCodec_RejectsNestedOnEncode(t *testing.T) {
	bad := map[string]any{"server": map[string]any{"port": 8080}}
	_, err := Dotenv.Encode(bad)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err=%v, want wrap of ErrUnsupportedFormat", err)
	}
}

func TestDotenvCodec_EmptyInput(t *testing.T) {
	m, err := Dotenv.Decode(nil)
	if err != nil {
		t.Fatalf("Decode(nil): %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("Decode(nil)=%v, want empty", m)
	}
}

func TestDotenvCodec_NameAndExtensions(t *testing.T) {
	if Dotenv.Name() != FormatDotenv {
		t.Fatalf("Name()=%q, want %q", Dotenv.Name(), FormatDotenv)
	}
	exts := Dotenv.Extensions()
	if len(exts) != 1 || exts[0] != ".env" {
		t.Fatalf("Extensions()=%v, want [.env]", exts)
	}
}
