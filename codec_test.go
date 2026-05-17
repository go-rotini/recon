package recon

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fixture is the shared multi-format payload the per-codec roundtrip
// tests decode. Type-shape is constrained to values every bundled codec
// can represent: string, float64, bool, nested map, slice of strings.
// Numeric int fidelity varies across codecs (JSON/YAML widen to float64,
// TOML preserves int64) — assertFixtureMatches accepts either.
var fixture = map[string]any{
	"server": map[string]any{
		"host":  "localhost",
		"port":  float64(8080),
		"debug": true,
	},
	"tags": []any{"alpha", "beta"},
}

// assertFixtureMatches inspects a decoded map against [fixture]. Loose
// numeric equivalence (int64 ≈ float64) keeps the assertion useful
// across codecs that differ in number representation. Structural
// equivalence — same keys, same nesting, same scalar values — is what
// the roundtrip check actually cares about.
func assertFixtureMatches(t *testing.T, got map[string]any) {
	t.Helper()
	server, ok := got["server"].(map[string]any)
	if !ok {
		t.Fatalf("server is %T, want map[string]any", got["server"])
	}
	if server["host"] != "localhost" {
		t.Fatalf("server.host=%v", server["host"])
	}
	switch p := server["port"].(type) {
	case float64:
		if p != 8080 {
			t.Fatalf("server.port=%v", p)
		}
	case int64:
		if p != 8080 {
			t.Fatalf("server.port=%v", p)
		}
	default:
		t.Fatalf("server.port is %T", server["port"])
	}
	if server["debug"] != true {
		t.Fatalf("server.debug=%v", server["debug"])
	}
	tags, ok := got["tags"].([]any)
	if !ok {
		t.Fatalf("tags is %T", got["tags"])
	}
	if len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Fatalf("tags=%v", tags)
	}
}

// fakeCodec is a Phase-2 test double — Phase 4 introduces real codecs.
type fakeCodec struct {
	name string
	exts []string
	dec  func([]byte) (map[string]any, error)
	enc  func(map[string]any) ([]byte, error)
}

func (f *fakeCodec) Name() string         { return f.name }
func (f *fakeCodec) Extensions() []string { return f.exts }
func (f *fakeCodec) Decode(b []byte) (map[string]any, error) {
	if f.dec == nil {
		return nil, errors.New("fakeCodec: no decoder")
	}
	return f.dec(b)
}

func (f *fakeCodec) Encode(v map[string]any) ([]byte, error) {
	if f.enc == nil {
		return nil, errors.New("fakeCodec: no encoder")
	}
	return f.enc(v)
}

func TestNewCodecs_Empty(t *testing.T) {
	c := NewCodecs()
	if len(c.Names()) != 0 {
		t.Errorf("fresh registry has names: %v", c.Names())
	}
}

func TestCodecs_Register(t *testing.T) {
	c := NewCodecs()
	y := &fakeCodec{name: "yaml", exts: []string{".yaml", ".yml"}}
	c.Register(y)

	got, ok := c.ByName("yaml")
	if !ok || got != y {
		t.Errorf("ByName(yaml) = (%v, %v)", got, ok)
	}
}

func TestCodecs_RegisterReplacesByName(t *testing.T) {
	// A second Register with the same Name() shadows the first — this is
	// how WithCodec(myYAML) replaces the bundled default.
	c := NewCodecs()
	first := &fakeCodec{name: "yaml", exts: []string{".yaml"}}
	second := &fakeCodec{name: "yaml", exts: []string{".yaml", ".yml"}}
	c.Register(first)
	c.Register(second)

	got, _ := c.ByName("yaml")
	if got != second {
		t.Errorf("Register should replace by Name(); got first=%v want second=%v", got == first, got == second)
	}
}

func TestCodecs_RegisterIgnoresNil(t *testing.T) {
	c := NewCodecs()
	c.Register(nil)
	if len(c.Names()) != 0 {
		t.Errorf("nil register added an entry")
	}
}

func TestCodecs_Unregister(t *testing.T) {
	c := NewCodecs(&fakeCodec{name: "yaml"})
	c.Unregister("yaml")
	if _, ok := c.ByName("yaml"); ok {
		t.Error("yaml still registered after Unregister")
	}
	// Unregister of unknown name is a no-op.
	c.Unregister("nope")
}

func TestCodecs_ByName_NotFound(t *testing.T) {
	c := NewCodecs()
	if _, ok := c.ByName("missing"); ok {
		t.Error("ByName(missing) should be false")
	}
}

func TestCodecs_ByExtension_FromCodec(t *testing.T) {
	// Codecs that advertise their own extensions resolve via Extensions().
	c := NewCodecs(&fakeCodec{name: "yaml", exts: []string{".yaml", ".yml"}})
	for _, ext := range []string{".yaml", ".yml", ".YML"} {
		t.Run(ext, func(t *testing.T) {
			if _, ok := c.ByExtension(ext); !ok {
				t.Errorf("ByExtension(%q) not found", ext)
			}
		})
	}
}

func TestCodecs_ByExtension_FromDetectFormatFallback(t *testing.T) {
	// A codec with no Extensions() still resolves via the package-wide
	// extToFormat map, by Name() match.
	c := NewCodecs(&fakeCodec{name: "yaml" /* no Extensions */})
	if _, ok := c.ByExtension(".yaml"); !ok {
		t.Error("DetectFormat fallback not used")
	}
}

func TestCodecs_ByExtension_NotFound(t *testing.T) {
	c := NewCodecs()
	if _, ok := c.ByExtension(".unknown"); ok {
		t.Error("ByExtension(unknown) should be false")
	}
}

func TestCodecs_Names(t *testing.T) {
	c := NewCodecs(
		&fakeCodec{name: "yaml"},
		&fakeCodec{name: "json"},
	)
	names := c.Names()
	slices.Sort(names)
	want := []string{"json", "yaml"}
	if !slices.Equal(names, want) {
		t.Errorf("Names() = %v, want %v", names, want)
	}
}

func TestCodecs_Clone(t *testing.T) {
	orig := NewCodecs(&fakeCodec{name: "yaml"})
	clone := orig.Clone()

	// Modifying the clone doesn't affect the original.
	clone.Register(&fakeCodec{name: "toml"})
	if _, ok := orig.ByName("toml"); ok {
		t.Error("Clone shares state with the original")
	}
	if _, ok := clone.ByName("yaml"); !ok {
		t.Error("Clone lost an inherited codec")
	}
}

// TestCodec_DecodeFailureContext checks every bundled codec produces an
// error string that names the codec ("recon: yaml decode: …") so a
// debugging grep can pinpoint which codec rejected a payload.
func TestCodec_DecodeFailureContext(t *testing.T) {
	cases := []struct {
		name string
		c    Codec
		bad  []byte
		want string
	}{
		{"json", JSON, []byte(`{not-json`), "recon: json"},
		{"jsonc", JSONC, []byte(`{not-json`), "recon: jsonc"},
		// YAML and TOML are tolerant of many inputs; use clearly broken
		// payloads they actually reject.
		{"yaml", YAML, []byte("\t\tnested:\n\tbad-indent"), "recon: yaml"},
		{"toml", TOML, []byte("=missing key"), "recon: toml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.c.Decode(tc.bad)
			if err == nil {
				t.Fatalf("expected decode error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestDefaultCodecs_RegistersAllBundled(t *testing.T) {
	c := DefaultCodecs()
	for _, name := range []string{
		FormatYAML, FormatTOML, FormatJSON, FormatJSONC, FormatDotenv,
	} {
		if _, ok := c.ByName(name); !ok {
			t.Errorf("DefaultCodecs missing %q", name)
		}
	}
}

func TestDefaultCodecs_ByExtension(t *testing.T) {
	cases := []struct {
		ext  string
		want string
	}{
		{".yaml", FormatYAML},
		{".YML", FormatYAML},
		{".toml", FormatTOML},
		{".json", FormatJSON},
		{".JSONC", FormatJSONC},
		{".env", FormatDotenv},
	}
	c := DefaultCodecs()
	for _, tc := range cases {
		codec, ok := c.ByExtension(tc.ext)
		if !ok {
			t.Errorf("ByExtension(%q) not found", tc.ext)
			continue
		}
		if codec.Name() != tc.want {
			t.Errorf("ByExtension(%q).Name=%q, want %q", tc.ext, codec.Name(), tc.want)
		}
	}
}

func TestCodecs_ConcurrentSafe(t *testing.T) {
	// The mutex on *Codecs must allow many concurrent ByName lookups while
	// Register / Unregister run.
	c := NewCodecs(&fakeCodec{name: "yaml"})
	var wg sync.WaitGroup
	const n = 50
	wg.Add(n * 3)
	for range n {
		go func() {
			defer wg.Done()
			c.ByName("yaml")
		}()
		go func() {
			defer wg.Done()
			c.Register(&fakeCodec{name: "tmp"})
		}()
		go func() {
			defer wg.Done()
			c.Unregister("tmp")
		}()
	}
	wg.Wait()
}
