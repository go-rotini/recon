package recon

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors. Concrete error types in this package wrap one of these so
// callers can use errors.Is for classification regardless of whether the
// concrete error is bare or aggregated inside a *MultiError.
var (
	// ErrKeyNotFound is returned when a registry lookup finds no value for
	// the requested key and no default applies.
	ErrKeyNotFound = errors.New("recon: key not found")

	// ErrTypeMismatch is returned when a Value's wire kind does not match
	// the type a caller requested via an As* accessor.
	ErrTypeMismatch = errors.New("recon: type mismatch")

	// ErrMissingRequired is returned when a key marked required has no value
	// supplied by any source and no default.
	ErrMissingRequired = errors.New("recon: missing required value")

	// ErrEmptyValue is returned when a key marked notEmpty resolved to the
	// empty string.
	ErrEmptyValue = errors.New("recon: empty value")

	// ErrUnknownKey is returned in strict mode when a source supplies a key
	// the bind target does not declare.
	ErrUnknownKey = errors.New("recon: unknown key (strict mode)")

	// ErrImmutableChanged is returned when a reload would change a key
	// tagged immutable.
	ErrImmutableChanged = errors.New("recon: immutable key changed")

	// ErrCoercion is returned when a wire value cannot be converted to the
	// requested Go type.
	ErrCoercion = errors.New("recon: coercion failed")

	// ErrReadOnlySource is returned when a write is attempted against a
	// source that cannot accept it.
	ErrReadOnlySource = errors.New("recon: source is read-only")

	// ErrAliasCycle is returned when RegisterAlias would create a cycle.
	ErrAliasCycle = errors.New("recon: alias cycle")

	// ErrInvalidPath is returned when a path argument is malformed (e.g.,
	// nil receiver, illegal escape).
	ErrInvalidPath = errors.New("recon: invalid path")

	// ErrUnsupportedFormat is returned when no registered codec matches the
	// requested format name or file extension.
	ErrUnsupportedFormat = errors.New("recon: unsupported format")

	// ErrValidation is returned when a SchemaValidator reports failure.
	ErrValidation = errors.New("recon: validation failed")

	// ErrSourceConflict is returned when AddSource / RegisterAlias would
	// introduce a duplicate name.
	ErrSourceConflict = errors.New("recon: source name conflict")

	// ErrRegistryClosed is returned by operations on a registry whose
	// Close() has been called.
	ErrRegistryClosed = errors.New("recon: registry closed")

	// ErrNilContext is returned when a context-taking call receives nil.
	ErrNilContext = errors.New("recon: nil context")

	// ErrSchemaInvalid is returned when a supplied schema fails to compile.
	ErrSchemaInvalid = errors.New("recon: schema invalid")
)

// Position is a source-local position used by ParseError. Line and Column are
// 1-indexed for human display; both zero means the position is unknown.
type Position struct {
	Line   int
	Column int
}

// String formats the position as "line:col" or returns "" when the position
// is the zero value.
func (p Position) String() string {
	if p.Line == 0 && p.Column == 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// MultiError aggregates per-field / per-key errors from a single Load or Bind
// invocation. Implements the errors.Unwrap() []error contract introduced in
// Go 1.20 so errors.Is and errors.As traverse every contained error.
type MultiError struct {
	Errors []error
}

// Error returns a newline-separated list of the contained error messages.
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

// Unwrap returns every contained error so errors.Is / errors.As recurse.
func (m *MultiError) Unwrap() []error { return m.Errors }

// Append adds err to the MultiError. nil is a no-op.
func (m *MultiError) Append(err error) {
	if err == nil {
		return
	}
	m.Errors = append(m.Errors, err)
}

// MissingRequiredError reports that a required key was not supplied by any
// source.
type MissingRequiredError struct {
	Path    Path
	Sources []string // names of sources consulted, in precedence order
}

func (e *MissingRequiredError) Error() string {
	if len(e.Sources) == 0 {
		return fmt.Sprintf("recon: missing required value for %s", e.Path)
	}
	return fmt.Sprintf("recon: missing required value for %s (sources consulted: %s)", e.Path, strings.Join(e.Sources, ", "))
}

// Is matches errors.Is against ErrMissingRequired and against another
// *MissingRequiredError for the same path. Direct comparison against the
// sentinel (rather than errors.Is) avoids the false positive where a peer
// *MissingRequiredError would otherwise short-circuit the match before the
// path-equality check runs.
func (e *MissingRequiredError) Is(target error) bool {
	if target == ErrMissingRequired {
		return true
	}
	var other *MissingRequiredError
	if errors.As(target, &other) {
		return e.Path.Equal(other.Path)
	}
	return false
}

// EmptyValueError reports that a key marked notEmpty resolved to the empty
// string.
type EmptyValueError struct {
	Path   Path
	Source string
}

func (e *EmptyValueError) Error() string {
	return fmt.Sprintf("recon: empty value for %s (from source %q)", e.Path, e.Source)
}

func (e *EmptyValueError) Is(target error) bool {
	if target == ErrEmptyValue {
		return true
	}
	var other *EmptyValueError
	if errors.As(target, &other) {
		return e.Path.Equal(other.Path) && e.Source == other.Source
	}
	return false
}

// CoercionError reports that a wire value could not be converted to the
// target Go type.
type CoercionError struct {
	Path     Path
	Source   string
	WireType string
	Target   string
	Cause    error
}

func (e *CoercionError) Error() string {
	base := fmt.Sprintf("recon: coerce %s: %s → %s", e.Path, e.WireType, e.Target)
	if e.Source != "" {
		base += fmt.Sprintf(" (source %q)", e.Source)
	}
	if e.Cause != nil {
		base += ": " + e.Cause.Error()
	}
	return base
}

func (e *CoercionError) Unwrap() error { return e.Cause }

func (e *CoercionError) Is(target error) bool {
	return errors.Is(target, ErrCoercion)
}

// UnknownKeyError reports that strict-mode decoding rejected an extra key.
type UnknownKeyError struct {
	Path   Path
	Source string
}

func (e *UnknownKeyError) Error() string {
	return fmt.Sprintf("recon: unknown key %s (source %q, strict mode)", e.Path, e.Source)
}

func (e *UnknownKeyError) Is(target error) bool {
	return errors.Is(target, ErrUnknownKey)
}

// ImmutableChangedError reports that a reload tried to change a key tagged
// immutable. Old and New are pre-redacted by the registry when the key is
// also tagged secret.
type ImmutableChangedError struct {
	Path Path
	Old  string
	New  string
}

func (e *ImmutableChangedError) Error() string {
	return fmt.Sprintf("recon: immutable key %s changed: %q → %q", e.Path, e.Old, e.New)
}

func (e *ImmutableChangedError) Is(target error) bool {
	if target == ErrImmutableChanged {
		return true
	}
	var other *ImmutableChangedError
	if errors.As(target, &other) {
		return e.Path.Equal(other.Path)
	}
	return false
}

// SourceError reports that a source failed to read, watch, or refresh.
type SourceError struct {
	Source string
	Op     string // "get" / "watch" / "refresh" / "close"
	Cause  error
}

func (e *SourceError) Error() string {
	return fmt.Sprintf("recon: source %q %s: %v", e.Source, e.Op, e.Cause)
}

func (e *SourceError) Unwrap() error { return e.Cause }

// AliasCycleError reports that RegisterAlias would create a cycle. Chain lists
// the offending alias chain in order.
type AliasCycleError struct {
	Chain []Path
}

func (e *AliasCycleError) Error() string {
	parts := make([]string, len(e.Chain))
	for i, p := range e.Chain {
		parts[i] = p.String()
	}
	return fmt.Sprintf("recon: alias cycle: %s", strings.Join(parts, " → "))
}

func (e *AliasCycleError) Is(target error) bool {
	return errors.Is(target, ErrAliasCycle)
}

// ValidationError reports that a SchemaValidator rejected a key.
type ValidationError struct {
	Path Path
	Rule string
	Msg  string
}

func (e *ValidationError) Error() string {
	if e.Rule != "" {
		return fmt.Sprintf("recon: validation %s [%s]: %s", e.Path, e.Rule, e.Msg)
	}
	return fmt.Sprintf("recon: validation %s: %s", e.Path, e.Msg)
}

func (e *ValidationError) Is(target error) bool {
	return errors.Is(target, ErrValidation)
}

// ParseError reports that a source's underlying format parser failed.
type ParseError struct {
	Source   string
	Path     string // file path, for file sources; empty otherwise
	Position Position
	Cause    error
}

func (e *ParseError) Error() string {
	loc := e.Source
	if e.Path != "" {
		loc = e.Path
	}
	if pos := e.Position.String(); pos != "" {
		loc += ":" + pos
	}
	if loc == "" {
		return fmt.Sprintf("recon: parse: %v", e.Cause)
	}
	return fmt.Sprintf("recon: parse %s: %v", loc, e.Cause)
}

func (e *ParseError) Unwrap() error { return e.Cause }

// DeprecationWarning is delivered (on Event.Warnings, not Event.Err) when a
// value is read from a key tagged `deprecated`. It is non-fatal: the registry
// emits it through the event channel and the consuming code (e.g., rotini's
// OnProgramWarning hook) decides how to surface it. The Replacement field is
// empty unless the `deprecated=` tag option supplied a suggested replacement.
type DeprecationWarning struct {
	Path        Path
	Source      string
	Replacement Path
	Message     string
}

func (w DeprecationWarning) String() string {
	out := fmt.Sprintf("recon: %s is deprecated", w.Path)
	if w.Source != "" {
		out += fmt.Sprintf(" (source %q)", w.Source)
	}
	if len(w.Replacement) > 0 {
		out += fmt.Sprintf("; use %s instead", w.Replacement)
	}
	if w.Message != "" {
		out += ": " + w.Message
	}
	return out
}
