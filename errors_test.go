package recon

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPosition_String(t *testing.T) {
	if got := (Position{}).String(); got != "" {
		t.Errorf("zero Position.String() = %q, want empty", got)
	}
	if got := (Position{Line: 10, Column: 4}).String(); got != "10:4" {
		t.Errorf("Position.String() = %q, want 10:4", got)
	}
}

func TestMultiError_Empty(t *testing.T) {
	m := &MultiError{}
	if got := m.Error(); !strings.Contains(got, "empty") {
		t.Errorf("empty MultiError.Error() = %q", got)
	}
	if m.Unwrap() != nil {
		t.Errorf("empty Unwrap() returned non-nil")
	}
}

func TestMultiError_Single(t *testing.T) {
	m := &MultiError{}
	m.Append(errors.New("boom"))
	if got := m.Error(); got != "boom" {
		t.Errorf("single MultiError.Error() = %q", got)
	}
}

func TestMultiError_Multi(t *testing.T) {
	m := &MultiError{}
	m.Append(errors.New("a"))
	m.Append(errors.New("b"))
	out := m.Error()
	if !strings.Contains(out, "2 errors") || !strings.Contains(out, "a") || !strings.Contains(out, "b") {
		t.Errorf("multi Error() = %q", out)
	}
	if len(m.Unwrap()) != 2 {
		t.Errorf("Unwrap returned %d, want 2", len(m.Unwrap()))
	}
	// errors.Is traverses the slice.
	m.Append(fmt.Errorf("wrap: %w", ErrEmptyValue))
	if !errors.Is(m, ErrEmptyValue) {
		t.Errorf("errors.Is should walk MultiError.Unwrap")
	}
}

func TestMultiError_AppendNil(t *testing.T) {
	m := &MultiError{}
	m.Append(nil)
	if len(m.Errors) != 0 {
		t.Errorf("Append(nil) added an entry")
	}
}

func TestMissingRequiredError(t *testing.T) {
	p := Path{"server", "port"}
	e := &MissingRequiredError{Path: p}
	if !errors.Is(e, ErrMissingRequired) {
		t.Error("MissingRequiredError should match ErrMissingRequired")
	}
	// Same path matches via Is.
	other := &MissingRequiredError{Path: p}
	if !errors.Is(e, other) {
		t.Error("same-path MissingRequiredError instances should match")
	}
	if !strings.Contains(e.Error(), "server.port") {
		t.Errorf("error message missing path: %q", e.Error())
	}
	// With sources listed.
	e2 := &MissingRequiredError{Path: p, Sources: []string{"env", "config"}}
	if !strings.Contains(e2.Error(), "env, config") {
		t.Errorf("sources missing from message: %q", e2.Error())
	}
}

func TestEmptyValueError(t *testing.T) {
	e := &EmptyValueError{Path: Path{"x"}, Source: "env"}
	if !errors.Is(e, ErrEmptyValue) {
		t.Error("EmptyValueError should match ErrEmptyValue")
	}
	other := &EmptyValueError{Path: Path{"x"}, Source: "env"}
	if !errors.Is(e, other) {
		t.Error("same path+source should match")
	}
	diff := &EmptyValueError{Path: Path{"y"}, Source: "env"}
	if errors.Is(e, diff) {
		t.Error("different path should not match")
	}
	// Error() must mention the path and the source so logs include
	// both.
	msg := e.Error()
	if !strings.Contains(msg, "x") || !strings.Contains(msg, "env") {
		t.Errorf("Error() = %q, want path and source in the message", msg)
	}
}

func TestCoercionError(t *testing.T) {
	cause := errors.New("parse fail")
	e := &CoercionError{
		Path:     Path{"port"},
		Source:   "env",
		WireType: "string",
		Target:   "int",
		Cause:    cause,
	}
	if !errors.Is(e, ErrCoercion) {
		t.Error("CoercionError should match ErrCoercion")
	}
	if !errors.Is(e, cause) {
		t.Error("CoercionError should unwrap to its Cause")
	}
	msg := e.Error()
	for _, want := range []string{"port", "string", "int", "env", "parse fail"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q missing %q", msg, want)
		}
	}
	// Without source / cause.
	e2 := &CoercionError{Path: Path{"a"}, WireType: "string", Target: "int"}
	if got := e2.Error(); strings.Contains(got, "source") {
		t.Errorf("Error() should not mention source when empty: %q", got)
	}
}

func TestUnknownKeyError(t *testing.T) {
	e := &UnknownKeyError{Path: Path{"x"}, Source: "config"}
	if !errors.Is(e, ErrUnknownKey) {
		t.Error("should match ErrUnknownKey")
	}
	if !strings.Contains(e.Error(), "x") {
		t.Errorf("error message missing path: %q", e.Error())
	}
}

func TestImmutableChangedError(t *testing.T) {
	e := &ImmutableChangedError{Path: Path{"tier"}, Old: "prod", New: "dev"}
	if !errors.Is(e, ErrImmutableChanged) {
		t.Error("should match ErrImmutableChanged")
	}
	other := &ImmutableChangedError{Path: Path{"tier"}}
	if !errors.Is(e, other) {
		t.Error("same-path ImmutableChangedError should match")
	}
	if !strings.Contains(e.Error(), "prod") || !strings.Contains(e.Error(), "dev") {
		t.Errorf("error message missing old/new: %q", e.Error())
	}
}

func TestSourceError(t *testing.T) {
	cause := errors.New("disk full")
	e := &SourceError{Source: "file", Op: "refresh", Cause: cause}
	if !errors.Is(e, cause) {
		t.Error("SourceError should unwrap to its Cause")
	}
	if !strings.Contains(e.Error(), "file") || !strings.Contains(e.Error(), "refresh") {
		t.Errorf("error message incomplete: %q", e.Error())
	}
}

func TestAliasCycleError(t *testing.T) {
	e := &AliasCycleError{Chain: []Path{{"a"}, {"b"}, {"a"}}}
	if !errors.Is(e, ErrAliasCycle) {
		t.Error("should match ErrAliasCycle")
	}
	msg := e.Error()
	if !strings.Contains(msg, "a") || !strings.Contains(msg, "b") {
		t.Errorf("error message should list the chain: %q", msg)
	}
}

func TestValidationError(t *testing.T) {
	e := &ValidationError{Path: Path{"port"}, Rule: "minimum", Msg: "below 1024"}
	if !errors.Is(e, ErrValidation) {
		t.Error("should match ErrValidation")
	}
	for _, want := range []string{"port", "minimum", "below 1024"} {
		if !strings.Contains(e.Error(), want) {
			t.Errorf("Error() = %q missing %q", e.Error(), want)
		}
	}
	// Without rule.
	e2 := &ValidationError{Path: Path{"port"}, Msg: "bad"}
	if strings.Contains(e2.Error(), "[]") {
		t.Errorf("empty rule should not print brackets: %q", e2.Error())
	}
}

func TestParseError(t *testing.T) {
	cause := errors.New("syntax")
	e := &ParseError{Source: "config.yaml", Position: Position{Line: 3, Column: 7}, Cause: cause}
	if !errors.Is(e, cause) {
		t.Error("ParseError should unwrap to its Cause")
	}
	msg := e.Error()
	if !strings.Contains(msg, "config.yaml") || !strings.Contains(msg, "3:7") {
		t.Errorf("Error() = %q", msg)
	}
	// With explicit file path.
	e2 := &ParseError{Source: "x", Path: "/tmp/c.yaml", Cause: cause}
	if !strings.Contains(e2.Error(), "/tmp/c.yaml") {
		t.Errorf("path missing: %q", e2.Error())
	}
	// Bare (no source, no path).
	e3 := &ParseError{Cause: cause}
	if !strings.Contains(e3.Error(), "syntax") {
		t.Errorf("bare ParseError: %q", e3.Error())
	}
}

func TestDeprecationWarning_String(t *testing.T) {
	w := DeprecationWarning{Path: Path{"old"}, Source: "env"}
	if got := w.String(); !strings.Contains(got, "old") || !strings.Contains(got, "env") {
		t.Errorf("DeprecationWarning: %q", got)
	}
	w2 := DeprecationWarning{Path: Path{"old"}, Replacement: Path{"new"}, Message: "use --new"}
	got := w2.String()
	for _, want := range []string{"old", "new", "use --new"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning %q missing %q", got, want)
		}
	}
}
