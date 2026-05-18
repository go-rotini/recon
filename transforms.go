package recon

import "strings"

// KeyTransform projects a recon [Path] onto the flat string a source's
// underlying store uses. The same configuration key spells differently
// across sources:
//
//	Path{"server","port"}  ↦  "SERVER_PORT"   (env var)
//	Path{"server","port"}  ↦  "server-port"   (CLI flag)
//	Path{"server","port"}  ↦  "server.port"   (file source)
//
// The bundled transforms cover the common cases. The reverse
// projection (flat string → Path) is transform-specific; sources that
// need it ship both directions in their wiring.
type KeyTransform func(p Path) string

// SnakeUpperTransform projects Path{"server","port"} to "SERVER_PORT".
// The default for env-backed sources. Path segments keep their
// internal characters; only the separator is replaced with "_" and the
// whole result is uppercased.
func SnakeUpperTransform(p Path) string {
	if len(p) == 0 {
		return ""
	}
	return strings.ToUpper(strings.Join(p, "_"))
}

// SnakeUpperPrefixTransform returns a [KeyTransform] that prepends
// prefix to every path's SNAKE_UPPER form. An empty prefix is
// equivalent to [SnakeUpperTransform].
func SnakeUpperPrefixTransform(prefix string) KeyTransform {
	if prefix == "" {
		return SnakeUpperTransform
	}
	return func(p Path) string {
		if len(p) == 0 {
			return prefix
		}
		return prefix + strings.ToUpper(strings.Join(p, "_"))
	}
}

// KebabTransform projects Path{"server","port"} to "server-port". The
// default for CLI-flag sources.
func KebabTransform(p Path) string {
	if len(p) == 0 {
		return ""
	}
	return strings.Join(p, "-")
}

// DotTransform projects Path{"server","port"} to "server.port" — the
// identity projection for file sources whose storage key already
// matches the recon path.
func DotTransform(p Path) string {
	if len(p) == 0 {
		return ""
	}
	return strings.Join(p, ".")
}

// IdentityTransform is an alias for [DotTransform], named to make
// "this source's keys are already recon-shaped" explicit at call
// sites.
func IdentityTransform(p Path) string { return DotTransform(p) }

// parseSnakeUpper is the approximate inverse of [SnakeUpperTransform]:
// it recovers a Path from "FOO_BAR_BAZ", treating every underscore
// as a separator. Information about which underscores were literal in
// the original segments is lost.
//
// A non-empty prefix is stripped before splitting; names missing the
// prefix return nil.
func parseSnakeUpper(name, prefix string) Path {
	if prefix != "" {
		if !strings.HasPrefix(name, prefix) {
			return nil
		}
		name = name[len(prefix):]
	}
	if name == "" {
		return Path{}
	}
	parts := strings.Split(name, "_")
	out := make(Path, len(parts))
	for i, p := range parts {
		out[i] = strings.ToLower(p)
	}
	return out
}
