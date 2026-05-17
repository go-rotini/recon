package recon

import (
	"fmt"

	"github.com/go-rotini/yaml"
)

// yamlCodec wraps go-rotini/yaml. The wrapper is intentionally thin — it
// delegates parsing and serialization to the sibling package and only
// normalizes the resulting map shape so the registry never sees a
// yaml.Node or other yaml-internal type.
//
// YAML's permissive type system (block scalars, flow style, tagged values)
// is handled inside the yaml package; by the time Decode returns, every
// leaf is one of the documented recon leaf types.
type yamlCodec struct{}

// YAML is the package-level [Codec] for YAML 1.2.2 documents (and the
// KYAML strict subset). Registered by [New] in the default codec set;
// available for explicit selection via the With*Codec options.
var YAML Codec = yamlCodec{}

func (yamlCodec) Name() string         { return FormatYAML }
func (yamlCodec) Extensions() []string { return []string{".yaml", ".yml"} }

// Decode parses data as a YAML document into a map[string]any. The
// document root must be a mapping; sequences or scalars at the top level
// are rejected as unsupported because [Source.Get] requires key/value
// addressing.
func (yamlCodec) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("recon: yaml decode: %w", err)
	}
	m, ok := normalizeMap(v)
	if !ok {
		return nil, fmt.Errorf("%w: yaml root must be a mapping, got %T",
			ErrUnsupportedFormat, v)
	}
	return m, nil
}

// Encode serializes v as a YAML document.
func (yamlCodec) Encode(v map[string]any) ([]byte, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("recon: yaml encode: %w", err)
	}
	return b, nil
}

// normalizeMap collapses YAML / TOML decoders that occasionally return
// map[any]any (legacy keys) into map[string]any. Non-string keys are
// coerced via fmt.Sprint; the conversion is lossy in theory but in
// practice every config-shape input uses string keys.
func normalizeMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return normalizeAnyMap(m), true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[fmt.Sprint(k)] = normalizeAny(val)
		}
		return out, true
	default:
		return nil, false
	}
}

// normalizeAnyMap recursively walks a map[string]any and rewrites any
// nested map[any]any / []any nodes through [normalizeAny] so the registry
// sees a uniform map[string]any / []any tree.
func normalizeAnyMap(m map[string]any) map[string]any {
	for k, v := range m {
		m[k] = normalizeAny(v)
	}
	return m
}

// normalizeAny is the per-element half of [normalizeAnyMap]. Maps are
// recursed into; slices have each element normalized; every other type is
// returned unchanged (recon's [NewValue] handles the leaf coercion).
func normalizeAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return normalizeAnyMap(x)
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[fmt.Sprint(k)] = normalizeAny(val)
		}
		return out
	case []any:
		for i, el := range x {
			x[i] = normalizeAny(el)
		}
		return x
	default:
		return v
	}
}
