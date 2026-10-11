package recon

import (
	"encoding/json"
	"errors"
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

// Decode parses data as a JSON object, ignoring one leading UTF-8
// byte-order mark. Arrays or scalars at the root are rejected with
// wrapped [ErrUnsupportedFormat].
func (jsonCodec) Decode(data []byte) (map[string]any, error) {
	data = stripBOM(data)
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, jsonDecodeError(err, data)
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

// jsonDecodeError gives a syntax error the line and column of its byte
// offset into data.
func jsonDecodeError(err error, data []byte) error {
	if e, ok := errors.AsType[*json.SyntaxError](err); ok {
		line, col := offsetPosition(data, jsonSyntaxOffset(e))
		return &codecError{format: FormatJSON, err: err, line: line, col: col, msg: e.Error()}
	}
	return fmt.Errorf("recon: json decode: %w", err)
}

// jsonSyntaxOffset is the offset of the byte e complains about: Offset
// is just past it, except at the end of the input, where Offset
// already marks where more was expected.
func jsonSyntaxOffset(e *json.SyntaxError) int64 {
	if e.Error() == jsonUnexpectedEnd {
		return e.Offset
	}
	return e.Offset - 1
}
