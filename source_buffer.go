package recon

import (
	"fmt"
)

// BufferSource is a [Source] backed by bytes plus a [Codec]. The bytes are
// decoded once at construction; subsequent Get / Keys calls read from the
// decoded map. Intended for tests, library callers with bytes in hand, and
// the rotini integration's "stdin was piped as YAML" pattern.
//
// BufferSource is a thin wrapper over [MapSource] — once the bytes are
// decoded, the behavior is identical. The construction-time decode is what
// distinguishes BufferSource from [NewMapSource].
type BufferSource struct {
	*MapSource

	format string
	codec  Codec
}

// NewBufferSource decodes data using the supplied codec (via the
// [WithBufferCodec] option) and returns a Source named name. The format
// string is recorded for diagnostic output but does not drive decoding —
// the codec is authoritative.
//
// Returns [ErrUnsupportedFormat] when no codec is supplied via
// [WithBufferCodec]. In Phase 3 there is no registry-side codec set
// available at source-construction time, so callers must pass the codec
// explicitly. In Phase 4 onwards, format-named convenience constructors
// ([YAMLSource], etc.) will pre-wire the matching bundled codec.
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

// Format reports the format hint passed at construction. Useful in
// diagnostic output ("source X (yaml) returned an empty Keys() set") but
// does not affect behavior.
func (s *BufferSource) Format() string { return s.format }

// Codec returns the codec used to decode this source's bytes. Stored so a
// future Save / Encode operation through this source can round-trip via
// the same codec.
func (s *BufferSource) Codec() Codec { return s.codec }
