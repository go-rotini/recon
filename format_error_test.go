package recon

import (
	"errors"
	"strings"
	"testing"
)

func TestFormatError_Nil(t *testing.T) {
	if got := FormatError(nil, nil); got != "" {
		t.Fatalf("FormatError(nil)=%q, want empty", got)
	}
}

func TestFormatError_SingleMissing(t *testing.T) {
	err := &MissingRequiredError{Path: MakePath("db", "dsn")}
	got := FormatError(nil, err)
	if !strings.Contains(got, "db.dsn") {
		t.Fatalf("got=%q, want path mention", got)
	}
	if !strings.Contains(got, "missing required") {
		t.Fatalf("got=%q, want reason", got)
	}
}

func TestFormatError_MultiError(t *testing.T) {
	multi := &MultiError{}
	multi.Append(&MissingRequiredError{Path: MakePath("a")})
	multi.Append(&EmptyValueError{Path: MakePath("b"), Source: "env"})
	multi.Append(&CoercionError{
		Path: MakePath("c"), WireType: "string", Target: "int",
		Cause: errors.New("invalid syntax"),
	})

	got := FormatError(nil, multi)
	if !strings.Contains(got, "3 errors") {
		t.Fatalf("missing count header: %q", got)
	}
	for _, want := range []string{"a", "b", "c", "missing required", "empty value", "coerce string", "invalid syntax"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, got)
		}
	}
	// Source annotation must include the source name.
	if !strings.Contains(got, `"env"`) {
		t.Fatalf("output missing source annotation: %s", got)
	}
}

func TestFormatError_RegistryProvenance(t *testing.T) {
	high := NewMapSource("high", map[string]any{"k": "from-high"})
	low := NewMapSource("low", map[string]any{"k": "from-low"})
	r := newRegistry(t, WithSources(high, low))
	err := &ValidationError{Path: MakePath("k"), Rule: "enum", Msg: "not allowed"}
	got := FormatError(r, err)
	if !strings.Contains(got, "chain") {
		t.Fatalf("missing provenance chain: %s", got)
	}
	if !strings.Contains(got, `"high"`) || !strings.Contains(got, `"low"`) {
		t.Fatalf("source names missing from chain: %s", got)
	}
}

func TestFormatError_PlainError(t *testing.T) {
	err := errors.New("something went wrong")
	got := FormatError(nil, err)
	if !strings.Contains(got, "something went wrong") {
		t.Fatalf("got=%q", got)
	}
}

func TestFormatError_ImmutableChanged(t *testing.T) {
	err := &ImmutableChangedError{Path: MakePath("tier"), Old: "prod", New: "staging"}
	got := FormatError(nil, err)
	if !strings.Contains(got, "tier") || !strings.Contains(got, "prod") || !strings.Contains(got, "staging") {
		t.Fatalf("got=%q", got)
	}
}
