package recon

import "fmt"

// codec_shape.go — helpers that normalize a codec's decoded payload
// into the uniform shape the registry expects (map[string]any /
// []any trees, no implementation-specific node types).
//
// Codecs are required by their [Codec.Decode] contract to return
// only the documented leaf types. The YAML / TOML codecs use these
// helpers to collapse the occasional map[any]any (legacy keys,
// non-string roots) into the canonical map[string]any.

// normalizeMap collapses a decoder's root value into map[string]any.
// Accepts the canonical map[string]any (passed through after a
// recursive walk via [normalizeAnyMap]) and the legacy map[any]any
// some decoders return. Non-string keys are coerced via
// fmt.Sprint — lossy in theory but every config-shape input uses
// string keys in practice.
//
// The bool return reports whether v was a map of either shape; a
// non-map input returns (nil, false) so the calling codec can
// reject the root as a non-mapping.
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

// normalizeAnyMap recursively walks a map[string]any and rewrites
// any nested map[any]any / []any nodes through [normalizeAny] so
// the registry sees a uniform map[string]any / []any tree.
func normalizeAnyMap(m map[string]any) map[string]any {
	for k, v := range m {
		m[k] = normalizeAny(v)
	}
	return m
}

// normalizeAny is the per-element half of [normalizeAnyMap]. Maps
// are recursed into; slices have each element normalized; every
// other type is returned unchanged (recon's [NewValue] handles
// leaf coercion).
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
