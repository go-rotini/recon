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

// NewBufferSource decodes data and returns a Source named name. The
// codec is the one supplied via [WithBufferCodec], else the codec
// format names in [DefaultCodecs]. One leading UTF-8 byte-order mark is
// ignored.
//
// Returns wrapped [ErrUnsupportedFormat] when no codec resolves, and a
// [*ParseError] when decoding fails.
func NewBufferSource(name, format string, data []byte, opts ...BufferOption) (Source, error) {
	cfg := bufferOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	codec := bufferCfgCodec(cfg)
	if codec == nil {
		c, ok := DefaultCodecs().ByName(format)
		if !ok {
			return nil, fmt.Errorf("%w: BufferSource(%q): no codec for format %q; pass recon.WithBufferCodec(c)",
				ErrUnsupportedFormat, name, format)
		}
		codec = c
	}
	data = stripBOM(data)
	decoded, err := codec.Decode(data)
	if err != nil {
		return nil, newParseError(name, "", data, err)
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
