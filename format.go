package recon

import (
	"path/filepath"
	"strings"
)

// Canonical codec names. A Codec.Name() value SHOULD use one of these strings
// when implementing one of the well-known formats so a same-format codec from
// a user shadows the bundled default by name (see Codecs.Register).
const (
	FormatYAML   = "yaml"
	FormatTOML   = "toml"
	FormatJSON   = "json"
	FormatJSONC  = "jsonc"
	FormatDotenv = "dotenv"
)

// extToFormat maps lowercase file extensions (including the leading dot) to
// canonical codec names. The Codecs registry walks every registered Codec's
// Extensions() and additionally consults this map as a fallback so an unset
// extension on a Codec doesn't break path-based detection. Users override by
// returning the appropriate extensions from their own Codec.Extensions().
var extToFormat = map[string]string{
	".yaml":  FormatYAML,
	".yml":   FormatYAML,
	".toml":  FormatTOML,
	".json":  FormatJSON,
	".jsonc": FormatJSONC,
	".json5": FormatJSONC, // JSON5 is a superset; jsonc parser handles it
	".env":   FormatDotenv,
}

// DetectFormat returns the canonical codec name for the given file path,
// determined by its extension. The second return is false if the extension
// is unknown.
//
// Detection is case-insensitive and operates only on the trailing extension
// — files like "config.local.yaml" still resolve to FormatYAML. Files with
// no extension return ("", false).
func DetectFormat(path string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return "", false
	}
	f, ok := extToFormat[ext]
	return f, ok
}
