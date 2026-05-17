package recon

import (
	"fmt"

	"github.com/go-rotini/jsonc"
)

// jsoncCodec wraps go-rotini/jsonc — JSON with comments and trailing
// commas. The codec advertises both ".jsonc" and ".json5" because the
// sibling parser accepts JSON5's relaxed grammar; ".json5" callers get
// the same behavior they'd expect from a dedicated JSON5 codec.
type jsoncCodec struct{}

// JSONC is the package-level [Codec] for JSONC / JSON5 documents.
// Registered by [New] in the default codec set.
var JSONC Codec = jsoncCodec{}

func (jsoncCodec) Name() string         { return FormatJSONC }
func (jsoncCodec) Extensions() []string { return []string{".jsonc", ".json5"} }

// Decode parses data as a JSONC document into a map[string]any. Comments
// and trailing commas are stripped by the underlying parser before the
// JSON grammar is applied; the result has the same shape constraints as
// the [JSON] codec (object root required).
func (jsoncCodec) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var v any
	if err := jsonc.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("recon: jsonc decode: %w", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: jsonc root must be an object, got %T",
			ErrUnsupportedFormat, v)
	}
	return m, nil
}

// Encode serializes v as a JSONC document. The output is structurally
// JSON (no comments) — JSONC's grammar is only meaningful on the input
// side; recon never invents comments on encode.
func (jsoncCodec) Encode(v map[string]any) ([]byte, error) {
	b, err := jsonc.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("recon: jsonc encode: %w", err)
	}
	return b, nil
}
