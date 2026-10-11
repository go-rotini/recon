package recon

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
