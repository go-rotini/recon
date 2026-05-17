package recon

import (
	"errors"
	"testing"
)

func TestJSONCodec_DecodeEncodeRoundtrip(t *testing.T) {
	b, err := JSON.Encode(fixture)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := JSON.Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertFixtureMatches(t, got)
}

func TestJSONCodec_RejectsNonObjectRoot(t *testing.T) {
	_, err := JSON.Decode([]byte(`[1,2,3]`))
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err=%v, want wrap of ErrUnsupportedFormat", err)
	}
}

func TestJSONCodec_EmptyInput(t *testing.T) {
	m, err := JSON.Decode(nil)
	if err != nil {
		t.Fatalf("Decode(nil): %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("Decode(nil)=%v, want empty", m)
	}
}

func TestJSONCodec_NameAndExtensions(t *testing.T) {
	if JSON.Name() != FormatJSON {
		t.Fatalf("Name()=%q, want %q", JSON.Name(), FormatJSON)
	}
	exts := JSON.Extensions()
	if len(exts) != 1 || exts[0] != ".json" {
		t.Fatalf("Extensions()=%v, want [.json]", exts)
	}
}
