package recon

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// PositionError is a [Codec] decode error that knows where in the
// document it occurred. [*ParseError] takes its line, column and
// message from one. The bundled codecs return one for syntax errors; a
// custom codec can too.
type PositionError interface {
	error
	// Position returns the 1-indexed line and column; line 0 means
	// unknown.
	Position() (line, col int)
	// Message describes the failure without its location.
	Message() string
}

// codecError is a bundled codec's decode error with its position. It
// reads like the "recon: <format> decode: ..." errors the codecs
// return otherwise and unwraps to the parser's own error.
type codecError struct {
	format    string
	err       error
	line, col int
	msg       string
}

func (e *codecError) Error() string             { return "recon: " + e.format + " decode: " + e.err.Error() }
func (e *codecError) Unwrap() error             { return e.err }
func (e *codecError) Position() (line, col int) { return e.line, e.col }
func (e *codecError) Message() string           { return e.msg }

// atPosition is a [codecError] for err at line and col, its message
// being err's text without the parser's "<format>: line N[, column M]: "
// prefix.
func atPosition(format string, err error, line, col int) *codecError {
	return &codecError{format: format, err: err, line: line, col: col, msg: stripPositionPrefix(err.Error(), format)}
}

// stripPositionPrefix removes a leading "<format>: " and then a
// "line N: " or "line N, column M: " from text.
func stripPositionPrefix(text, format string) string {
	rest := strings.TrimPrefix(text, format+": ")
	after, ok := strings.CutPrefix(rest, "line ")
	if !ok {
		return rest
	}
	i := 0
	for i < len(after) && after[i] >= '0' && after[i] <= '9' {
		i++
	}
	if i == 0 {
		return rest
	}
	after = after[i:]
	if tail, ok := strings.CutPrefix(after, ", column "); ok {
		j := 0
		for j < len(tail) && tail[j] >= '0' && tail[j] <= '9' {
			j++
		}
		after = tail[j:]
	}
	if msg, ok := strings.CutPrefix(after, ": "); ok {
		return msg
	}
	return rest
}

// errInvalidUTF8 reports input that is not valid UTF-8.
var errInvalidUTF8 = errors.New("invalid UTF-8 in input")

// checkUTF8 returns a [codecError] at the first byte of data that is
// not valid UTF-8, or nil when data is valid.
func checkUTF8(format string, data []byte) error {
	if utf8.Valid(data) {
		return nil
	}
	off := 0
	for off < len(data) {
		r, size := utf8.DecodeRune(data[off:])
		if r == utf8.RuneError && size <= 1 {
			break
		}
		off += size
	}
	line, col := offsetPosition(data, int64(off))
	return &codecError{format: format, err: errInvalidUTF8, line: line, col: col, msg: errInvalidUTF8.Error()}
}
