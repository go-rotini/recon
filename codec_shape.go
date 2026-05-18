package recon

import "fmt"

// Helpers that collapse a codec's decoded payload into the uniform
// map[string]any / []any tree the registry expects. Codecs that may
// return map[any]any (YAML, TOML) route their output through
// [normalizeMap] at the [Codec.Decode] boundary.

// normalizeMap collapses v into map[string]any. Accepts map[string]any
// (passed through via [normalizeAnyMap]) and the legacy map[any]any;
// non-string keys are coerced through fmt.Sprint. Returns (nil, false)
// when v is not a map of either shape so the caller can reject the
// root.
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

// normalizeAnyMap recursively rewrites any nested map[any]any nodes
// in m so the registry sees a uniform tree.
func normalizeAnyMap(m map[string]any) map[string]any {
	for k, v := range m {
		m[k] = normalizeAny(v)
	}
	return m
}

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
