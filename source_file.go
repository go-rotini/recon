package recon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// FileSource is a codec-driven [Source] backed by a single file on
// the local filesystem. The file is read at construction and decoded;
// later Get calls read from the decoded map.
//
// Pair with [WithFileCodec] / [WithSearchPaths] / [WithPathExpansion]
// / [WithOptional] to express "look in N directories, expand ~, treat
// missing as no-op" in one constructor.
//
// FileSource is watched when a [WatcherFactory] is available, either
// set per-source via [WithFileWatcher] or injected by the registry from
// [WithWatcher].
type FileSource struct {
	*MapSource

	// resolvedPath is the absolute, expanded path the file was read
	// from.
	resolvedPath string

	// codec is the codec resolved at construction.
	codec Codec

	// optional records whether a missing file is accepted.
	optional bool

	// watcher is the per-source override; nil means use the
	// registry-wide factory injected at AddSource time.
	watcher WatcherFactory

	// missing reports whether the file did not exist at construction
	// (only meaningful when optional is true).
	missing atomic.Bool

	// parseErr builds the error for a decode failure.
	parseErr parseErrorFunc

	// reload serializes Reload calls.
	reload sync.Mutex
}

// NewFileSource constructs a [FileSource] for path. Codec resolution
// order: [WithFileCodec] > [WithFileFormat] > file extension. Returns
// wrapped [ErrUnsupportedFormat] when no codec resolves. One leading
// UTF-8 byte-order mark is ignored. Decode failures surface as
// [*ParseError] with the resolved path and, when the parser reports
// one, the line and column.
//
// The source's Name is the basename of the resolved path.
func NewFileSource(path string, opts ...FileOption) (Source, error) {
	return newFileSource(DefaultCodecs(), newParseError, path, opts)
}

// NewFileSourceWithCodecs is [NewFileSource] resolving [WithFileFormat]
// and the file extension against codecs instead of [DefaultCodecs]; nil
// means no codecs, so only [WithFileCodec] can supply one. A format
// missing from codecs returns wrapped [ErrUnsupportedFormat].
// A decode failure takes its line and column from a [PositionError],
// which every bundled codec returns for a syntax error.
func NewFileSourceWithCodecs(codecs *Codecs, path string, opts ...FileOption) (Source, error) {
	return newFileSource(codecs, newCodecParseError, path, opts)
}

func newFileSource(codecs *Codecs, parseErr parseErrorFunc, path string, opts []FileOption) (Source, error) {
	cfg := defaultFileOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	resolved, err := resolveFilePath(path, cfg)
	if err != nil {
		return nil, err
	}

	codec, err := resolveFileCodec(resolved, cfg, codecs)
	if err != nil {
		return nil, err
	}

	name := filepath.Base(resolved)
	data, missing, err := readFileMaybeOptional(resolved, cfg.optional)
	if err != nil {
		return nil, err
	}
	data = stripBOM(data)
	decoded := map[string]any{}
	if !missing && len(data) > 0 {
		decoded, err = codec.Decode(data)
		if err != nil {
			return nil, parseErr(name, resolved, data, err)
		}
	}

	src := &FileSource{
		MapSource:    NewMapSource(name, decoded),
		resolvedPath: resolved,
		codec:        codec,
		optional:     cfg.optional,
		watcher:      cfg.watcher,
		parseErr:     parseErr,
	}
	if missing {
		src.missing.Store(true)
	}
	return src, nil
}

// Path returns the absolute, expanded path the source reads from.
func (s *FileSource) Path() string { return s.resolvedPath }

// Format returns the canonical codec name driving this source's
// decode.
func (s *FileSource) Format() string {
	if s.codec == nil {
		return ""
	}
	return s.codec.Name()
}

// Reload re-reads the file and atomically swaps the underlying map.
// On a missing-file outcome with [WithOptional], the source is emptied
// without error. Decode failures retain the existing contents. Reload
// does not rebuild the registry's snapshot: a watched source's reload
// does, otherwise call [Registry.Reload] afterwards.
func (s *FileSource) Reload() error {
	s.reload.Lock()
	defer s.reload.Unlock()

	data, missing, err := readFileMaybeOptional(s.resolvedPath, s.optional)
	if err != nil {
		return err
	}
	s.missing.Store(missing)
	if missing {
		s.Replace(map[string]any{})
		return nil
	}
	data = stripBOM(data)
	decoded, err := s.codec.Decode(data)
	if err != nil {
		return s.parseErr(s.Name(), s.resolvedPath, data, err)
	}
	s.Replace(decoded)
	return nil
}

// Watch implements [Watcher] for the underlying file. Without a
// [WatcherFactory] it returns a closed channel.
//
// Each upstream notification triggers a [FileSource.Reload], then
// forwards a [SourceChange] carrying any reload error.
func (s *FileSource) Watch(ctx context.Context) (<-chan SourceChange, error) {
	factory := s.watcherFactory()
	if factory == nil {
		ch := make(chan SourceChange)
		close(ch)
		return ch, nil
	}
	upstream, err := factory.Watch(ctx, s.resolvedPath)
	if err != nil {
		return nil, err
	}
	out := make(chan SourceChange, 1)
	go s.fanWatchEvents(ctx, upstream, out)
	return out, nil
}

// watcherFactory returns the factory Watch will use, or nil.
func (s *FileSource) watcherFactory() WatcherFactory {
	s.reload.Lock()
	defer s.reload.Unlock()
	return s.watcher
}

// SetWatcher attaches a [WatcherFactory] after construction. The
// registry calls it to install the [WithWatcher] factory. Has no effect
// on a running subscription.
func (s *FileSource) SetWatcher(w WatcherFactory) {
	s.reload.Lock()
	defer s.reload.Unlock()
	s.watcher = w
}

// fanWatchEvents bridges the upstream channel into the FileSource's
// downstream channel, running [Reload] on every notification.
func (s *FileSource) fanWatchEvents(
	ctx context.Context,
	upstream <-chan SourceChange,
	out chan<- SourceChange,
) {
	defer close(out)
	for {
		select {
		case <-ctx.Done():
			return
		case change, ok := <-upstream:
			if !ok {
				return
			}
			if change.Err != nil {
				forwardChange(ctx, out, change)
				continue
			}
			next := SourceChange{}
			if err := s.Reload(); err != nil {
				next.Err = err
			}
			forwardChange(ctx, out, next)
		}
	}
}

// forwardChange sends change on out, honoring ctx cancellation so a
// stalled consumer cannot pin the goroutine.
func forwardChange(ctx context.Context, out chan<- SourceChange, change SourceChange) {
	select {
	case out <- change:
	case <-ctx.Done():
	}
}

// defaultFileOptions returns the construction-time defaults. Path
// expansion defaults on so callers don't have to opt in to the common
// case.
func defaultFileOptions() fileOptions {
	return fileOptions{
		pathExpansion: true,
	}
}

// resolveFilePath applies path expansion and search-paths lookup,
// returning the absolute resolved path. A missing file is not an
// error here — that's left to [readFileMaybeOptional].
func resolveFilePath(path string, cfg fileOptions) (string, error) {
	expand := cfg.pathExpansion
	if !cfg.pathExpansionSet {
		expand = true
	}
	if len(cfg.searchPaths) == 0 {
		return expandAndAbs(path, expand)
	}
	base := filepath.Base(path)
	var firstResolved string
	for _, dir := range cfg.searchPaths {
		expandedDir, err := expandPath(dir, expand)
		if err != nil {
			return "", err
		}
		candidate := filepath.Join(expandedDir, base)
		abs, err := filepath.Abs(candidate)
		if err != nil {
			return "", fmt.Errorf("recon: resolve %q: %w", candidate, err)
		}
		if firstResolved == "" {
			firstResolved = abs
		}
		if fileExists(abs) {
			return abs, nil
		}
	}
	// None existed; return the first candidate so the missing-file
	// branch has a stable address to report.
	return firstResolved, nil
}

func expandAndAbs(path string, expand bool) (string, error) {
	expanded, err := expandPath(path, expand)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("recon: resolve %q: %w", expanded, err)
	}
	return abs, nil
}

// expandPath applies POSIX-shell expansion to p via
// [expandShellPath]. Disabled expansion returns p unchanged.
func expandPath(p string, enabled bool) (string, error) {
	if !enabled {
		return p, nil
	}
	out, err := expandShellPath(p)
	if err != nil {
		return "", fmt.Errorf("recon: expand %q: %w", p, err)
	}
	return out, nil
}

// resolveFileCodec picks the codec for path. Precedence: explicit
// codec value > explicit format name > extension lookup.
func resolveFileCodec(path string, cfg fileOptions, codecs *Codecs) (Codec, error) {
	if cfg.codec != nil {
		return cfg.codec, nil
	}
	if codecs == nil {
		codecs = NewCodecs()
	}
	if cfg.format != "" {
		c, ok := codecs.ByName(cfg.format)
		if !ok {
			return nil, fmt.Errorf("%w: no codec registered for format %q",
				ErrUnsupportedFormat, cfg.format)
		}
		return c, nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	c, ok := codecs.ByExtension(ext)
	if !ok {
		return nil, fmt.Errorf("%w: no codec for extension %q (path %q)",
			ErrUnsupportedFormat, ext, path)
	}
	return c, nil
}

// readFileMaybeOptional reads path. With optional=true a missing file
// returns (nil, true, nil); other errors surface regardless.
func readFileMaybeOptional(path string, optional bool) (data []byte, missing bool, err error) {
	if !fileExists(path) {
		if optional {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("recon: file %q: %w", path, fs.ErrNotExist)
	}
	b, err := readFileCapped(path)
	if err != nil {
		if optional && errors.Is(err, fs.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("recon: read %q: %w", path, err)
	}
	return b, false, nil
}
