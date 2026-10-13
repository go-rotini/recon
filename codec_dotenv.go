package recon

import (
	"errors"
	"fmt"

	"github.com/go-rotini/dotenv"
)

// dotenvCodec wraps go-rotini/dotenv. Dotenv is a flat key-value
// format — no nesting — so Decode produces a single-level map and
// Encode rejects nested input.
type dotenvCodec struct{}

// Dotenv is the [Codec] for `.env` files, part of [DefaultCodecs]. It
// decodes to one flat level of string values.
var Dotenv Codec = dotenvCodec{}

func (dotenvCodec) Name() string         { return FormatDotenv }
func (dotenvCodec) Extensions() []string { return []string{".env"} }

// Decode parses data as a `.env` document. Values are strings. It
// accepts input up to [MaxFileSize], the cap recon puts on every file.
// Input that is not valid UTF-8 is an error.
func (dotenvCodec) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	if err := checkUTF8(FormatDotenv, data); err != nil {
		return nil, err
	}
	f, err := dotenv.Parse(data, dotenv.WithMaxFileSize(MaxFileSize))
	if err != nil {
		return nil, dotenvDecodeError(err)
	}
	flat := f.ToMap()
	out := make(map[string]any, len(flat))
	for k, v := range flat {
		out[k] = v
	}
	return out, nil
}

// Encode serializes a flat map as a `.env` document. Nested values
// are rejected with wrapped [ErrUnsupportedFormat].
func (dotenvCodec) Encode(v map[string]any) ([]byte, error) {
	flat := make(map[string]string, len(v))
	for k, val := range v {
		switch val.(type) {
		case map[string]any, []any:
			return nil, fmt.Errorf("%w: dotenv cannot encode nested value at %q",
				ErrUnsupportedFormat, k)
		}
		flat[k] = fmt.Sprint(val)
	}
	b, err := dotenv.Marshal(flat)
	if err != nil {
		return nil, fmt.Errorf("recon: dotenv encode: %w", err)
	}
	return b, nil
}

func dotenvDecodeError(err error) error {
	if e, ok := errors.AsType[*dotenv.ParseError](err); ok {
		return &codecError{format: FormatDotenv, err: err, line: e.Pos.Line, col: e.Pos.Column, msg: e.Message}
	}
	if e, ok := errors.AsType[*dotenv.ExpansionError](err); ok && e.Pos.Line > 0 {
		return atPosition(FormatDotenv, err, e.Pos.Line, e.Pos.Column)
	}
	return fmt.Errorf("recon: dotenv decode: %w", err)
}
