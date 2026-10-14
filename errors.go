package recon

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Sentinel errors. Concrete error types in this package wrap one of
// these so callers can use errors.Is for classification.
var (
	// ErrKeyNotFound is returned when a lookup finds no value and no
	// default applies.
	ErrKeyNotFound = errors.New("recon: key not found")

	// ErrTypeMismatch is returned when a [Value]'s wire kind does not
	// match the type a caller requested via an As* accessor.
	ErrTypeMismatch = errors.New("recon: type mismatch")

	// ErrMissingRequired is returned when a key tagged required has no
	// value supplied by any source and no default.
	ErrMissingRequired = errors.New("recon: missing required value")

	// ErrEmptyValue is returned when a key tagged notEmpty resolved to
	// the empty string.
	ErrEmptyValue = errors.New("recon: empty value")

	// ErrUnknownKey is returned in strict mode when a source supplies
	// a key the bind target does not declare.
	ErrUnknownKey = errors.New("recon: unknown key (strict mode)")

	// ErrImmutableChanged is returned when a reload would change a
	// key tagged immutable.
	ErrImmutableChanged = errors.New("recon: immutable key changed")

	// ErrCoercion is returned when a wire value cannot be converted
	// to the requested Go type.
	ErrCoercion = errors.New("recon: coercion failed")

	// ErrReadOnlySource reports a write attempted against a read-only
	// source.
	//
	// Deprecated: recon never writes to a source, so nothing in recon
	// returns it. A custom [Source] with its own write path should
	// define its own error.
	ErrReadOnlySource = errors.New("recon: source is read-only")

	// ErrAliasCycle is returned when [Registry.RegisterAlias] would
	// create a cycle.
	ErrAliasCycle = errors.New("recon: alias cycle")

	// ErrInvalidPath is returned when a path argument is malformed.
	ErrInvalidPath = errors.New("recon: invalid path")

	// ErrUnsupportedFormat is returned when no registered codec
	// matches a requested format or file extension.
	ErrUnsupportedFormat = errors.New("recon: unsupported format")

	// ErrValidation is returned when a [SchemaValidator] reports
	// failure.
	ErrValidation = errors.New("recon: validation failed")

	// ErrSourceConflict is returned when a source or [Configs] entry
	// would get an empty, duplicate, or reserved name.
	ErrSourceConflict = errors.New("recon: source name conflict")

	// ErrRegistryClosed is returned by operations on a Closed
	// registry.
	ErrRegistryClosed = errors.New("recon: registry closed")

	// ErrNilContext is returned when a context-taking call receives
	// nil.
	ErrNilContext = errors.New("recon: nil context")

	// ErrSchemaInvalid is returned when a supplied schema fails to
	// compile.
	ErrSchemaInvalid = errors.New("recon: schema invalid")

	// ErrFileTooLarge is returned when a file read by a [FileSource] or
	// a `fromFile` field exceeds [MaxFileSize].
	ErrFileTooLarge = errors.New("recon: file size exceeds limit")

	// ErrNotWatched is returned by [NewLive] when the registry cannot
	// deliver reload events: it was built [WithoutWatch], or a file
	// source has no [WatcherFactory].
	ErrNotWatched = errors.New("recon: registry is not watched")
)

// Position is a source-local position used by [ParseError]. Line and
// Column are 1-indexed; both zero means unknown. Column is taken from
// the codec's error: it counts bytes for YAML, JSON, JSONC and dotenv,
// and characters for TOML; an error that knows only its line has
// Column 0.
type Position struct {
	Line   int
	Column int
}

// String formats the position as "line:col", as "line" when the column
// is unknown, or returns "" for the zero value.
func (p Position) String() string {
	if p.Line == 0 && p.Column == 0 {
		return ""
	}
	if p.Column == 0 {
		return strconv.Itoa(p.Line)
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// MultiError aggregates the errors of one operation, such as a
// [Registry.Bind] or a snapshot rebuild. Its Unwrap() []error method
// lets errors.Is and errors.As see every contained error.
type MultiError struct {
	Errors []error
}

// Error returns the single error's text, or a count followed by one
// line per error.
func (m *MultiError) Error() string {
	switch len(m.Errors) {
	case 0:
		return "recon: empty multi-error"
	case 1:
		return m.Errors[0].Error()
	default:
		var b strings.Builder
		b.WriteString("recon: ")
		fmt.Fprintf(&b, "%d errors:\n", len(m.Errors))
		for i, e := range m.Errors {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("  - ")
			b.WriteString(e.Error())
		}
		return b.String()
	}
}

// Unwrap returns the contained errors.
func (m *MultiError) Unwrap() []error { return m.Errors }

// Append adds err to the MultiError. Nil is a no-op.
func (m *MultiError) Append(err error) {
	if err == nil {
		return
	}
	m.Errors = append(m.Errors, err)
}

// MissingRequiredError reports that a required key was not supplied
// by any source.
type MissingRequiredError struct {
	Path    Path
	Sources []string // names of sources consulted, in precedence order
}

// Error describes the missing key.
func (e *MissingRequiredError) Error() string {
	if len(e.Sources) == 0 {
		return fmt.Sprintf("recon: missing required value for %s", e.Path)
	}
	return fmt.Sprintf("recon: missing required value for %s (sources consulted: %s)", e.Path, strings.Join(e.Sources, ", "))
}

// Is matches [ErrMissingRequired] and any *MissingRequiredError with
// the same Path.
func (e *MissingRequiredError) Is(target error) bool {
	if target == ErrMissingRequired {
		return true
	}
	if other, ok := errors.AsType[*MissingRequiredError](target); ok {
		return e.Path.Equal(other.Path)
	}
	return false
}

// EmptyValueError reports that a notEmpty key resolved to "".
type EmptyValueError struct {
	Path   Path
	Source string
}

// Error describes the empty key and its source.
func (e *EmptyValueError) Error() string {
	return fmt.Sprintf("recon: empty value for %s (from source %q)", e.Path, e.Source)
}

// Is matches [ErrEmptyValue] and any *EmptyValueError with the same
// Path and Source.
func (e *EmptyValueError) Is(target error) bool {
	if target == ErrEmptyValue {
		return true
	}
	if other, ok := errors.AsType[*EmptyValueError](target); ok {
		return e.Path.Equal(other.Path) && e.Source == other.Source
	}
	return false
}

// CoercionError reports that a wire value could not be converted to
// the target Go type. When Secret is true, Error prints "[redacted]"
// in place of Cause's text.
type CoercionError struct {
	Path     Path
	Source   string
	WireType string
	Target   string
	Cause    error
	Secret   bool
}

// Error describes the failed conversion.
func (e *CoercionError) Error() string {
	base := fmt.Sprintf("recon: coerce %s: %s → %s", e.Path, e.WireType, e.Target)
	if e.Source != "" {
		base += fmt.Sprintf(" (source %q)", e.Source)
	}
	if e.Cause != nil {
		if e.Secret {
			base += ": [redacted]"
		} else {
			base += ": " + e.Cause.Error()
		}
	}
	return base
}

// Unwrap returns Cause.
func (e *CoercionError) Unwrap() error { return e.Cause }

// Is matches [ErrCoercion].
func (e *CoercionError) Is(target error) bool {
	return errors.Is(target, ErrCoercion)
}

// UnknownKeyError reports a key that no field of a strict
// [Registry.Bind] target consumed.
type UnknownKeyError struct {
	Path   Path
	Source string
}

// Error describes the unknown key and its source.
func (e *UnknownKeyError) Error() string {
	return fmt.Sprintf("recon: unknown key %s (source %q, strict mode)", e.Path, e.Source)
}

// Is matches [ErrUnknownKey].
func (e *UnknownKeyError) Is(target error) bool {
	return errors.Is(target, ErrUnknownKey)
}

// ImmutableChangedError reports that a reload would change a key
// tagged immutable. Old and New are pre-redacted when the key is
// also tagged secret.
type ImmutableChangedError struct {
	Path Path
	Old  string
	New  string
}

// Error describes the rejected change.
func (e *ImmutableChangedError) Error() string {
	return fmt.Sprintf("recon: immutable key %s changed: %q → %q", e.Path, e.Old, e.New)
}

// Is matches [ErrImmutableChanged] and any *ImmutableChangedError with
// the same Path.
func (e *ImmutableChangedError) Is(target error) bool {
	if target == ErrImmutableChanged {
		return true
	}
	if other, ok := errors.AsType[*ImmutableChangedError](target); ok {
		return e.Path.Equal(other.Path)
	}
	return false
}

// SourceError reports that an operation on a named source failed.
type SourceError struct {
	Source string
	Op     string // "list" / "get" (remote reads), "close", or "pin"
	Cause  error
}

// Error describes the failed operation.
func (e *SourceError) Error() string {
	return fmt.Sprintf("recon: source %q %s: %v", e.Source, e.Op, e.Cause)
}

// Unwrap returns Cause.
func (e *SourceError) Unwrap() error { return e.Cause }

// AliasCycleError reports that [Registry.RegisterAlias] would create
// a cycle. Chain lists the offending alias chain in walk order.
type AliasCycleError struct {
	Chain []Path
}

// Error lists the alias chain.
func (e *AliasCycleError) Error() string {
	parts := make([]string, len(e.Chain))
	for i, p := range e.Chain {
		parts[i] = p.String()
	}
	return fmt.Sprintf("recon: alias cycle: %s", strings.Join(parts, " → "))
}

// Is matches [ErrAliasCycle].
func (e *AliasCycleError) Is(target error) bool {
	return errors.Is(target, ErrAliasCycle)
}

// ValidationError reports that a [SchemaValidator] rejected a key.
// When Secret is true, Error prints "[redacted]" in place of Msg.
type ValidationError struct {
	Path   Path
	Rule   string
	Msg    string
	Secret bool
	// Cause is the validator's own error, when it has one: a
	// *jsonschema.ValidationError from [JSONSchemaValidator], with its
	// nested causes and keyword locations. Unwrap returns it, except
	// when Secret is true, since it may quote the value.
	Cause error
}

// Unwrap returns Cause, or nil when Secret is true.
func (e *ValidationError) Unwrap() error {
	if e.Secret {
		return nil
	}
	return e.Cause
}

// Error describes the failed rule.
func (e *ValidationError) Error() string {
	msg := e.Msg
	if e.Secret {
		msg = "[redacted]"
	}
	if e.Rule != "" {
		return fmt.Sprintf("recon: validation %s [%s]: %s", e.Path, e.Rule, msg)
	}
	return fmt.Sprintf("recon: validation %s: %s", e.Path, msg)
}

// Is matches [ErrValidation].
func (e *ValidationError) Is(target error) bool {
	return errors.Is(target, ErrValidation)
}

// ParseError reports that a source's underlying format parser failed.
// Sources built from a bundled codec fill Position when the parser
// reports one (see [ParsePosition]) and Msg with the parser's own
// description, so callers can render file:line:col without inspecting
// each format's error type.
type ParseError struct {
	Source   string
	Path     string // file path for file sources; empty otherwise
	Position Position
	Cause    error
	// Msg is the parser's description of the problem without the
	// format's position prefix; empty means use Cause.
	Msg string
}

// Error renders "recon: parse path:line:col: msg", omitting the parts
// that are unknown.
func (e *ParseError) Error() string {
	loc := e.location()
	if loc == "" {
		return "recon: parse: " + e.detail()
	}
	return fmt.Sprintf("recon: parse %s: %s", loc, e.detail())
}

// location renders "path:line:col", falling back to the source name.
func (e *ParseError) location() string {
	loc := e.Source
	if e.Path != "" {
		loc = e.Path
	}
	if pos := e.Position.String(); pos != "" {
		loc += ":" + pos
	}
	return loc
}

// detail returns Msg, or the cause's text when Msg is empty.
func (e *ParseError) detail() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprint(e.Cause)
}

// Unwrap returns Cause.
func (e *ParseError) Unwrap() error { return e.Cause }

// DeprecationWarning is a non-fatal notice that a source supplied a
// `deprecated`-tagged key during a bind. Message is the `deprecated=`
// tag value, or a generic message when the tag has none. The tag has
// no replacement form, so Replacement is left for callers that build
// their own warnings. Delivered on [Event.Warnings] and via
// [Registry.DrainWarnings]. The watch engine and [Live] also use it,
// with an empty Path, to report events dropped because a channel was
// full.
type DeprecationWarning struct {
	Path        Path
	Source      string
	Replacement Path
	Message     string
}

// String renders the warning on one line. A warning with an empty Path
// renders as its Message.
func (w DeprecationWarning) String() string {
	if len(w.Path) == 0 {
		return w.Message
	}
	out := fmt.Sprintf("recon: %s is deprecated", w.Path)
	if w.Source != "" {
		out += fmt.Sprintf(" (source %q)", w.Source)
	}
	if len(w.Replacement) > 0 {
		out += fmt.Sprintf("; use %s instead", w.Replacement)
	}
	if w.Message != "" && w.Message != genericDeprecationMessage(w.Path) {
		out += ": " + w.Message
	}
	return out
}

// genericDeprecationMessage is the Message of a warning whose
// `deprecated` tag carries no text.
func genericDeprecationMessage(p Path) string {
	return "recon: key " + p.String() + " is deprecated"
}

// schemaValidationError is a validator's error err as the registry
// reports it: under a "recon: schema validation: " prefix, with err's
// own "recon: " prefix dropped so it is not repeated.
func schemaValidationError(err error) error {
	return &schemaError{err: err}
}

// schemaError is the error [schemaValidationError] returns.
type schemaError struct{ err error }

func (e *schemaError) Error() string {
	return "recon: schema validation: " + strings.TrimPrefix(e.err.Error(), "recon: ")
}

func (e *schemaError) Unwrap() error { return e.err }
