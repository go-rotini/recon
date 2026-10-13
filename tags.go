package recon

import "strings"

// TagName is the default struct tag the decoder reads. Override
// per-call via [WithDecodeTag]. When the primary tag is absent or empty
// on a field, the decoder falls back through env / json / yaml / toml
// in that order so structs from those ecosystems bind without
// re-tagging. A field with none of these tags binds at its Go name in
// snake_case.
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
	// Name is the key segment, or a dotted path such as "db.dsn".
	// Empty means the Go field name, used as written (not snake_cased).
	Name string

	// Skip is true when the tag is exactly "-".
	Skip bool

	// Path overrides the inferred path with an absolute one: the
	// enclosing struct's path and any [Registry.Sub] prefix do not
	// apply. Set via `path=server.port`.
	Path string

	// Source accepts the resolved value only when the named source
	// supplied it; a value from any other source is treated as absent.
	// Set via `source=env`.
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

	// Inline binds a nested struct's fields under the parent's path,
	// without the field's own segment. Unnamed embedded structs are
	// inlined without it.
	Inline bool

	// OmitEmpty leaves a nil pointer-to-struct field nil when no key
	// exists under its path, instead of allocating it. Set via
	// `omitempty` in the primary tag; Bind ignores it in the env, json,
	// yaml and toml fallback tags, where it means something else.
	OmitEmpty bool

	// Required: an absent or null value is an error.
	Required bool

	// NotEmpty: the resolved value must be non-empty.
	NotEmpty bool

	// HasDefault, DefaultValue: a `default=` option was supplied.
	HasDefault   bool
	DefaultValue string

	// Secret marks the key secret, so [Registry.Describe],
	// [Registry.Save], [Snapshot.String] and bind errors redact it.
	Secret bool

	// Immutable records the first bound value; a later rebuild that
	// changes it is rejected with [*ImmutableChangedError].
	Immutable bool

	// Expand substitutes ${other.key} references to other registry
	// keys in the resolved value; ${key:-default} and ${key:?message}
	// are also accepted. A reference is read relative to the registry
	// being bound: through [Registry.Sub]("svc"), ${host} reads
	// svc.host.
	Expand bool

	// FromFile: the resolved value is a path; load the file contents
	// as the actual value.
	FromFile bool

	// Unset clears the key's [Registry.Set] override after a
	// successful bind. Source values are not affected.
	Unset bool

	// Deprecated and DeprecationMessage: queue a [DeprecationWarning]
	// when a source supplies the key. Set via `deprecated` or
	// `deprecated=message`.
	Deprecated         bool
	DeprecationMessage string

	// Validate holds the `validate=` option.
	//
	// Deprecated: the option has no effect; recon parses it but never
	// evaluates it. Validate bound values with a [Validator] or
	// [ValidatorContext] on the target, or a [SchemaValidator].
	Validate string

	// Layout is the time.Time parse layout, default RFC 3339. When it
	// fails, the forms [Value.AsTime] accepts are tried: RFC 3339 with
	// nanoseconds, a date-time without an offset, a bare date and a
	// time of day. Set via `layout=`.
	Layout string

	// Base64 / Hex: byte-encoding decoders. Mutually exclusive.
	Base64 bool
	Hex    bool

	// Separator / KVSeparator govern string → slice / string → map
	// splits. Empty fields fall back to "," and "=".
	Separator   string
	KVSeparator string
}

// ParseTag parses a single struct-tag value. It never fails: unknown
// options are ignored, and an option that needs a value but has none
// is ignored too.
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
		case "omitempty":
			ft.OmitEmpty = true
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
