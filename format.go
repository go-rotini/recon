package recon

import (
	"path/filepath"
	"strings"
)

// Canonical codec names. A [Codec.Name] implementation should use one
// of these strings when implementing a well-known format so a custom
// codec shadows the bundled default by name.
const (
	FormatYAML   = "yaml"
	FormatTOML   = "toml"
	FormatJSON   = "json"
	FormatJSONC  = "jsonc"
	FormatDotenv = "dotenv"
)

// extToFormat maps lowercased extensions (with the leading dot) to
// canonical codec names. [Codecs.ByExtension] consults this map as a
// fallback when no registered codec advertises the extension.
var extToFormat = map[string]string{
	".yaml":  FormatYAML,
	".yml":   FormatYAML,
	".toml":  FormatTOML,
	".json":  FormatJSON,
	".jsonc": FormatJSONC,
	".json5": FormatJSONC, // JSON5 is a superset handled by the jsonc parser.
	".env":   FormatDotenv,
}

// DetectFormat returns the canonical codec name for path's extension.
// Case-insensitive; only the trailing extension is examined (so
// "config.local.yaml" resolves to [FormatYAML]).
func DetectFormat(path string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return "", false
	}
	f, ok := extToFormat[ext]
	return f, ok
}
