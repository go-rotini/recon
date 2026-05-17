package recon

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	rotinifs "github.com/go-rotini/fs"
)

// Save serializes the current snapshot through a codec and writes
// the resulting bytes to w. The codec is selected by
// [WithSaveFormat]; Save with no [WithSaveFormat] returns a wrapped
// [ErrUnsupportedFormat] because the [io.Writer] has no path
// extension to fall back on.
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

// unwrapValueDeep converts a [Value] into the plain-Go shape codecs
// expect: scalars become their underlying type, [SliceKind] becomes
// []any (recursively unwrapped), [MapKind] becomes map[string]any
// (recursively unwrapped). The function exists because [Value.Any]
// returns map[string]Value / []Value for compound kinds — useful for
// further chain-of-Value processing but unfit for direct serialization.
func unwrapValueDeep(v Value) any {
	switch v.Kind() {
	case SliceKind:
		s, ok := v.Any().([]Value)
		if !ok {
			return v.Any()
		}
		out := make([]any, len(s))
		for i, el := range s {
			out[i] = unwrapValueDeep(el)
		}
		return out
	case MapKind:
		m, ok := v.Any().(map[string]Value)
		if !ok {
			return v.Any()
		}
		out := make(map[string]any, len(m))
		for k, el := range m {
			out[k] = unwrapValueDeep(el)
		}
		return out
	default:
		return v.Any()
	}
}
