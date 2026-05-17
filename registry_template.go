package recon

import (
	"fmt"
	"sort"
)

// GenerateTemplate emits a stub configuration document populated
// with the registry's known default values. The output is encoded
// in the requested format (one of the [FormatYAML] / [FormatTOML] /
// [FormatJSONC] / [FormatJSON] / [FormatDotenv] constants, or any
// codec the registry has registered).
//
// The template includes every key with a [Registry.SetDefault] value
// at its current snapshot — the "what the program would resolve if
// no sources provided anything" view. Keys whose only source is a
// registered source (not a default) are also included so callers see
// every key the registry currently knows about; secret-marked keys
// are redacted via the registry's [WithSecretRedactor] unless the
// caller opts in via [WithSaveIncludeSecrets].
//
// Returns a wrapped [ErrUnsupportedFormat] when format is unknown to
// the registry's codec set.
//
// GenerateTemplate is the "myapp config init" entry point: produce
// a starter file with defaults filled in, ready to commit.
func (r *Registry) GenerateTemplate(format string, opts ...SaveOption) ([]byte, error) {
	if err := r.validateNotClosed(); err != nil {
		return nil, err
	}
	if format == "" {
		return nil, fmt.Errorf("%w: GenerateTemplate requires a format",
			ErrUnsupportedFormat)
	}

	// Reuse the Save machinery: a template is just a Save with
	// defaults included by default (heh) and a specific format
	// override.
	cfg := saveOptions{
		format:          format,
		includeDefaults: true,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	// User can override includeDefaults via WithSaveIncludeDefaults
	// being passed as a no-op (it sets to true again). To switch it
	// off entirely they'd need a hypothetical WithoutDefaults — kept
	// out of the API for now since "template without defaults"
	// makes little sense.

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

// TemplateKeys returns the sorted list of paths GenerateTemplate
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
	var out []string
	for _, p := range snap.keys {
		ps := p.String()
		if _, isAlias := snap.aliases[ps]; isAlias {
			continue
		}
		if len(prefix) > 0 && !p.HasPrefix(prefix) {
			continue
		}
		out = append(out, ps)
	}
	sort.Strings(out)
	paths := make([]Path, len(out))
	for i, s := range out {
		paths[i] = ParsePath(s)
	}
	return paths
}
