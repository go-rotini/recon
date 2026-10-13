package recon

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Save serializes the current snapshot through a codec and writes the
// bytes to w. The codec is named by [WithSaveFormat]; without it Save
// returns a wrapped [ErrUnsupportedFormat] since an [io.Writer] has no
// extension to detect from.
//
// Save, [Registry.SaveTo], [Registry.SaveString] and
// [Registry.GenerateTemplate] differ in destination: Save writes to any
// [io.Writer]; SaveTo writes to a file path with atomic
// write-temp-then-rename; SaveString returns the encoded form as a
// string; GenerateTemplate returns the bytes with defaults included.
//
// Default policy is safe to pipe anywhere:
//   - Secret-marked keys are redacted via [WithSecretRedactor] unless
//     [WithSaveIncludeSecrets] is set.
//   - Default-only keys are omitted unless [WithSaveIncludeDefaults]
//     is set.
//   - [WithSaveOnly] limits output to a sub-tree.
//   - A key whose value came from an [OSEnvSource] built without
//     [WithEnvPrefix], [WithEnvVars] or [WithEnvKeyParser] is omitted
//     unless a [Registry.Bind] read it: such a source reports every
//     variable in the process environment.
//
// Save encodes the whole registry, even on a [Registry.Sub] view. It
// reads one snapshot, so concurrent reloads do not interleave with the
// encode.
func (r *Registry) Save(w io.Writer, opts ...SaveOption) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	cfg := buildSaveOptions(opts)
	if cfg.format == "" {
		return fmt.Errorf("%w: Save requires WithSaveFormat (Writer has no extension)",
			ErrUnsupportedFormat)
	}
	return r.encodeWith(w, cfg)
}

// SaveTo is [Registry.Save] to a file. The format is detected from
// path's extension when [WithSaveFormat] is not supplied. The bytes go
// to a temp file next to path, which is synced to disk and then renamed
// over it, and the directory is synced after the rename where the
// platform allows. When path is a symlink the file it points to is
// written and the link is kept. An existing file keeps its permissions
// and, where the caller is allowed to set them, its owner and group; a
// new file is created owner-only (0600).
func (r *Registry) SaveTo(path string, opts ...SaveOption) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	cfg := buildSaveOptions(opts)
	if cfg.format == "" {
		if name, ok := DetectFormat(path); ok {
			cfg.format = name
		}
	}
	if cfg.format == "" {
		return fmt.Errorf("%w: cannot determine format from path %q (pass WithSaveFormat)",
			ErrUnsupportedFormat, path)
	}

	target := resolveSymlinks(path)
	var existing fs.FileInfo
	if info, statErr := os.Stat(target); statErr == nil {
		existing = info
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".*.tmp")
	if err != nil {
		return fmt.Errorf("recon: SaveTo create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	writeErr := r.encodeWith(tmp, cfg)
	if writeErr == nil {
		writeErr = finishTemp(tmp, existing)
	}
	closeErr := tmp.Close()
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("recon: SaveTo close tmp: %w", closeErr)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("recon: SaveTo rename %q → %q: %w", tmpPath, target, err)
	}
	syncDir(filepath.Dir(target))
	return nil
}

// finishTemp gives the temp file the permissions, and best effort the
// owner, of the file it replaces (existing, nil for a new file) and
// flushes it to disk before the rename.
func finishTemp(tmp *os.File, existing fs.FileInfo) error {
	if existing != nil {
		keepOwner(tmp, existing)
		if err := tmp.Chmod(existing.Mode().Perm()); err != nil {
			return fmt.Errorf("recon: SaveTo chmod tmp: %w", err)
		}
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("recon: SaveTo sync tmp: %w", err)
	}
	return nil
}

// maxSymlinkHops bounds [resolveSymlinks] on a symlink loop.
const maxSymlinkHops = 40

// resolveSymlinks follows path while it is a symlink, even a dangling
// one, and returns the file it ends at, so a save writes through the
// link instead of replacing it.
func resolveSymlinks(path string) string {
	for range maxSymlinkHops {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&fs.ModeSymlink == 0 {
			return path
		}
		link, err := os.Readlink(path)
		if err != nil {
			return path
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(path), link)
		}
		path = link
	}
	return path
}

// encodeWith is the shared body of [Save] / [SaveTo].
func (r *Registry) encodeWith(w io.Writer, cfg saveOptions) error {
	codec, err := r.resolveSaveCodec(cfg.format)
	if err != nil {
		return err
	}

	snap := r.state.snapshot.Load()
	if snap == nil {
		// Empty snapshot still produces a valid empty document.
		b, encErr := codec.Encode(map[string]any{})
		if encErr != nil {
			return fmt.Errorf("recon: Save encode: %w", encErr)
		}
		_, writeErr := w.Write(b)
		if writeErr != nil {
			return fmt.Errorf("recon: Save write: %w", writeErr)
		}
		return nil
	}

	r.state.mu.Lock()
	policy := r.savePolicyLocked()
	r.state.mu.Unlock()

	payload := buildSavePayload(snap, cfg, policy)
	b, err := codec.Encode(payload)
	if err != nil {
		return fmt.Errorf("recon: Save encode (%s): %w", codec.Name(), err)
	}
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("recon: Save write: %w", err)
	}
	return nil
}

// resolveSaveCodec looks the codec named format up in the registry's
// codec set. Returns wrapped [ErrUnsupportedFormat] when unknown.
func (r *Registry) resolveSaveCodec(format string) (Codec, error) {
	return lookupCodec(r.state.opts.codecs, format)
}

func buildSaveOptions(opts []SaveOption) saveOptions {
	cfg := saveOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// buildSavePayload collapses snap into the map shape the codec
// encodes, applying the save filters in order: skip alias entries,
// filter by [WithSaveOnly] prefix, drop default-only keys unless
// [WithSaveIncludeDefaults], redact secrets unless
// [WithSaveIncludeSecrets], and drop keys an unscoped environment
// source supplied that no bind read.
func buildSavePayload(snap *Snapshot, cfg saveOptions, policy savePolicy) map[string]any {
	out := map[string]any{}
	prefix := ParsePath(cfg.onlyPrefix)

	idx := make([]int, 0, len(snap.keys))
	for i, p := range snap.keys {
		ps := snap.keyStrs[i]
		if _, isAlias := snap.aliases[ps]; isAlias {
			continue
		}
		if len(prefix) > 0 && !p.HasPrefix(prefix) {
			continue
		}
		srcs := snap.sources[ps]
		if !cfg.includeDefaults && len(srcs) == 1 && srcs[0] == srcDefault {
			continue
		}
		if len(srcs) > 0 && policy.unscopedEnv[srcs[0]] {
			if _, read := policy.consulted[ps]; !read {
				continue
			}
		}
		idx = append(idx, i)
	}
	shadowed := snap.shadowedAmong(idx)

	for _, i := range idx {
		p, ps := snap.keys[i], snap.keyStrs[i]
		if _, drop := shadowed[ps]; drop {
			continue
		}
		v := snap.values[ps]
		leaf := unwrapValueDeep(v)
		if policy.secrets.has(ps) {
			if !cfg.includeSecrets {
				if policy.redactor != nil {
					leaf = policy.redactor(v.String())
				} else {
					// No redactor: omit rather than emit the secret.
					continue
				}
			}
		}

		// Strip the WithSaveOnly prefix so the document is a
		// self-contained sub-tree.
		writePath := p
		if len(prefix) > 0 {
			writePath = p.After(prefix)
			if len(writePath) == 0 {
				continue
			}
		}
		setNested(out, writePath, leaf)
	}
	return out
}

// savePolicy is the registry state the save filters need.
type savePolicy struct {
	secrets     secretMatcher
	redactor    func(string) string
	unscopedEnv map[string]bool     // names of OSEnvSources reading every variable
	consulted   map[string]struct{} // paths a bind has read
}

// savePolicyLocked snapshots the save filters' inputs. Caller must hold
// r.state.mu.
func (r *Registry) savePolicyLocked() savePolicy {
	p := savePolicy{
		secrets:   r.secretMatcherLocked(),
		redactor:  r.state.opts.secretRedactor,
		consulted: cloneStringSet(r.state.consulted),
	}
	for _, s := range r.state.sources {
		if env, ok := s.(*OSEnvSource); ok && !env.scoped {
			if p.unscopedEnv == nil {
				p.unscopedEnv = map[string]bool{}
			}
			p.unscopedEnv[s.Name()] = true
		}
	}
	return p
}

// SaveString is [Registry.Save] returning the encoded form as a string.
func (r *Registry) SaveString(opts ...SaveOption) (string, error) {
	var b strings.Builder
	if err := r.Save(&b, opts...); err != nil {
		return "", err
	}
	return b.String(), nil
}

// GenerateTemplate encodes the current snapshot in format with
// default-only keys included, for writing a starter config file.
// Secret-marked keys are redacted unless [WithSaveIncludeSecrets].
// Returns wrapped [ErrUnsupportedFormat] when format is unknown or
// empty.
func (r *Registry) GenerateTemplate(format string, opts ...SaveOption) ([]byte, error) {
	if err := r.validateNotClosed(); err != nil {
		return nil, err
	}
	if format == "" {
		return nil, fmt.Errorf("%w: GenerateTemplate requires a format",
			ErrUnsupportedFormat)
	}

	cfg := saveOptions{format: format, includeDefaults: true}
	for _, opt := range opts {
		opt(&cfg)
	}

	codec, err := r.resolveSaveCodec(cfg.format)
	if err != nil {
		return nil, err
	}

	snap := r.state.snapshot.Load()
	if snap == nil {
		b, encErr := codec.Encode(map[string]any{})
		if encErr != nil {
			return nil, fmt.Errorf("recon: GenerateTemplate encode: %w", encErr)
		}
		return b, nil
	}

	r.state.mu.Lock()
	policy := r.savePolicyLocked()
	r.state.mu.Unlock()

	payload := buildSavePayload(snap, cfg, policy)
	b, err := codec.Encode(payload)
	if err != nil {
		return nil, fmt.Errorf("recon: GenerateTemplate encode (%s): %w",
			codec.Name(), err)
	}
	return b, nil
}

// TemplateKeys returns the sorted canonical paths of the current
// snapshot, limited by [WithSaveOnly] (other options are ignored). The
// paths keep the [WithSaveOnly] prefix.
func (r *Registry) TemplateKeys(opts ...SaveOption) []Path {
	if r.validateNotClosed() != nil {
		return nil
	}
	snap := r.state.snapshot.Load()
	if snap == nil {
		return nil
	}
	cfg := saveOptions{includeDefaults: true}
	for _, opt := range opts {
		opt(&cfg)
	}
	prefix := ParsePath(cfg.onlyPrefix)
	keys := make([]string, 0, len(snap.keys))
	for _, p := range snap.keys {
		ps := p.String()
		if _, isAlias := snap.aliases[ps]; isAlias {
			continue
		}
		if len(prefix) > 0 && !p.HasPrefix(prefix) {
			continue
		}
		keys = append(keys, ps)
	}
	sort.Strings(keys)
	paths := make([]Path, len(keys))
	for i, s := range keys {
		paths[i] = ParsePath(s)
	}
	return paths
}
