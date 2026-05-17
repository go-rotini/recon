package recon

import (
	"fmt"

	"github.com/go-rotini/dotenv"
)

// dotenvCodec wraps go-rotini/dotenv. Unlike the other bundled codecs,
// dotenv is a *flat* key-value format — `.env` files only express scalar
// strings, never nested maps or arrays. The codec produces a single-
// level map[string]any (with string values) and rejects nested input on
// encode.
type dotenvCodec struct{}

// Dotenv is the package-level [Codec] for `.env` files. Registered by
// [New] in the default codec set.
var Dotenv Codec = dotenvCodec{}

func (dotenvCodec) Name() string         { return FormatDotenv }
func (dotenvCodec) Extensions() []string { return []string{".env"} }

// Decode parses data as a `.env` document into a single-level
// map[string]any whose values are strings. Variable expansion is left to
// the dotenv parser's defaults; surrounding shells expand vars at process
// launch, so post-load expansion is rarely the desired behavior.
func (dotenvCodec) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	f, err := dotenv.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("recon: dotenv decode: %w", err)
	}
	flat := f.ToMap()
	out := make(map[string]any, len(flat))
	for k, v := range flat {
		out[k] = v
	}
	return out, nil
}

// Encode serializes a flat map[string]any as a `.env` document. Every
// value is coerced to string via fmt.Sprint; nested maps or slices are
// rejected with a wrapped [ErrUnsupportedFormat] because the `.env`
// grammar has no representation for them.
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
