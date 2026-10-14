package recon

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

// Codec parses and serializes a single file format. The bundled
// YAML / TOML / JSONC / JSON / Dotenv values implement it; users plug
// in third-party parsers by implementing the same shape.
//
// Decode returns a nested map[string]any whose leaves are normally
// string, bool, int64, float64, time.Time, []any, map[string]any, or
// nil. Codecs should widen native numeric types and convert
// implementation-specific types (yaml.Node, etc.) at the boundary. The
// registry also takes other leaves as [NewValue] does: other integer
// and float types are widened (a uint64 beyond int64 becomes its
// decimal text), and any other value becomes its fmt.Sprint text. The
// bundled YAML codec returns such a uint64, and the bundled TOML codec
// its local date, date-time and time types, whose text is the TOML
// form ("2024-01-02", "2024-01-02T03:04:05", "03:04:05").
//
// Encode is the inverse and should be byte-stable for the same input
// (deterministic key ordering, no whitespace drift) so round-trips
// reproduce.
//
// Name is the canonical lowercase identifier used by [Codecs.Register]
// and [WithFileFormat]. Extensions is the lowercased set (including
// the leading dot) consulted by [Codecs.ByExtension].
type Codec interface {
	Name() string
	Extensions() []string
	Decode(data []byte) (map[string]any, error)
	Encode(v map[string]any) ([]byte, error)
}

// Codecs is a registry of [Codec] values. The zero value is unusable;
// construct with [NewCodecs]. Safe for concurrent use.
//
// Registration is keyed by [Codec.Name]: a Register with a duplicate
// name replaces the prior entry, letting a user-supplied codec shadow
// a bundled default.
type Codecs struct {
	mu     sync.RWMutex
	byName map[string]Codec

	// order records when each name was last registered, so
	// [Codecs.ByExtension] can prefer the most recent claimant.
	order map[string]uint64
	next  uint64
}

// NewCodecs returns a [Codecs] pre-populated with initial. Later
// entries with the same Name shadow earlier ones.
func NewCodecs(initial ...Codec) *Codecs {
	c := &Codecs{byName: make(map[string]Codec, len(initial)), order: make(map[string]uint64, len(initial))}
	for _, codec := range initial {
		c.Register(codec)
	}
	return c
}

// Register adds or replaces a codec keyed by its Name. Nil is ignored.
func (c *Codecs) Register(codec Codec) {
	if codec == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byName[codec.Name()] = codec
	c.next++
	c.order[codec.Name()] = c.next
}

// Unregister removes the codec named name. Unknown names are ignored.
func (c *Codecs) Unregister(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byName, name)
	delete(c.order, name)
}

// ByName returns the codec named name. Lookup is case-sensitive.
func (c *Codecs) ByName(name string) (Codec, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	codec, ok := c.byName[name]
	return codec, ok
}

// ByExtension returns the codec whose [Codec.Extensions] includes ext
// (case-insensitive, ext should include the leading dot); when several
// do, the one registered most recently wins. On a miss it maps ext to a
// canonical name as [DetectFormat] does and returns the codec
// registered under that name.
func (c *Codecs) ByExtension(ext string) (Codec, bool) {
	ext = strings.ToLower(ext)
	c.mu.RLock()
	defer c.mu.RUnlock()
	var best Codec
	var bestOrder uint64
	for name, codec := range c.byName {
		if slices.Contains(codec.Extensions(), ext) && (best == nil || c.order[name] > bestOrder) {
			best, bestOrder = codec, c.order[name]
		}
	}
	if best != nil {
		return best, true
	}
	if name, ok := extToFormat[ext]; ok {
		if codec, ok := c.byName[name]; ok {
			return codec, true
		}
	}
	return nil, false
}

// Names returns the registered codec names in unspecified order.
func (c *Codecs) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.byName))
	for name := range c.byName {
		out = append(out, name)
	}
	return out
}

// Clone returns an independent shallow copy. The codecs themselves
// are shared (stateless by contract); the lookup map is fresh.
func (c *Codecs) Clone() *Codecs {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := &Codecs{byName: make(map[string]Codec, len(c.byName)), order: make(map[string]uint64, len(c.order)), next: c.next}
	maps.Copy(out.byName, c.byName)
	maps.Copy(out.order, c.order)
	return out
}

// lookupCodec returns the codec named format from codecs (nil holds
// none), or wrapped [ErrUnsupportedFormat].
func lookupCodec(codecs *Codecs, format string) (Codec, error) {
	if codecs != nil {
		if c, ok := codecs.ByName(format); ok {
			return c, nil
		}
	}
	return nil, fmt.Errorf("%w: no codec registered for format %q", ErrUnsupportedFormat, format)
}

// freshDecoder reports whether c is a bundled codec, whose Decode returns a new map that
// nothing else refers to.
func freshDecoder(c Codec) bool {
	switch c.(type) {
	case yamlCodec, jsonCodec, tomlCodec, jsoncCodec, dotenvCodec:
		return true
	}
	return false
}
