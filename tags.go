package recon

import "strings"

// TagName is the default struct tag the decoder reads. Override
// per-call via [WithDecodeTag]. When the primary tag is absent on a
// field, the decoder falls back through env / json / yaml / toml in
// that order so structs from those ecosystems bind without
// re-tagging.
const TagName = "recon"

// fallbackTagNames is the in-order fallback chain consulted when the
// primary tag is empty.
var fallbackTagNames = [...]string{"env", "json", "yaml", "toml"}

// FieldTag is the parsed form of a single struct-tag value. The
// grammar follows encoding/json:
//
//	recon:"name,opt1,opt2=value,opt3"
//
// An empty Name means "use the Go field name".
type FieldTag struct {
	// Name is the canonical key; empty means "use the field name."
	Name string

	// Skip is true when the tag is exactly "-".
	Skip bool

	// Path overrides the inferred path. Set via `path=server.port`.
	Path string

	// Source pins this field to a specific source by name. Set via
	// `source=env`.
	Source string

	// Format hints that the field's raw value is a sub-document to
	// decode. Set via `format=json`.
	Format string

	// Aliases lists additional paths that resolve to this field. Set
	// via `aliases=a;b;c`.
	Aliases []string

	// Transform names a key-spelling transform: snake / kebab / camel
	// / upper / lower. Set via `transform=`.
	Transform string

	// Inline, on an embedded struct, flattens the field-name prefix
	// out of the path.
	Inline bool

	// Required: an absent value is an error.
	Required bool

	// NotEmpty: the resolved value must be non-empty.
	NotEmpty bool

	// HasDefault, DefaultValue: a `default=` option was supplied.
	HasDefault   bool
	DefaultValue string

	// Secret redacts in [Describe] / [Snapshot.String] / errors.
	Secret bool

	// Immutable: a reload must not change this field.
	Immutable bool

	// Expand applies ${VAR} expansion to the resolved value.
	Expand bool

	// FromFile: the resolved value is a path; load the file contents
	// as the actual value.
	FromFile bool

	// Unset clears the source value after the field is read.
	Unset bool

	// Deprecated and DeprecationMessage: emit a [DeprecationWarning]
	// on read.
	Deprecated         bool
	DeprecationMessage string

	// Validate carries a free-form expression for future
	// CEL / struct-validator integration.
	Validate string

	// Layout is the time.Time parse layout. Set via `layout=`.
	Layout string

	// Base64 / Hex: byte-encoding decoders. Mutually exclusive.
	Base64 bool
	Hex    bool

	// Separator / KVSeparator govern string → slice / string → map
	// splits. Empty fields fall back to "," and "=".
	Separator   string
	KVSeparator string
}

// ParseTag parses a single struct-tag value. Unknown option tokens
// are silently ignored to leave room for future additions. Malformed
// key=value pairs degrade to bare option names.
//
// ParseTag never returns an error; tag-related problems surface at
// the point where the option matters.
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
		}
	}
	return ft
}
