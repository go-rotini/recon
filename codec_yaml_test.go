package recon

import "testing"

func TestYAMLCodec_DecodeEncodeRoundtrip(t *testing.T) {
	b, err := YAML.Encode(fixture)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := YAML.Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertFixtureMatches(t, got)
}

func TestYAMLCodec_EmptyInput(t *testing.T) {
	m, err := YAML.Decode(nil)
	if err != nil {
		t.Fatalf("Decode(nil): %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("Decode(nil)=%v, want empty", m)
	}
}

func TestYAMLCodec_NameAndExtensions(t *testing.T) {
	if YAML.Name() != FormatYAML {
		t.Fatalf("Name()=%q, want %q", YAML.Name(), FormatYAML)
	}
	exts := YAML.Extensions()
	if len(exts) != 2 {
		t.Fatalf("Extensions()=%v, want 2 entries", exts)
	}
}
