package recon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-rotini/jsonc"
)

// jsoncCodec wraps go-rotini/jsonc (JSON with comments and trailing
// commas).
type jsoncCodec struct{}

// JSONC is the [Codec] for JSONC documents (JSON with comments and
// trailing commas), part of [DefaultCodecs]. Numbers decode as [JSON]'s
// do: integer literals exactly, as int64, uint64 or a [json.Number]. It
// also claims the ".json5" extension, but parses such files as JSONC:
// the rest of JSON5 (unquoted keys, single-quoted strings and so on) is
// rejected.
var JSONC Codec = jsoncCodec{}

func (jsoncCodec) Name() string         { return FormatJSONC }
func (jsoncCodec) Extensions() []string { return []string{".jsonc", ".json5"} }

// Decode parses data as a JSONC document. An object root is required;
// a document holding only comments and white space, or a null,
// decodes as an empty map. A key repeated in one object, a number with a fraction or
// exponent beyond the float64 range, and input that is not valid UTF-8
// are errors.
func (jsoncCodec) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	if err := checkUTF8(FormatJSONC, stripBOM(data)); err != nil {
		return nil, err
	}
	var v any
	if err := jsonc.UnmarshalWithOptions(data, &v, jsonc.WithUseNumber()); err != nil {
		if jsoncHoldsNoValue(data) {
			return map[string]any{}, nil
		}
		return nil, jsoncDecodeError(err)
	}
	v, bad := decodeJSONNumbers(v)
	if bad != "" {
		rangeErr := errNumberRange(bad)
		out := &codecError{format: FormatJSONC, err: rangeErr, msg: rangeErr.Error()}
		// Decoding without UseNumber reports where the number is.
		var probe any
		if err := jsonc.Unmarshal(data, &probe); err != nil {
			if pe, ok := jsoncDecodeError(err).(*codecError); ok { //nolint:errorlint // jsoncDecodeError returns it unwrapped
				out.line, out.col = pe.line, pe.col
			}
		}
		return nil, out
	}
	if v == nil {
		return map[string]any{}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: jsonc root must be an object, got %T",
			ErrUnsupportedFormat, v)
	}
	return m, nil
}

// jsoncHoldsNoValue reports whether data is only white space and
// complete comments, after an optional byte-order mark.
func jsoncHoldsNoValue(data []byte) bool {
	data = stripBOM(data)
	for {
		data = bytes.TrimLeft(data, " \t\r\n")
		switch {
		case len(data) == 0:
			return true
		case bytes.HasPrefix(data, []byte("//")):
			end := bytes.IndexByte(data, '\n')
			if end < 0 {
				return true
			}
			data = data[end+1:]
		case bytes.HasPrefix(data, []byte("/*")):
			end := bytes.Index(data[2:], []byte("*/"))
			if end < 0 {
				return false
			}
			data = data[2+end+2:]
		default:
			return false
		}
	}
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

// jsoncDecodeError gives every parser error that knows its position a
// line and column.
func jsoncDecodeError(err error) error {
	if e, ok := errors.AsType[*jsonc.SyntaxError](err); ok {
		return &codecError{format: FormatJSONC, err: err, line: e.Pos.Line, col: e.Pos.Column, msg: e.Message}
	}
	var pos jsonc.Position
	if e, ok := errors.AsType[*jsonc.DuplicateKeyError](err); ok {
		pos = e.Pos
	} else if e, ok := errors.AsType[*jsonc.OverflowError](err); ok {
		pos = e.Pos
	} else if e, ok := errors.AsType[*jsonc.StrictJSONError](err); ok {
		pos = e.Pos
	}
	if pos.Line > 0 {
		return atPosition(FormatJSONC, err, pos.Line, pos.Column)
	}
	return fmt.Errorf("recon: jsonc decode: %w", err)
}

// decodeJSONNumbers replaces every [json.Number] in v (decoded with
// UseNumber) by its [jsonNumber] form. bad is the text of a number
// beyond the float64 range, when there is one.
func decodeJSONNumbers(v any) (out any, bad string) {
	switch x := v.(type) {
	case json.Number:
		n, ok := jsonNumber(x)
		if !ok {
			return nil, x.String()
		}
		return n, ""
	case map[string]any:
		for k, e := range x {
			if x[k], bad = decodeJSONNumbers(e); bad != "" {
				return nil, bad
			}
		}
		return x, ""
	case []any:
		for i, e := range x {
			if x[i], bad = decodeJSONNumbers(e); bad != "" {
				return nil, bad
			}
		}
		return x, ""
	default:
		return v, ""
	}
}
