package recon

import (
	"strings"
)

// TagName is the default struct-tag the decoder reads. Override per-call via
// [WithDecodeTag]. The decoder falls back through "env", "json", "yaml",
// "toml" (in that order) when the primary tag is absent on a field — this is
// the interop story documented in §6.4 of the requirements.
const TagName = "recon"

// fallbackTagNames lists the tag names consulted in order when the primary
// tag (TagName, or the one supplied via WithDecodeTag) is empty on a field.
// Order matters: an `env:"NAME"` tag wins over a `json:"name"` tag because
// recon's role as the env-loader successor is more authoritative than
// stdlib-JSON shape.
//
//nolint:unused // consumed by the decoder in Phase 6 (registry_bind.go).
var fallbackTagNames = [...]string{"env", "json", "yaml", "toml"}

// FieldTag is the parsed form of a single struct-tag value. The decoder
// consumes one per exported field of a bind target. A bare name with no
// options yields a FieldTag with Name set and every other field zero.
//
// The tag grammar follows encoding/json (and go-rotini/env):
//
//	recon:"name,opt1,opt2=value,opt3"
//
// Names may be empty (the bare `recon:",required"` form), in which case the
// decoder falls back to the Go field name with the registry's key-transform
// applied.
type FieldTag struct {
	// Name is the canonical key. An empty Name signals "use the field name."
	Name string

	// Skip is true when the tag value is exactly "-" (field is explicitly
	// excluded from binding).
	Skip bool

	// Path overrides the inferred path. Set via `path=server.port`. When
	// empty the path is derived from Name + any embedded-struct prefix.
	Path string

	// Source pins this field to a specific source by name. Set via
	// `source=env`. Empty means "use the registry's normal precedence."
	Source string

	// Format hints that the field's raw value is itself a sub-document to
	// decode as the given format. Set via `format=json`. Empty means
	// "no sub-decoding."
	Format string

	// Aliases lists additional key paths that resolve to this field. Set
	// via `aliases=a;b;c`. Empty when no aliases were declared.
	Aliases []string

	// Transform names a key-spelling transform applied at lookup. Set via
	// `transform=snake|kebab|camel|upper|lower`. Empty means no transform.
	Transform string

	// Inline, on an embedded struct field, skips the field-name prefix —
	// the embedded struct's tagged fields are read at the same path level
	// as the parent.
	Inline bool

	// Required: a value MUST be supplied by some source; absent → error.
	Required bool

	// NotEmpty: the resolved value must be non-empty (set-but-empty is an
	// error).
	NotEmpty bool

	// HasDefault and DefaultValue: a default= option was supplied.
	HasDefault   bool
	DefaultValue string

	// Secret: redact in Describe / Snapshot.String / error output.
	Secret bool

	// Immutable: a reload MUST NOT change this field.
	Immutable bool

	// Expand: apply `$VAR` / `${VAR}` expansion to the resolved value.
	Expand bool

	// FromFile: the resolved value is a file path; load the file's contents
	// as the actual value.
	FromFile bool

	// Unset: clear the source value after the field is read (one-shot
	// secrets from env).
	Unset bool

	// Deprecated and DeprecationMessage: read-but-warn. Default message is
	// generated from the field name when DeprecationMessage is empty.
	Deprecated         bool
	DeprecationMessage string

	// Validate is a free-form expression for future CEL / struct-validator
	// integration. Stored verbatim for now.
	Validate string

	// Layout: time.Time parse layout. Set via `layout=2006-01-02`.
	Layout string

	// Base64 / Hex: byte-encoding decoders. Mutually exclusive.
	Base64 bool
	Hex    bool

	// Separator and KVSeparator govern string→slice / string→map splits.
	// Empty fields fall back to the decoder's defaults (",", ":").
	Separator   string
	KVSeparator string
}

// ParseTag parses a single struct-tag value (the content of a `recon:"..."`
// or fallback tag) into a [FieldTag]. The grammar tolerates unknown options —
// unrecognized tokens are silently ignored — so a future option doesn't break
// older callers' parsers. Likewise, malformed `key=value` pairs (an unclosed
// quote, a missing `=`) degrade gracefully: the token is treated as a bare
// option name.
//
// ParseTag never returns an error. A bind operation's decoder will surface
// any tag-related problem at the point where the option matters (e.g., a
// missing default for a `default=` tag without a value).
func ParseTag(s string) FieldTag {
	var ft FieldTag
	if s == "" {
		return ft
	}
	if s == "-" {
		ft.Skip = true
		return ft
	}

	parts := strings.Split(s, ",")
	ft.Name = strings.TrimSpace(parts[0])
	if ft.Name == "-" {
		// Equivalent to `tag:"-"` (skip), even with options after.
		ft.Skip = true
		return ft
	}
	for _, raw := range parts[1:] {
		opt := strings.TrimSpace(raw)
		if opt == "" {
			continue
		}
		key, value, hasValue := strings.Cut(opt, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch key {
		case "required":
			ft.Required = true
		case "notEmpty":
			ft.NotEmpty = true
		case "default":
			ft.HasDefault = true
			if hasValue {
				ft.DefaultValue = value
			}
		case "secret":
			ft.Secret = true
		case "immutable":
			ft.Immutable = true
		case "expand":
			ft.Expand = true
		case "fromFile":
			ft.FromFile = true
		case "unset":
			ft.Unset = true
		case "inline":
			ft.Inline = true
		case "base64":
			ft.Base64 = true
		case "hex":
			ft.Hex = true
		case "deprecated":
			ft.Deprecated = true
			if hasValue {
				ft.DeprecationMessage = value
			}
		case "path":
			if hasValue {
				ft.Path = value
			}
		case "source":
			if hasValue {
				ft.Source = value
			}
		case "format":
			if hasValue {
				ft.Format = value
			}
		case "transform":
			if hasValue {
				ft.Transform = value
			}
		case "layout":
			if hasValue {
				ft.Layout = value
			}
		case "validate":
			if hasValue {
				ft.Validate = value
			}
		case "separator":
			if hasValue {
				ft.Separator = value
			}
		case "kvSeparator":
			if hasValue {
				ft.KVSeparator = value
			}
		case "aliases":
			if hasValue {
				for a := range strings.SplitSeq(value, ";") {
					a = strings.TrimSpace(a)
					if a != "" {
						ft.Aliases = append(ft.Aliases, a)
					}
				}
			}
		default:
			// Unknown options are silently ignored to leave room for
			// future additions. The decoder will surface real errors at
			// resolve time.
		}
	}
	return ft
}
