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

	rotinifs "github.com/go-rotini/fs"
)

// FileSource is a codec-driven [Source] backed by a single file on the
// local filesystem. The file's bytes are read at construction and decoded
// through whichever [Codec] matches its extension; later [Source.Get]
// calls read from the decoded map (the same in-memory shape a
// [MapSource] holds).
//
// FileSource is the workhorse for application config: pair it with
// [WithFileCodec] / [WithSearchPaths] / [WithPathExpansion] / [WithOptional]
// to express the full "look in N directories, expand ~, treat missing as
// no-op" pattern in a single constructor call.
//
// FileSource implements [Watcher] when constructed against an existing
// file — the registry-wide [WatcherFactory] (or a per-source override via
// [WithFileWatcher]) is responsible for emitting [SourceChange] events on
// every file modification. The Phase 4 watcher factory is wired in
// Phase 5; until then, FileSource.Watch returns a closed channel.
type FileSource struct {
	*MapSource

	// resolvedPath is the absolute, expanded path the file was read from.
	// Stored for [FileSource.Path] and for the watcher hookup.
	resolvedPath string

	// codec is the codec the constructor selected (either explicitly via
	// [WithFileCodec] / [WithFileFormat] or by extension lookup).
	codec Codec

	// optional records whether the constructor accepted a missing file
	// without error. Persisted so a Reload that finds the file freshly
	// created can pick it up.
	optional bool

	// watcher is the per-source override, if any. nil means "use the
	// registry-wide factory" (Phase 5).
	watcher WatcherFactory

	// missing reports whether the source was constructed against a file
	// that did not exist (only meaningful when optional). Atomic so
	// future Watcher hookups can flip it under a re-read.
	missing atomic.Bool

	// reload guards against concurrent Reload calls racing each other.
	reload sync.Mutex
}

// NewFileSource constructs a [FileSource] for path, decoded through
// whichever codec matches. Options may be empty — the defaults are
// path-expansion-on, optional-off, codec-by-extension.
//
// Codec resolution order:
//  1. [WithFileCodec] — explicit codec value (wins outright).
//  2. [WithFileFormat] — explicit codec name; looked up in [DefaultCodecs].
//  3. The file extension — looked up via [Codecs.ByExtension].
//
// When the codec cannot be resolved the constructor returns a wrapped
// [ErrUnsupportedFormat]. Decode failures surface as [*ParseError].
//
// The source's [Source.Name] is the basename of the resolved file path
// (so "/etc/myapp/config.yaml" → "config.yaml"); callers wanting a
// different name should pass [WithFileFormat] and then wrap with a
// rename helper, or use [NewFileSourceFS] with an explicit name.
func NewFileSource(path string, opts ...FileOption) (Source, error) {
	cfg := defaultFileOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	resolved, err := resolveFilePath(path, cfg)
	if err != nil {
		return nil, err
	}

	codec, err := resolveFileCodec(resolved, cfg, DefaultCodecs())
	if err != nil {
		return nil, err
	}

	name := filepath.Base(resolved)
	data, missing, err := readFileMaybeOptional(resolved, cfg.optional)
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

	src := &FileSource{
		MapSource:    NewMapSource(name, decoded),
		resolvedPath: resolved,
		codec:        codec,
		optional:     cfg.optional,
		watcher:      cfg.watcher,
	}
	if missing {
		src.missing.Store(true)
	}
	return src, nil
}

// Path returns the absolute, expanded path the source is reading from.
// Useful for diagnostic output and for tooling that wants to display the
// resolved location regardless of whether the caller passed a relative
// path or one containing `~` / `$VAR`.
func (s *FileSource) Path() string { return s.resolvedPath }

// Format returns the canonical codec name driving this source's decode.
func (s *FileSource) Format() string {
	if s.codec == nil {
		return ""
	}
	return s.codec.Name()
}

// Reload re-reads the file from disk and swaps the underlying [MapSource]
// contents atomically. Called by the watcher hookup (Phase 5) on every
// file-changed event; callers may invoke it directly for manual refresh.
//
// On a missing-file outcome with WithOptional set, Reload empties the
// source's keys without returning an error. On any decode failure the
// existing contents are retained and the error is returned.
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
	decoded, err := s.codec.Decode(data)
	if err != nil {
		return &ParseError{Source: s.Name(), Cause: err}
	}
	s.Replace(decoded)
	return nil
}

// Watch returns a [SourceChange] channel for the underlying file. The
// per-source watcher factory ([WithFileWatcher]) takes precedence; when
// none is set, the caller is expected to supply one — typically the
// registry-wide [WithWatcher] factory threaded through the watch engine.
// A nil factory makes Watch a no-op (returns a closed channel) so the
// optional-watch contract on [Source] stays satisfiable.
//
// On every notification from the underlying factory, Watch first re-reads
// and decodes the file via [FileSource.Reload], then forwards a
// [SourceChange] downstream so the registry's reload engine recomputes
// the snapshot.
func (s *FileSource) Watch(ctx context.Context) (<-chan SourceChange, error) {
	factory := s.watcher
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

// SetWatcher attaches a [WatcherFactory] to a [FileSource] after
// construction. Used by the registry to thread its registry-wide
// factory into sources that did not get a per-source factory via
// [WithFileWatcher].
//
// Calling SetWatcher after [FileSource.Watch] has been invoked has no
// effect on the running subscription; restart the subscription to pick
// up the new factory.
func (s *FileSource) SetWatcher(w WatcherFactory) {
	s.reload.Lock()
	defer s.reload.Unlock()
	s.watcher = w
}

// fanWatchEvents bridges the upstream factory's channel into the
// FileSource's downstream channel. On every upstream notification it
// runs [FileSource.Reload] and forwards a SourceChange describing the
// outcome — empty Keys (whole-source refresh) on success, non-nil Err
// when the reload itself fails.
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

// forwardChange writes change to out, honoring ctx-cancellation so a
// stalled downstream consumer does not pin the goroutine.
func forwardChange(ctx context.Context, out chan<- SourceChange, change SourceChange) {
	select {
	case out <- change:
	case <-ctx.Done():
	}
}

// defaultFileOptions returns the fileOptions struct fresh callers see.
// Path expansion defaults to on so callers don't have to opt in to the
// common case.
func defaultFileOptions() fileOptions {
	return fileOptions{
		pathExpansion: true,
	}
}

// resolveFilePath finds the actual file the constructor will read. It
// applies path expansion (when enabled), walks [WithSearchPaths] (when
// supplied), and returns the absolute resolved path.
//
// A missing file is NOT an error here — the optional / required policy is
// applied later by [readFileMaybeOptional].
func resolveFilePath(path string, cfg fileOptions) (string, error) {
	expand := cfg.pathExpansion
	if !cfg.pathExpansionSet {
		expand = true
	}
	if len(cfg.searchPaths) == 0 {
		return expandAndAbs(path, expand)
	}
	// Search paths: treat path as a filename, look it up under each dir.
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
		if rotinifs.Exists(abs) {
			return abs, nil
		}
	}
	// None existed — return the first candidate so the missing-file path
	// has a stable address to report.
	return firstResolved, nil
}

// expandAndAbs runs the expansion + Abs pipeline a single-path resolve
// follows. Split out because it's used by both the single-path and
// search-paths branches.
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

// expandPath applies POSIX-shell-style expansion to p via
// [expandShellPath] — tilde forms (`~`, `~user`), bare and braced
// env-var references, and the `${VAR-def}` / `${VAR:-def}` /
// `${VAR:?msg}` / `${VAR:+alt}` / `${VAR+alt}` POSIX modifiers.
// A disabled-expansion call returns p unchanged.
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

// resolveFileCodec chooses the codec for a FileSource. The precedence
// order is: explicit codec value > explicit format name > extension
// lookup. A failed resolution returns a wrapped [ErrUnsupportedFormat].
func resolveFileCodec(path string, cfg fileOptions, codecs *Codecs) (Codec, error) {
	if cfg.codec != nil {
		return cfg.codec, nil
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

// readFileMaybeOptional reads the named file. When optional is true and
// the file does not exist, the returned (nil, true, nil) signals "missing
// — proceed with empty data." Other I/O errors surface as wrapped errors
// regardless of the optional flag.
func readFileMaybeOptional(path string, optional bool) (data []byte, missing bool, err error) {
	if !rotinifs.Exists(path) {
		if optional {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("recon: file %q: %w", path, fs.ErrNotExist)
	}
	b, err := rotinifs.ReadFile(path)
	if err != nil {
		if optional && errors.Is(err, fs.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("recon: read %q: %w", path, err)
	}
	return b, false, nil
}
