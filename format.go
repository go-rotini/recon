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

// extToFormat maps a lowercased extension (with the leading dot) to its
// canonical codec name. [Codecs.ByExtension] consults it as a fallback
// when no registered codec advertises the extension. A switch, not a
// map, so nothing is built at package init.
func extToFormat(ext string) (string, bool) {
	switch ext {
	case ".yaml", ".yml":
		return FormatYAML, true
	case ".toml":
		return FormatTOML, true
	case ".json":
		return FormatJSON, true
	case ".jsonc", ".json5": // .json5 is parsed as JSONC; JSON5-only syntax is rejected
		return FormatJSONC, true
	case ".env":
		return FormatDotenv, true
	}
	return "", false
}

// DetectFormat returns the canonical codec name for path's extension.
// Case-insensitive; only the trailing extension is examined (so
// "config.local.yaml" resolves to [FormatYAML]).
func DetectFormat(path string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return "", false
	}
	return extToFormat(ext)
}
