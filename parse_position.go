package recon

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/go-rotini/dotenv"
	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// utf8BOM is the UTF-8 byte-order mark some editors write at the start
// of a file.
var utf8BOM = []byte("\xef\xbb\xbf")

// jsonUnexpectedEnd is encoding/json's message for a truncated document.
const jsonUnexpectedEnd = "unexpected end of JSON input"

// stripBOM removes one leading UTF-8 byte-order mark.
func stripBOM(data []byte) []byte { return bytes.TrimPrefix(data, utf8BOM) }

// ParsePosition reports the 1-indexed line and column at which a
// bundled codec's Decode failed. err is the decode error (or a
// [*ParseError] wrapping one); data is the document that was decoded,
// used to turn encoding/json's byte offsets into a line and column (a
// leading byte-order mark is not counted). ok is false when err
// carries no position, as for a document whose root has the wrong
// type.
func ParsePosition(err error, data []byte) (line, col int, ok bool) {
	if pe, isParse := errors.AsType[*ParseError](err); isParse && pe.Position.Line > 0 {
		return pe.Position.Line, pe.Position.Column, true
	}
	if e, isYAML := errors.AsType[*yaml.SyntaxError](err); isYAML {
		return e.Pos.Line, e.Pos.Column, e.Pos.Line > 0
	}
	if e, isTOML := errors.AsType[*toml.SyntaxError](err); isTOML {
		return e.Pos.Line, e.Pos.Column, e.Pos.Line > 0
	}
	if e, isJSONC := errors.AsType[*jsonc.SyntaxError](err); isJSONC {
		return e.Pos.Line, e.Pos.Column, e.Pos.Line > 0
	}
	if e, isDotenv := errors.AsType[*dotenv.ParseError](err); isDotenv {
		return e.Pos.Line, e.Pos.Column, e.Pos.Line > 0
	}
	if e, isJSON := errors.AsType[*json.SyntaxError](err); isJSON {
		// Offset is just past the offending byte; at the end of the
		// input it already marks where more was expected.
		offset := e.Offset - 1
		if e.Error() == jsonUnexpectedEnd {
			offset = e.Offset
		}
		line, col = offsetPosition(stripBOM(data), offset)
		return line, col, true
	}
	if e, isJSON := errors.AsType[*json.UnmarshalTypeError](err); isJSON {
		line, col = offsetPosition(stripBOM(data), e.Offset)
		return line, col, true
	}
	return 0, 0, false
}

// ParseMessage returns a bundled codec's description of a decode
// failure without the format's "line N, column M" prefix, for pairing
// with [ParsePosition]. A [*ParseError] returns its Msg, a nil error
// returns "", and any other error returns err.Error().
func ParseMessage(err error) string {
	if err == nil {
		return ""
	}
	if e, ok := errors.AsType[*ParseError](err); ok && e.Msg != "" {
		return e.Msg
	}
	if e, ok := errors.AsType[*yaml.SyntaxError](err); ok {
		return e.Message
	}
	if e, ok := errors.AsType[*toml.SyntaxError](err); ok {
		return e.Message
	}
	if e, ok := errors.AsType[*jsonc.SyntaxError](err); ok {
		return e.Message
	}
	if e, ok := errors.AsType[*dotenv.ParseError](err); ok {
		return e.Message
	}
	if e, ok := errors.AsType[*json.SyntaxError](err); ok {
		return e.Error()
	}
	if e, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return e.Error()
	}
	return err.Error()
}

// newParseError wraps a codec decode failure of data, filling the
// position and message.
func newParseError(source, path string, data []byte, err error) *ParseError {
	pe := &ParseError{Source: source, Path: path, Cause: err, Msg: ParseMessage(err)}
	if line, col, ok := ParsePosition(err, data); ok {
		pe.Position = Position{Line: line, Column: col}
	}
	return pe
}

// offsetPosition converts a byte offset into data to a 1-indexed line
// and column. Only "\n" ends a line, so CRLF input counts the same.
func offsetPosition(data []byte, offset int64) (line, col int) {
	offset = min(max(offset, 0), int64(len(data)))
	head := data[:offset]
	line = bytes.Count(head, []byte("\n")) + 1
	col = int(offset) - (bytes.LastIndexByte(head, '\n') + 1) + 1
	return line, col
}
