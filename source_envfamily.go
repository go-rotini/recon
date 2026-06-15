package recon

import (
	"os"
	"slices"
	"strings"
)

// EnvFamily describes one nested group of environment variables. Every
// variable named Base+Separator+K1[+Separator+K2...] is collected into a
// nested map[string]any rooted at Target: with Base "ACME_HTTP" and Separator
// "__", ACME_HTTP__RETRY__MAX=9 contributes {retry: {max: "9"}}. Segments are
// lowercased; values stay strings.
type EnvFamily struct {
	// Target is the recon path the assembled family binds to.
	Target Path
	// Base is the variable-name prefix that introduces the family.
	Base string
	// Separator joins Base to each nested segment (and the segments to one
	// another). An empty Separator disables the family.
	Separator string
}

// envFamilySource assembles [EnvFamily] groups from the process environment.
type envFamilySource struct {
	name     string
	families []EnvFamily
}

// NewEnvFamilySource returns a [Source] that collects each family's
// Base<Separator>… variables from the process environment into a nested
// map[string]any and surfaces it as one [MapKind] [Value] at the family's
// Target. Unlike [OSEnvSource] — which maps each path to a single variable —
// this lets a map-typed field bind an open-ended group of variables at once.
//
// A family with no matching variables contributes no key, so a Target bound to
// a required field reports the standard missing-required error.
func NewEnvFamilySource(name string, families ...EnvFamily) Source {
	return &envFamilySource{name: name, families: families}
}

// Name returns the source identifier.
func (s *envFamilySource) Name() string { return s.name }

// Get returns the family rooted at path as a [MapKind] value, or
// (Value{}, false, nil) when no family targets path or none of its variables
// are set.
func (s *envFamilySource) Get(path Path) (Value, bool, error) {
	for _, fam := range s.families {
		if fam.Separator == "" || !fam.Target.Equal(path) {
			continue
		}
		if m := collectEnvFamily(fam.Base, fam.Separator); len(m) > 0 {
			return NewValue(m), true, nil
		}
	}
	return Value{}, false, nil
}

// Keys reports the Target of every family that currently has at least one
// matching variable.
func (s *envFamilySource) Keys() []Path {
	var out []Path
	for _, fam := range s.families {
		if fam.Separator == "" {
			continue
		}
		if m := collectEnvFamily(fam.Base, fam.Separator); len(m) > 0 {
			out = append(out, fam.Target.Clone())
		}
	}
	slices.SortFunc(out, func(a, b Path) int {
		switch {
		case a.String() < b.String():
			return -1
		case a.String() > b.String():
			return 1
		default:
			return 0
		}
	})
	return out
}

// Close is a no-op.
func (s *envFamilySource) Close() error { return nil }

// collectEnvFamily scans the process environment for Base+Separator+…
// variables and assembles the nested map. Segments are lowercased; the
// separator is matched case-insensitively. An empty result means no variable
// matched.
func collectEnvFamily(base, sep string) map[string]any {
	out := map[string]any{}
	prefix := base + sep
	lowerSep := strings.ToLower(sep)
	for _, kv := range os.Environ() {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
			continue
		}
		segs := strings.Split(strings.ToLower(name[len(prefix):]), lowerSep)
		setNestedSegments(out, segs, val)
	}
	return out
}

// setNestedSegments stores val at the segment path in m, creating intermediate
// maps as needed.
func setNestedSegments(m map[string]any, segs []string, val any) {
	for _, seg := range segs[:len(segs)-1] {
		next, ok := m[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[seg] = next
		}
		m = next
	}
	m[segs[len(segs)-1]] = val
}
