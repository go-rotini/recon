package recon

import (
	"fmt"
	"io"
	"os"
)

// NewStdinSource reads os.Stdin to EOF, decodes the bytes through codec
// (resolved from format), and returns a [Source] holding the decoded
// map. The construction is one-shot — there is no incremental decode and
// no streaming; callers piping a large config into stdin should redirect
// to a file and use [NewFileSource] instead.
//
// The source's [Source.Name] is "stdin" so [Snapshot.String] /
// [Describe] callers can see at a glance where the values came from.
//
// Codec resolution:
//   - [WithStdinCodec] supplies the codec explicitly (wins outright).
//   - Otherwise format is looked up in [DefaultCodecs] by name.
//   - A blank format with no [WithStdinCodec] returns a wrapped
//     [ErrUnsupportedFormat] — stdin has no extension to fall back on.
//
// TTY-safe: if stdin is a TTY (no piped data) and no bytes are
// available, the constructor still succeeds with an empty source —
// matching the "stdin is optional unless data is being piped"
// expectation most CLIs have.
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

// resolveStdinCodec dispatches the codec-resolution rules described on
// [NewStdinSource]. Kept package-private so the public surface is just
// the constructor.
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

// readStdin reads os.Stdin to EOF. Returns an empty slice (not an error)
// when stdin is a TTY with no data available — see the TTY-safety note on
// [NewStdinSource].
func readStdin() ([]byte, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return nil, fmt.Errorf("recon: stat stdin: %w", err)
	}
	// Bit ModeCharDevice means stdin is a TTY; nothing piped to us.
	if (info.Mode() & os.ModeCharDevice) != 0 {
		return nil, nil
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("recon: read stdin: %w", err)
	}
	return b, nil
}
