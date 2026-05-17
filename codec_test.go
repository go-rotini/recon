package recon

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

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
