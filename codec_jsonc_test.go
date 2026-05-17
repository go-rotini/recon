package recon

import "testing"

func TestJSONCCodec_AcceptsCommentsAndTrailingCommas(t *testing.T) {
	data := []byte(`{
		// inline comment
		"k": 1,
		"nested": { "a": true, },
	}`)
	m, err := JSONC.Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if m["k"] != float64(1) {
		t.Fatalf("k=%v, want 1", m["k"])
	}
	sub, ok := m["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested is %T", m["nested"])
	}
	if sub["a"] != true {
		t.Fatalf("nested.a=%v", sub["a"])
	}
}

func TestJSONCCodec_DecodeEncodeRoundtrip(t *testing.T) {
	b, err := JSONC.Encode(fixture)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := JSONC.Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertFixtureMatches(t, got)
}

func TestJSONCCodec_NameAndExtensions(t *testing.T) {
	if JSONC.Name() != FormatJSONC {
		t.Fatalf("Name()=%q, want %q", JSONC.Name(), FormatJSONC)
	}
	exts := JSONC.Extensions()
	if len(exts) != 2 {
		t.Fatalf("Extensions()=%v, want 2 entries (.jsonc and .json5)", exts)
	}
}
