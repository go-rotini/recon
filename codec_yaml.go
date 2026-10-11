package recon

import (
	"errors"
	"fmt"

	"github.com/go-rotini/yaml"
)

// yamlCodec wraps go-rotini/yaml and normalizes the decoded map so
// the registry never sees a yaml.Node.
type yamlCodec struct{}

// YAML is the [Codec] for YAML 1.2.2 documents (and the KYAML strict
// subset). Registered in the default codec set by [New].
var YAML Codec = yamlCodec{}

func (yamlCodec) Name() string         { return FormatYAML }
func (yamlCodec) Extensions() []string { return []string{".yaml", ".yml"} }

// Decode parses data as a YAML document. The root must be a mapping;
// sequences or scalars at the top level are rejected.
func (yamlCodec) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, yamlDecodeError(err)
	}
	m, ok := normalizeMap(v)
	if !ok {
		return nil, fmt.Errorf("%w: yaml root must be a mapping, got %T",
			ErrUnsupportedFormat, v)
	}
	return m, nil
}

func (yamlCodec) Encode(v map[string]any) ([]byte, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("recon: yaml encode: %w", err)
	}
	return b, nil
}

func yamlDecodeError(err error) error {
	if e, ok := errors.AsType[*yaml.SyntaxError](err); ok {
		return &codecError{format: FormatYAML, err: err, line: e.Pos.Line, col: e.Pos.Column, msg: e.Message}
	}
	return fmt.Errorf("recon: yaml decode: %w", err)
}
