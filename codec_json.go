package recon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// jsonCodec is the bundled encoding/json codec.
type jsonCodec struct{}

// JSON is the [Codec] for JSON documents, part of [DefaultCodecs]. An
// integer literal decodes exactly, as YAML's do: as int64, so
// 9007199254740993 keeps every digit, as uint64 above the int64 range,
// and as its decimal text beyond the uint64 range. A literal with a
// fraction or exponent decodes as float64; one beyond the float64 range
// is an error. Input that is not valid UTF-8 is an error.
var JSON Codec = jsonCodec{}

func (jsonCodec) Name() string         { return FormatJSON }
func (jsonCodec) Extensions() []string { return []string{".json"} }

// Decode parses data as a JSON object, ignoring one leading UTF-8
// byte-order mark. Arrays or scalars at the root are rejected with
// wrapped [ErrUnsupportedFormat]; a document holding only white space
// decodes as an empty map. A key repeated in one object is an error.
func (jsonCodec) Decode(data []byte) (map[string]any, error) {
	data = stripBOM(data)
	if isJSONSpace(data) {
		return map[string]any{}, nil
	}
	if err := checkUTF8(FormatJSON, data); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := jsonValue(dec, data, 0)
	if err == nil && !isJSONSpace(data[dec.InputOffset():]) {
		err = errJSONTrailing
	}
	if err != nil {
		if dup, ok := err.(*codecError); ok { //nolint:errorlint // jsonValue returns it unwrapped
			return nil, dup
		}
		// Report the error json.Unmarshal gives for the document.
		var probe any
		if uerr := json.Unmarshal(data, &probe); uerr != nil {
			err = uerr
		}
		return nil, jsonDecodeError(err, data)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: json root must be an object, got %T",
			ErrUnsupportedFormat, v)
	}
	return m, nil
}

// jsonMaxDepth is how deeply arrays and objects may nest, the limit
// encoding/json applies.
const jsonMaxDepth = 10000

// errJSONDepth stops a walk nested deeper than [jsonMaxDepth]; the
// decode path replaces it with json.Unmarshal's own error.
var errJSONDepth = errors.New("exceeded max depth")

// errJSONDuplicateKey reports a key repeated in one object.
var errJSONDuplicateKey = errors.New("duplicate key")

// jsonValue reads the next value from dec, which has UseNumber set,
// converting numbers as [jsonNumber] does. A key repeated in one
// object is a [*codecError] at the second occurrence in data.
func jsonValue(dec *json.Decoder, data []byte, depth int) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err //nolint:wrapcheck // replaced by json.Unmarshal's error
	}
	switch t := tok.(type) {
	case json.Number:
		v, ok := jsonNumber(t)
		if !ok {
			err := errNumberRange(t.String())
			line, col := offsetPosition(data, dec.InputOffset()-int64(len(t)))
			return nil, &codecError{format: FormatJSON, err: err, line: line, col: col, msg: err.Error()}
		}
		return v, nil
	case json.Delim:
		if depth >= jsonMaxDepth {
			return nil, errJSONDepth
		}
		if t == '[' {
			return jsonArray(dec, data, depth+1)
		}
		return jsonObject(dec, data, depth+1)
	default:
		return tok, nil
	}
}

// jsonArray reads the elements of an array whose '[' dec has read.
func jsonArray(dec *json.Decoder, data []byte, depth int) (any, error) {
	out := []any{}
	for dec.More() {
		v, err := jsonValue(dec, data, depth)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err //nolint:wrapcheck // replaced by json.Unmarshal's error
	}
	return out, nil
}

// jsonObject reads the members of an object whose '{' dec has read.
func jsonObject(dec *json.Decoder, data []byte, depth int) (any, error) {
	out := map[string]any{}
	for dec.More() {
		start := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			return nil, err //nolint:wrapcheck // replaced by json.Unmarshal's error
		}
		key, _ := tok.(string)
		if _, dup := out[key]; dup {
			line, col := offsetPosition(data, jsonKeyOffset(data, start))
			err := fmt.Errorf("%w %q", errJSONDuplicateKey, key)
			return nil, &codecError{format: FormatJSON, err: err, line: line, col: col, msg: err.Error()}
		}
		v, err := jsonValue(dec, data, depth)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, err //nolint:wrapcheck // replaced by json.Unmarshal's error
	}
	return out, nil
}

// jsonKeyOffset is the offset of the key that follows offset in data,
// past white space and a member separator.
func jsonKeyOffset(data []byte, offset int64) int64 {
	for offset < int64(len(data)) {
		switch data[offset] {
		case ' ', '\t', '\n', '\r', ',':
			offset++
		default:
			return offset
		}
	}
	return offset
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

// errJSONTrailing reports data after the top-level JSON value; the
// decode path replaces it with json.Unmarshal's own error.
var errJSONTrailing = errors.New("invalid character after top-level value")

// jsonNumber converts a JSON number literal: an integer that fits int64
// becomes int64, one that fits uint64 a uint64, a larger integer its
// decimal text, and anything with a fraction or exponent float64. ok is
// false for a fraction or exponent literal beyond the float64 range.
func jsonNumber(n json.Number) (v any, ok bool) {
	s := n.String()
	if !strings.ContainsAny(s, ".eE") {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i, true
		}
		if u, err := strconv.ParseUint(s, 10, 64); err == nil {
			return u, true
		}
		return s, true
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// errNumberRange reports a number literal whose fraction or exponent
// form is beyond the float64 range.
func errNumberRange(s string) error {
	return fmt.Errorf("number %s: %w", s, strconv.ErrRange)
}

// isJSONSpace reports whether b holds only JSON white space: space,
// tab, line feed and carriage return.
func isJSONSpace(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
		default:
			return false
		}
	}
	return true
}
