package recon

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// FileSourceFS is the [io/fs.FS]-backed twin of [FileSource]. Useful
// for shipping a default config embedded in the binary and overlaying
// a user-supplied file on top via precedence.
//
// It is read once: it has no Reload method and is not a [Watcher].
// Path expansion and [WithSearchPaths] do not apply; a leading "/" in
// the path is dropped, since [io/fs.FS] paths are relative.
type FileSourceFS struct {
	*MapSource

	codec    Codec
	path     string
	optional bool
}

// NewFileSourceFS returns a [FileSourceFS] reading path from fsys.
// Codec resolution mirrors [NewFileSource]: [WithFileCodec] >
// [WithFileFormat] > extension lookup. Returns wrapped
// [ErrInvalidPath] for nil fsys or empty name.
func NewFileSourceFS(name string, fsys fs.FS, path string, opts ...FileOption) (Source, error) {
	return newFileSourceFS(DefaultCodecs(), newParseError, name, fsys, path, opts)
}

// NewFileSourceFSWithCodecs is [NewFileSourceFS] resolving the codec
// against codecs instead of [DefaultCodecs], as
// [NewFileSourceWithCodecs] does.
func NewFileSourceFSWithCodecs(codecs *Codecs, name string, fsys fs.FS, path string, opts ...FileOption) (Source, error) {
	return newFileSourceFS(codecs, newCodecParseError, name, fsys, path, opts)
}

func newFileSourceFS(codecs *Codecs, parseErr parseErrorFunc, name string, fsys fs.FS, path string, opts []FileOption) (Source, error) {
	if fsys == nil {
		return nil, fmt.Errorf("%w: NewFileSourceFS: nil fs.FS", ErrInvalidPath)
	}
	if name == "" {
		return nil, fmt.Errorf("%w: NewFileSourceFS: empty source name", ErrInvalidPath)
	}
	cfg := defaultFileOptions()
	for _, opt := range opts {
		opt(&cfg)
	}

	codec, err := resolveFileCodec(path, cfg, codecs)
	if err != nil {
		return nil, err
	}

	data, missing, err := readFSMaybeOptional(fsys, path, cfg.optional)
	if err != nil {
		return nil, err
	}
	data = stripBOM(data)
	decoded := map[string]any{}
	if !missing && len(data) > 0 {
		decoded, err = codec.Decode(data)
		if err != nil {
			return nil, parseErr(name, path, data, err)
		}
	}

	return &FileSourceFS{
		MapSource: newFileMapSource(name, decoded, freshDecoder(codec), cfg),
		codec:     codec,
		path:      path,
		optional:  cfg.optional,
	}, nil
}

// Path returns the in-fs.FS path the source reads from.
func (s *FileSourceFS) Path() string { return s.path }

// Format returns the canonical codec name driving this source's
// decode.
func (s *FileSourceFS) Format() string {
	if s.codec == nil {
		return ""
	}
	return s.codec.Name()
}

// readFSMaybeOptional reads path from fsys. fs.FS paths must be
// relative; a leading slash is stripped so callers can write
// "/etc/myapp/config.yaml" with both source kinds.
func readFSMaybeOptional(fsys fs.FS, path string, optional bool) (data []byte, missing bool, err error) {
	clean := strings.TrimPrefix(filepath.ToSlash(path), "/")
	b, err := readFSFileCapped(fsys, clean)
	if err != nil {
		if optional && errors.Is(err, fs.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("recon: fs read %q: %w", clean, err)
	}
	return b, false, nil
}
