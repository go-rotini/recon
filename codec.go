package recon

import (
	"maps"
	"slices"
	"strings"
	"sync"
)

// Codec parses and serializes a single file format. The interface is the
// seam every Source.Get / Save path uses — the bundled YAML / TOML / JSONC /
// JSON / Dotenv values implement it, and any user can plug in a third-party
// parser by implementing the same shape.
//
// # Decode contract
//
// Decode returns a nested map[string]any. Leaf values MUST be limited to:
// string, bool, int64, float64, time.Time, []any, map[string]any, or nil.
// Codecs are responsible for normalizing their native numeric types into
// int64 / float64 (e.g., a YAML decoder returning `int` should widen to
// int64). Decoders SHOULD NOT return implementation-specific types
// (yaml.Node, toml.Tree, json.RawMessage) — convert at the codec boundary so
// the registry only ever sees the documented leaf-value set.
//
// # Encode contract
//
// Encode is the inverse. The output SHOULD be byte-stable for the same input
// (deterministic key ordering, no trailing-whitespace drift) so round-trips
// are reproducible.
//
// # Name and Extensions
//
// Name is the canonical identifier (lowercase, single word) used by
// [Codecs.Register] for replacement-by-name and by [WithFileFormat] for
// explicit selection. Extensions is the set of lowercased file extensions
// (including the leading dot, e.g. []string{".yaml", ".yml"}); the
// [Codecs.ByExtension] resolver walks this set first, falling back to the
// package-wide [DetectFormat] map for ambiguous cases. Codecs that exist
// only for explicit use may return an empty Extensions slice.
type Codec interface {
	Name() string
	Extensions() []string
	Decode(data []byte) (map[string]any, error)
	Encode(v map[string]any) ([]byte, error)
}

// Codecs is the registry of [Codec] values a [Registry] consults for
// file-source decoding and Save encoding. The zero value is unusable;
// construct with [NewCodecs]. Codecs is safe for concurrent use.
//
// Registration is keyed by [Codec.Name]: a Register with a duplicate name
// replaces the prior entry (this is how a user-supplied codec shadows a
// bundled default — `recon.WithCodec(myYAML{})` simply re-registers under
// "yaml").
type Codecs struct {
	mu     sync.RWMutex
	byName map[string]Codec
}

// NewCodecs returns a Codecs registry pre-populated with the supplied
// initial codecs. Later codecs with the same Name() shadow earlier ones.
func NewCodecs(initial ...Codec) *Codecs {
	c := &Codecs{byName: make(map[string]Codec, len(initial))}
	for _, codec := range initial {
		c.Register(codec)
	}
	return c
}

// Register adds (or replaces) a codec keyed by its [Codec.Name]. nil codecs
// are silently ignored — pass [Codecs.Unregister] instead if you want to
// remove a default.
func (c *Codecs) Register(codec Codec) {
	if codec == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byName[codec.Name()] = codec
}

// Unregister removes the codec with the given name. Names that aren't
// registered are silently ignored.
func (c *Codecs) Unregister(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byName, name)
}

// ByName returns the codec registered under name. Lookup is case-sensitive
// against the canonical names ("yaml", "toml", "jsonc", "json", "dotenv",
// plus whatever a user has registered).
func (c *Codecs) ByName(name string) (Codec, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	codec, ok := c.byName[name]
	return codec, ok
}

// ByExtension returns the codec whose [Codec.Extensions] includes ext. The
// lookup is case-insensitive and ext SHOULD include the leading dot
// (".yaml"). When no registered codec advertises the extension, the
// package-wide [DetectFormat] fallback is consulted — if it maps the
// extension to a canonical name and that name is in the registry, the
// matching codec is returned.
func (c *Codecs) ByExtension(ext string) (Codec, bool) {
	ext = strings.ToLower(ext)
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, codec := range c.byName {
		if slices.Contains(codec.Extensions(), ext) {
			return codec, true
		}
	}
	if name, ok := extToFormat[ext]; ok {
		if codec, ok := c.byName[name]; ok {
			return codec, true
		}
	}
	return nil, false
}

// Names returns the registered codec names in unspecified order. Callers
// that need stable ordering should sort the result.
func (c *Codecs) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.byName))
	for name := range c.byName {
		out = append(out, name)
	}
	return out
}

// Clone returns an independent shallow copy of the registry. The codecs
// themselves are shared (they're stateless by contract), but the lookup map
// is fresh so a Register on the clone does not affect the original.
func (c *Codecs) Clone() *Codecs {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := &Codecs{byName: make(map[string]Codec, len(c.byName))}
	maps.Copy(out.byName, c.byName)
	return out
}
