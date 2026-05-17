package recon

import "strings"

// KeyTransform projects a recon [Path] onto the flat string a
// source's underlying store uses. Different sources spell the same
// configuration key differently:
//
//	Path{"server","port"}  ↦  "SERVER_PORT"        (env var)
//	Path{"server","port"}  ↦  "server-port"        (CLI flag)
//	Path{"server","port"}  ↦  "server.port"        (file source)
//
// A [KeyTransform] is a small, deterministic function callers can
// compose without recon ever shipping a hardcoded mapping for any
// one source type. The bundled transforms ([SnakeUpperTransform],
// [KebabTransform], [DotTransform], [IdentityTransform]) cover the
// common cases; users write their own when their store has an
// idiosyncratic naming convention.
//
// The reverse projection — flat-string back to [Path] — is
// transform-specific; sources that need it (e.g., to enumerate
// Keys()) carry both directions in their constructor wiring.
type KeyTransform func(p Path) string

// SnakeUpperTransform projects a path to ALL_CAPS_SNAKE form.
// Path{"server","port"} ↦ "SERVER_PORT". The default transform for
// env-var-backed sources.
//
// Path segments themselves keep their internal case; only the
// segment separator is replaced with "_". A path containing a
// segment with explicit lowercase / mixed case ("oauth2_client" as
// a single segment) is uppercased letter-by-letter.
func SnakeUpperTransform(p Path) string {
	if len(p) == 0 {
		return ""
	}
	return strings.ToUpper(strings.Join(p, "_"))
}

// SnakeUpperPrefixTransform returns a [KeyTransform] that prepends
// prefix to every path's ALL_CAPS_SNAKE form. Used by [OSEnvSource]
// when [WithEnvPrefix] is set:
//
//	WithEnvPrefix("APP_") + Path{"server","port"} ↦ "APP_SERVER_PORT"
//
// An empty prefix is equivalent to [SnakeUpperTransform].
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

// KebabTransform projects a path to kebab-case. Path{"server","port"}
// ↦ "server-port". The default for command-line-flag sources where
// the parser exposes flags as `--server-port`.
//
// Segments are not case-folded; only the separator becomes "-".
func KebabTransform(p Path) string {
	if len(p) == 0 {
		return ""
	}
	return strings.Join(p, "-")
}

// DotTransform projects a path to dot.notation form. The identity
// projection for file sources (YAML / TOML / JSONC / JSON / Dotenv)
// where the storage key matches the recon Path verbatim.
func DotTransform(p Path) string {
	if len(p) == 0 {
		return ""
	}
	return strings.Join(p, ".")
}

// IdentityTransform projects a single-segment path to its only
// segment, and a multi-segment path to its DotTransform form. Used
// by sources whose keys are recon-shaped already.
func IdentityTransform(p Path) string { return DotTransform(p) }

// parseSnakeUpper is the inverse of [SnakeUpperTransform] — it
// recovers a recon Path from a SNAKE_CASE string. Used by
// [OSEnvSource.Keys] to project env-var names back into the path
// space. The recovery is approximate: information about which
// underscores were separators vs. literal characters in the
// original path segments is lost on the round-trip; the recovered
// path treats every underscore as a separator.
//
// When prefix is non-empty, names not starting with prefix are
// rejected (returns an empty Path); matching names have the prefix
// stripped before splitting.
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
