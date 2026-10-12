package recon

import (
	"errors"
	"fmt"

	"github.com/go-rotini/jsonc"
)

// jsoncCodec wraps go-rotini/jsonc (JSON with comments and trailing
// commas).
type jsoncCodec struct{}

// JSONC is the [Codec] for JSONC documents (JSON with comments and
// trailing commas), part of [DefaultCodecs]. It also claims the ".json5"
// extension, but parses such files as JSONC: the rest of JSON5
// (unquoted keys, single-quoted strings and so on) is rejected.
var JSONC Codec = jsoncCodec{}

func (jsoncCodec) Name() string         { return FormatJSONC }
func (jsoncCodec) Extensions() []string { return []string{".jsonc", ".json5"} }

// Decode parses data as a JSONC document. An object root is required.
func (jsoncCodec) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var v any
	if err := jsonc.Unmarshal(data, &v); err != nil {
		return nil, jsoncDecodeError(err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: jsonc root must be an object, got %T",
			ErrUnsupportedFormat, v)
	}
	return m, nil
}

// Encode serializes v as JSON; JSONC comments are an input-only
// concern.
func (jsoncCodec) Encode(v map[string]any) ([]byte, error) {
	b, err := jsonc.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("recon: jsonc encode: %w", err)
	}
	return b, nil
}

func jsoncDecodeError(err error) error {
	if e, ok := errors.AsType[*jsonc.SyntaxError](err); ok {
		return &codecError{format: FormatJSONC, err: err, line: e.Pos.Line, col: e.Pos.Column, msg: e.Message}
	}
	return fmt.Errorf("recon: jsonc decode: %w", err)
}
