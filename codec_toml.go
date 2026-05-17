package recon

import (
	"fmt"

	"github.com/go-rotini/toml"
)

// tomlCodec wraps go-rotini/toml. TOML's document grammar always has a
// table at the root, so the unsupported-format check that the YAML and
// JSON codecs perform is unnecessary here — but normalizeMap still runs
// to flatten any map[any]any an older decoder might return.
type tomlCodec struct{}

// TOML is the package-level [Codec] for TOML documents. Registered by
// [New] in the default codec set.
var TOML Codec = tomlCodec{}

func (tomlCodec) Name() string         { return FormatTOML }
func (tomlCodec) Extensions() []string { return []string{".toml"} }

// Decode parses data as a TOML document into a map[string]any.
func (tomlCodec) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var v any
	if err := toml.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("recon: toml decode: %w", err)
	}
	m, ok := normalizeMap(v)
	if !ok {
		return nil, fmt.Errorf("%w: toml root must be a table, got %T",
			ErrUnsupportedFormat, v)
	}
	return m, nil
}

// Encode serializes v as a TOML document.
func (tomlCodec) Encode(v map[string]any) ([]byte, error) {
	b, err := toml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("recon: toml encode: %w", err)
	}
	return b, nil
}
