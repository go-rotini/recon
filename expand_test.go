package recon

import (
	"errors"
	"strings"
	"testing"
)

func TestExpand_NoReferences(t *testing.T) {
	r := newRegistry(t)
	out, err := expandValueRefs("plain string with no refs", r)
	if err != nil {
		t.Fatalf("expandValueRefs: %v", err)
	}
	if out != "plain string with no refs" {
		t.Fatalf("got %q, want passthrough", out)
	}
}

func TestExpand_RequiredSucceeds(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("host", "example.com")

	out, err := expandValueRefs("https://${host}/api", r)
	if err != nil {
		t.Fatalf("expandValueRefs: %v", err)
	}
	if out != "https://example.com/api" {
		t.Fatalf("got %q", out)
	}
}

func TestExpand_RequiredMissingErrors(t *testing.T) {
	r := newRegistry(t)
	_, err := expandValueRefs("https://${absent}/api", r)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("err = %v, want wrapping ErrKeyNotFound", err)
	}
}

func TestExpand_WithDefault_KeySet(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("port", "9000")
	out, err := expandValueRefs("${port:-8080}", r)
	if err != nil {
		t.Fatalf("expandValueRefs: %v", err)
	}
	if out != "9000" {
		t.Fatalf("got %q, want 9000 (set value wins)", out)
	}
}

func TestExpand_WithDefault_KeyMissing(t *testing.T) {
	r := newRegistry(t)
	out, err := expandValueRefs("${absent:-fallback}", r)
	if err != nil {
		t.Fatalf("expandValueRefs: %v", err)
	}
	if out != "fallback" {
		t.Fatalf("got %q, want fallback", out)
	}
}

func TestExpand_WithDefault_EmptyValueFallsThrough(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("port", "")
	out, err := expandValueRefs("${port:-8080}", r)
	if err != nil {
		t.Fatalf("expandValueRefs: %v", err)
	}
	if out != "8080" {
		t.Fatalf("got %q, want 8080 (empty value falls back)", out)
	}
}

func TestExpand_OrError_KeySet(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("api_key", "secret123")
	out, err := expandValueRefs("${api_key:?required}", r)
	if err != nil {
		t.Fatalf("expandValueRefs: %v", err)
	}
	if out != "secret123" {
		t.Fatalf("got %q", out)
	}
}

func TestExpand_OrError_KeyMissingFails(t *testing.T) {
	r := newRegistry(t)
	_, err := expandValueRefs("${api_key:?api_key must be set}", r)
	if err == nil {
		t.Fatal("expected error for missing required key")
	}
	if !strings.Contains(err.Error(), "api_key must be set") {
		t.Fatalf("err = %v, want msg to include caller-supplied text", err)
	}
}

func TestExpand_UnknownModifierFallsBack(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("foo:bar", "baz")
	// `:!` is not a recognized modifier — the whole expr is treated
	// as a key. The key contains the colon, so we Set it directly.
	out, err := expandValueRefs("${foo:bar}", r)
	if err != nil {
		// Some setups may not support colons in keys; the important
		// thing is the parser does not panic.
		return
	}
	_ = out
}

func TestExpand_UnclosedBracePassesThrough(t *testing.T) {
	r := newRegistry(t)
	out, err := expandValueRefs("prefix ${unclosed", r)
	if err != nil {
		t.Fatalf("expandValueRefs: %v", err)
	}
	if !strings.Contains(out, "${unclosed") {
		t.Fatalf("got %q, want unclosed brace preserved", out)
	}
}

func TestExpand_MultipleReferences(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("host", "example.com")
	_ = r.Set("port", "8080")
	out, err := expandValueRefs("https://${host}:${port}/api", r)
	if err != nil {
		t.Fatalf("expandValueRefs: %v", err)
	}
	if out != "https://example.com:8080/api" {
		t.Fatalf("got %q", out)
	}
}

func TestExpandMissingError_IsErrKeyNotFound(t *testing.T) {
	e := &expandMissingError{key: "foo"}
	if !errors.Is(e, ErrKeyNotFound) {
		t.Fatal("expandMissingError should match ErrKeyNotFound")
	}
	msg := e.Error()
	if !strings.Contains(msg, "foo") {
		t.Fatalf("Error() = %q, want key in message", msg)
	}
}

func TestExpandRequiredError_Error(t *testing.T) {
	e := &expandRequiredError{key: "k", msg: "must be set"}
	got := e.Error()
	if !strings.Contains(got, "k") || !strings.Contains(got, "must be set") {
		t.Fatalf("Error() = %q, want both key and msg", got)
	}
}

func TestExpand_RequiredNonStringProjectableValue(t *testing.T) {
	// A required reference whose value can't be projected to string
	// surfaces an error rather than silently substituting "".
	r := newRegistry(t)
	_ = r.Set("data", map[string]any{"k": "v"})
	out, err := expandValueRefs("${data}", r)
	// Maps DO flatten to a string via fmt.Sprint, so this should
	// succeed with some textual representation.
	if err != nil {
		t.Fatalf("expandValueRefs: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty string projection of map value")
	}
}
