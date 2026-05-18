package recon

import (
	"fmt"
	"io"
	"os"
)

// NewStdinSource reads os.Stdin to EOF, decodes the bytes through the
// codec resolved from format (or [WithStdinCodec]), and returns a
// [Source] holding the decoded map. The construction is one-shot — no
// streaming, no incremental decode.
//
// Codec resolution: [WithStdinCodec] > codec named by format. A blank
// format with no [WithStdinCodec] returns wrapped
// [ErrUnsupportedFormat].
//
// TTY-safe: when stdin is a TTY with no piped data the constructor
// returns an empty source rather than blocking.
func NewStdinSource(format string, opts ...StdinOption) (Source, error) {
	cfg := stdinOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	codec, err := resolveStdinCodec(format, cfg, DefaultCodecs())
	if err != nil {
		return nil, err
	}
	data, err := readStdin()
	if err != nil {
		return nil, err
	}
	decoded := map[string]any{}
	if len(data) > 0 {
		decoded, err = codec.Decode(data)
		if err != nil {
			return nil, &ParseError{Source: "stdin", Cause: err}
		}
	}
	return NewMapSource("stdin", decoded), nil
}

func resolveStdinCodec(format string, cfg stdinOptions, codecs *Codecs) (Codec, error) {
	if cfg.codec != nil {
		return cfg.codec, nil
	}
	if format == "" {
		return nil, fmt.Errorf("%w: NewStdinSource needs a format or WithStdinCodec",
			ErrUnsupportedFormat)
	}
	c, ok := codecs.ByName(format)
	if !ok {
		return nil, fmt.Errorf("%w: no codec registered for format %q",
			ErrUnsupportedFormat, format)
	}
	return c, nil
}

// readStdin returns the bytes piped to os.Stdin, or an empty slice
// when stdin is a TTY with nothing piped.
func readStdin() ([]byte, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return nil, fmt.Errorf("recon: stat stdin: %w", err)
	}
	if (info.Mode() & os.ModeCharDevice) != 0 {
		return nil, nil
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("recon: read stdin: %w", err)
	}
	return b, nil
}
