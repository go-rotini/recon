package recon

import (
	"encoding/json"
	"fmt"
)

// jsonCodec is the bundled stdlib-encoding/json codec. JSON's only data
// shapes — objects, arrays, numbers, strings, booleans, and null — map
// 1:1 onto the recon leaf-value set; the only normalization step is
// widening every JSON number to float64 (which encoding/json already does).
//
// Numeric int-vs-float fidelity: encoding/json decodes every number as
// float64. Callers that need int-typed access should use Get[int] or
// Bind a struct field of int type — coerceValueAny converts via AsInt64.
type jsonCodec struct{}

// JSON is the package-level [Codec] for application/json. Registered by
// [New] in the default codec set; available for explicit selection via
// [WithFileCodec] / [WithBufferCodec] / [WithStdinCodec].
var JSON Codec = jsonCodec{}

func (jsonCodec) Name() string         { return FormatJSON }
func (jsonCodec) Extensions() []string { return []string{".json"} }

// Decode parses data as a JSON object into a map[string]any. JSON arrays
// or scalars at the document root are rejected with a wrapped
// [ErrUnsupportedFormat] because the [Source.Get] contract requires
// key/value addressing.
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

// Encode serializes v as compact JSON. Callers wanting indented output
// should run the result through [json.Indent] or write a custom codec.
func (jsonCodec) Encode(v map[string]any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("recon: json encode: %w", err)
	}
	return b, nil
}
