package recon

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	rotinifs "github.com/go-rotini/fs"
)

// Save serializes the current snapshot through a codec and writes
// the resulting bytes to w. The codec is selected by
// [WithSaveFormat]; Save with no [WithSaveFormat] returns a wrapped
// [ErrUnsupportedFormat] because the [io.Writer] has no path
// extension to fall back on.
//
// The four save-flavored methods cover distinct destinations:
//
//   - Save: any [io.Writer] (network socket, buffer, stdout) — needs
//     [WithSaveFormat] because there's no extension to detect from.
//   - SaveTo: a file path — atomic write-temp + rename; format
//     detected from the path's extension.
//   - SaveString: same payload as Save, returned as a string —
//     convenience for callers feeding the result back into
//     templating / logging.
//   - GenerateTemplate: produces a stub config with defaults
//     pre-populated — the "myapp config init" entry point.
//
// The default policy is "safe to pipe anywhere":
//
//   - Secret-marked keys (via [Registry.MarkSecret] or a `secret`-
//     tagged [Bind] field) are redacted via the registry's
//     [WithSecretRedactor]. Pass [WithSaveIncludeSecrets] to emit
//     the verbatim value when the destination is known-private.
//   - Default-only keys (the snapshot's "default" provenance) are
//     omitted. Pass [WithSaveIncludeDefaults] to include them.
//   - A sub-tree filter ([WithSaveOnly]) limits the output to keys
//     under the named prefix.
//
// Save reads the registry's current snapshot atomically; concurrent
// reloads do NOT interleave with the encode.
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

// SaveTo is the path-aware variant of [Save]. The format is detected
// from path's extension when [WithSaveFormat] is not supplied; the
// file is created (or overwritten) and closed before SaveTo returns.
//
// The atomic-rename pattern (write to .tmp + rename) is used so a
// concurrent reader never observes a partially-written file. The
// temporary file lives in the same directory as path so the rename
// stays atomic on every supported filesystem.
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

	// Write to a temp file in the same directory, then rename. The
	// temp lives next to the target so the rename can stay atomic
	// even across exotic filesystems (overlayfs, tmpfs, …).
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
	_ = rotinifs.Exists // keep the import alive for future watch-aware integrations
	return nil
}

// encodeWith is the shared body of Save / SaveTo. The format and
// every per-call SaveOption have already been resolved into cfg.
func (r *Registry) encodeWith(w io.Writer, cfg saveOptions) error {
	codec, err := r.resolveSaveCodec(cfg.format)
	if err != nil {
		return err
	}

	snap := r.state.snapshot.Load()
	if snap == nil {
		// Empty snapshot — still produce a valid empty document.
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

// resolveSaveCodec looks up the codec by name in the registry's
// configured codec set. Returns a wrapped [ErrUnsupportedFormat]
// when no codec answers to that name.
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

// buildSaveOptions runs a fresh saveOptions through the supplied
// SaveOption closures. Centralized so Save / SaveTo share the option
// resolution.
func buildSaveOptions(opts []SaveOption) saveOptions {
	cfg := saveOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// buildSavePayload collapses snap into the map[string]any shape the
// codec encodes, applying the per-call filters in order:
//
//  1. Keep only canonical paths (skip alias entries).
//  2. Filter by [WithSaveOnly] prefix.
//  3. Filter out default-only keys unless [WithSaveIncludeDefaults].
//  4. Redact secret-marked keys unless [WithSaveIncludeSecrets].
//  5. Nest each surviving leaf into the result map.
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
					// No redactor: omit the key entirely rather than
					// emitting the verbatim secret.
					continue
				}
			}
		}

		// Strip the WithSaveOnly prefix from the output path so the
		// resulting document is a self-contained sub-tree.
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

// SaveString is a one-shot helper for callers that want the encoded
// form as a string without dealing with [io.Writer]. Returns the
// encoded payload + nil on success; an empty string + wrapped error
// otherwise.
func (r *Registry) SaveString(opts ...SaveOption) (string, error) {
	var b strings.Builder
	if err := r.Save(&b, opts...); err != nil {
		return "", err
	}
	return b.String(), nil
}

// GenerateTemplate emits a stub configuration document populated
// with the registry's currently-known keys, encoded in the requested
// format. Used to produce a starter config file (`myapp config init`
// → `config.example.yaml`) — defaults-included by default; secret-
// marked keys are redacted unless [WithSaveIncludeSecrets] is set.
//
// Returns a wrapped [ErrUnsupportedFormat] when format is unknown to
// the registry's codec set, or when format is empty.
func (r *Registry) GenerateTemplate(format string, opts ...SaveOption) ([]byte, error) {
	if err := r.validateNotClosed(); err != nil {
		return nil, err
	}
	if format == "" {
		return nil, fmt.Errorf("%w: GenerateTemplate requires a format",
			ErrUnsupportedFormat)
	}

	// Reuse the Save machinery: a template is just a Save with
	// defaults pre-enabled and a specific format pin.
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

// TemplateKeys returns the sorted list of paths [GenerateTemplate]
// would include with the supplied [SaveOption] set. Useful for
// "myapp config init --keys" tooling that wants to enumerate the
// fields before generating the file.
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
