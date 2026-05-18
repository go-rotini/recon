package recon

import (
	"fmt"
	"io"
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
// Save / [SaveTo] / [SaveString] / [GenerateTemplate] differ in
// destination: Save writes to any [io.Writer]; SaveTo writes to a
// file path with atomic write-temp-then-rename; SaveString returns
// the encoded form as a string; GenerateTemplate emits a stub
// document with defaults included.
//
// Default policy is safe to pipe anywhere:
//   - Secret-marked keys are redacted via [WithSecretRedactor] unless
//     [WithSaveIncludeSecrets] is set.
//   - Default-only keys are omitted unless [WithSaveIncludeDefaults]
//     is set.
//   - [WithSaveOnly] limits output to a sub-tree.
//
// Save reads the current snapshot atomically; concurrent reloads do
// not interleave with the encode.
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

// SaveTo is the path-aware [Save]. The format is detected from path's
// extension when [WithSaveFormat] is not supplied. The temp file lives
// next to the target so the rename stays atomic across exotic
// filesystems.
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

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("recon: SaveTo create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	encodeErr := r.encodeWith(tmp, cfg)
	closeErr := tmp.Close()
	if encodeErr != nil {
		_ = os.Remove(tmpPath)
		return encodeErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("recon: SaveTo close tmp: %w", closeErr)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("recon: SaveTo rename %q → %q: %w", tmpPath, path, err)
	}
	return nil
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
	secrets := cloneStringSet(r.state.secretKeys)
	redactor := r.state.opts.secretRedactor
	r.state.mu.Unlock()

	payload := buildSavePayload(snap, cfg, secrets, redactor)
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
	codecs := r.state.opts.codecs
	if codecs == nil {
		codecs = DefaultCodecs()
	}
	c, ok := codecs.ByName(format)
	if !ok {
		return nil, fmt.Errorf("%w: no codec registered for format %q",
			ErrUnsupportedFormat, format)
	}
	return c, nil
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
// [WithSaveIncludeSecrets].
func buildSavePayload(
	snap *Snapshot,
	cfg saveOptions,
	secrets map[string]struct{},
	redactor func(string) string,
) map[string]any {
	out := map[string]any{}
	prefix := ParsePath(cfg.onlyPrefix)

	for _, p := range snap.keys {
		ps := p.String()
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

		v := snap.values[ps]
		leaf := unwrapValueDeep(v)
		if _, isSecret := secrets[ps]; isSecret {
			if !cfg.includeSecrets {
				if redactor != nil {
					leaf = redactor(v.String())
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

// SaveString returns the encoded form as a string.
func (r *Registry) SaveString(opts ...SaveOption) (string, error) {
	var b strings.Builder
	if err := r.Save(&b, opts...); err != nil {
		return "", err
	}
	return b.String(), nil
}

// GenerateTemplate emits a stub configuration document with defaults
// included, encoded in format. Used to produce a starter config file.
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
	secrets := cloneStringSet(r.state.secretKeys)
	redactor := r.state.opts.secretRedactor
	r.state.mu.Unlock()

	payload := buildSavePayload(snap, cfg, secrets, redactor)
	b, err := codec.Encode(payload)
	if err != nil {
		return nil, fmt.Errorf("recon: GenerateTemplate encode (%s): %w",
			codec.Name(), err)
	}
	return b, nil
}

// TemplateKeys returns the sorted paths [GenerateTemplate] would
// include with opts. Useful for `config init --keys` tooling.
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
