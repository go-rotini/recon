package recon

import "fmt"

// BufferSource is a [Source] backed by bytes plus a [Codec]. The bytes
// are decoded once at construction; subsequent Get / Keys calls read
// from the decoded map. Useful for tests, in-process bytes, and the
// "stdin was piped as YAML" pattern.
type BufferSource struct {
	*MapSource

	format string
	codec  Codec
}

// NewBufferSource decodes data with the codec supplied via
// [WithBufferCodec] and returns a Source named name. The format string
// is recorded for diagnostics but does not drive decoding.
//
// Returns wrapped [ErrUnsupportedFormat] when no codec is supplied.
// Source construction has no access to the registry's codec set, so
// callers must pass the codec explicitly or use a format-named
// constructor ([NewYAMLSource], etc.) for files.
func NewBufferSource(name, format string, data []byte, opts ...BufferOption) (Source, error) {
	cfg := bufferOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	codec := bufferCfgCodec(cfg)
	if codec == nil {
		return nil, fmt.Errorf("%w: BufferSource(%q): no codec supplied; pass recon.WithBufferCodec(c)",
			ErrUnsupportedFormat, name)
	}
	decoded, err := codec.Decode(data)
	if err != nil {
		return nil, &ParseError{Source: name, Cause: err}
	}
	return &BufferSource{
		MapSource: NewMapSource(name, decoded),
		format:    format,
		codec:     codec,
	}, nil
}

// Format returns the format hint passed at construction.
func (s *BufferSource) Format() string { return s.format }

// Codec returns the codec used to decode this source's bytes.
func (s *BufferSource) Codec() Codec { return s.codec }
