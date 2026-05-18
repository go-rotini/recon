package recon

import (
	"encoding/json"
	"fmt"
)

// jsonCodec is the bundled encoding/json codec. JSON numbers decode
// as float64; callers that need int-typed access should use Get[int]
// or [Bind] into an int-typed field.
type jsonCodec struct{}

// JSON is the [Codec] for application/json. Registered in the default
// codec set by [New].
var JSON Codec = jsonCodec{}

func (jsonCodec) Name() string         { return FormatJSON }
func (jsonCodec) Extensions() []string { return []string{".json"} }

// Decode parses data as a JSON object. Arrays or scalars at the root
// are rejected with wrapped [ErrUnsupportedFormat].
func (jsonCodec) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("recon: json decode: %w", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: json root must be an object, got %T",
			ErrUnsupportedFormat, v)
	}
	return m, nil
}

// Encode serializes v as compact JSON. Indented output should be
// produced by passing the result through [json.Indent].
func (jsonCodec) Encode(v map[string]any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("recon: json encode: %w", err)
	}
	return b, nil
}
