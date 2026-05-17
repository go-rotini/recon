package recon

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// FileSourceFS is the [io/fs.FS]-backed twin of [FileSource]. It reads
// the configuration file through an [fs.FS] (typically embed.FS, but any
// fs.FS works) rather than the local filesystem; useful for shipping a
// "default config" embedded in the binary and overlaying a user-supplied
// file on top via precedence.
//
// FileSourceFS is read-only: there is no [Reload] target since most fs.FS
// implementations expose immutable bytes. The [Watcher] interface is not
// implemented — embedded files don't change at runtime.
type FileSourceFS struct {
	*MapSource

	codec    Codec
	path     string
	optional bool
}

// NewFileSourceFS returns a [FileSourceFS] reading path from fsys. The
// source's [Source.Name] is name (so the embedded "config.yaml" can be
// distinguished from the on-disk "config.yaml" by precedence label).
//
// Codec resolution follows the same rules as [NewFileSource]:
// [WithFileCodec] > [WithFileFormat] > extension lookup.
//
// fsys MUST NOT be nil; nil-fsys returns an error wrapping
// [ErrInvalidPath].
func NewFileSourceFS(name string, fsys fs.FS, path string, opts ...FileOption) (Source, error) {
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

	codec, err := resolveFileCodec(path, cfg, DefaultCodecs())
	if err != nil {
		return nil, err
	}

	data, missing, err := readFSMaybeOptional(fsys, path, cfg.optional)
	if err != nil {
		return nil, err
	}
	decoded := map[string]any{}
	if !missing && len(data) > 0 {
		decoded, err = codec.Decode(data)
		if err != nil {
			return nil, &ParseError{Source: name, Cause: err}
		}
	}

	return &FileSourceFS{
		MapSource: NewMapSource(name, decoded),
		codec:     codec,
		path:      path,
		optional:  cfg.optional,
	}, nil
}

// Path returns the in-fs.FS path the source is reading from.
func (s *FileSourceFS) Path() string { return s.path }

// Format returns the canonical codec name driving this source's decode.
func (s *FileSourceFS) Format() string {
	if s.codec == nil {
		return ""
	}
	return s.codec.Name()
}

// readFSMaybeOptional reads path from fsys with the same
// optional / missing semantics as [readFileMaybeOptional]: an absent file
// with optional=true returns (nil, true, nil); other I/O errors surface.
//
// The leading slash is stripped because fs.FS paths must be relative —
// callers used to writing "/etc/myapp/config.yaml" on the local FS get
// the same call shape with the FS variant.
func readFSMaybeOptional(fsys fs.FS, path string, optional bool) (data []byte, missing bool, err error) {
	clean := strings.TrimPrefix(filepath.ToSlash(path), "/")
	b, err := fs.ReadFile(fsys, clean)
	if err != nil {
		if optional && errors.Is(err, fs.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("recon: fs read %q: %w", clean, err)
	}
	return b, false, nil
}
