package recon

import (
	"fmt"

	"github.com/go-rotini/toml"
)

type tomlCodec struct{}

// TOML is the [Codec] for TOML documents. Registered in the default
// codec set by [New].
var TOML Codec = tomlCodec{}

func (tomlCodec) Name() string         { return FormatTOML }
func (tomlCodec) Extensions() []string { return []string{".toml"} }

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

func (tomlCodec) Encode(v map[string]any) ([]byte, error) {
	b, err := toml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("recon: toml encode: %w", err)
	}
	return b, nil
}
