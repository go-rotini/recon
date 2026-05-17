package recon

import "testing"

func TestTOMLCodec_DecodeEncodeRoundtrip(t *testing.T) {
	b, err := TOML.Encode(fixture)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := TOML.Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertFixtureMatches(t, got)
}

func TestTOMLCodec_EmptyInput(t *testing.T) {
	m, err := TOML.Decode(nil)
	if err != nil {
		t.Fatalf("Decode(nil): %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("Decode(nil)=%v, want empty", m)
	}
}

func TestTOMLCodec_NameAndExtensions(t *testing.T) {
	if TOML.Name() != FormatTOML {
		t.Fatalf("Name()=%q, want %q", TOML.Name(), FormatTOML)
	}
	exts := TOML.Extensions()
	if len(exts) != 1 || exts[0] != ".toml" {
		t.Fatalf("Extensions()=%v, want [.toml]", exts)
	}
}
