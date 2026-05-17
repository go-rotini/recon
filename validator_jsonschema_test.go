package recon

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestJSONSchemaValidator_AcceptsValidInstance(t *testing.T) {
	schema := []byte(`{
		"type": "object",
		"properties": {
			"port": {"type": "integer", "minimum": 1, "maximum": 65535},
			"name": {"type": "string"}
		},
		"required": ["port"]
	}`)
	v, err := NewJSONSchemaValidator(schema)
	if err != nil {
		t.Fatalf("NewJSONSchemaValidator: %v", err)
	}
	err = v.Validate(map[string]any{
		"port": int64(8080),
		"name": "rotini",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestJSONSchemaValidator_CatchesRequired(t *testing.T) {
	v := mustValidator(t, `{
		"type": "object",
		"required": ["host"],
		"properties": {"host": {"type": "string"}}
	}`)
	err := v.Validate(map[string]any{}) // host missing
	assertValidationErrorKeyword(t, err, "required")
}

func TestJSONSchemaValidator_CatchesEnum(t *testing.T) {
	v := mustValidator(t, `{
		"type": "object",
		"properties": {
			"env": {"type": "string", "enum": ["dev", "staging", "prod"]}
		}
	}`)
	err := v.Validate(map[string]any{"env": "production"})
	assertValidationErrorKeyword(t, err, "enum")
}

func TestJSONSchemaValidator_CatchesPattern(t *testing.T) {
	v := mustValidator(t, `{
		"type": "object",
		"properties": {
			"id": {"type": "string", "pattern": "^[a-z]+$"}
		}
	}`)
	err := v.Validate(map[string]any{"id": "Mixed-Case"})
	assertValidationErrorKeyword(t, err, "pattern")
}

func TestJSONSchemaValidator_CatchesMinimumAndMaximum(t *testing.T) {
	v := mustValidator(t, `{
		"type": "object",
		"properties": {
			"port": {"type": "integer", "minimum": 1, "maximum": 65535}
		}
	}`)
	tooLow := v.Validate(map[string]any{"port": int64(0)})
	assertValidationErrorKeyword(t, tooLow, "minimum")

	tooHigh := v.Validate(map[string]any{"port": int64(70000)})
	assertValidationErrorKeyword(t, tooHigh, "maximum")
}

func TestJSONSchemaValidator_CatchesRefViolation(t *testing.T) {
	// $ref points to a nested $defs entry; the value violates the
	// referenced subschema's "type" assertion.
	v := mustValidator(t, `{
		"type": "object",
		"properties": {"port": {"$ref": "#/$defs/portNum"}},
		"$defs": {
			"portNum": {"type": "integer", "minimum": 1}
		}
	}`)
	err := v.Validate(map[string]any{"port": "not-a-number"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err=%v, want wrap of ErrValidation", err)
	}
}

func TestJSONSchemaValidator_CompileFailureReportsSchemaInvalid(t *testing.T) {
	_, err := NewJSONSchemaValidator([]byte(`{"type": 12345}`))
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("err=%v, want wrap of ErrSchemaInvalid", err)
	}
}

func TestJSONSchemaValidator_NilOrEmptySchemaIsNoop(t *testing.T) {
	var v *JSONSchemaValidator
	if err := v.Validate(map[string]any{"k": "v"}); err != nil {
		t.Fatalf("nil validator returned err=%v", err)
	}
}

func TestJSONSchemaValidator_YAMLInput(t *testing.T) {
	v, err := NewJSONSchemaValidatorYAML([]byte(`
type: object
properties:
  port:
    type: integer
    minimum: 1
required:
  - port
`))
	if err != nil {
		t.Fatalf("NewJSONSchemaValidatorYAML: %v", err)
	}
	if err := v.Validate(map[string]any{"port": int64(8080)}); err != nil {
		t.Fatalf("valid instance: %v", err)
	}
	if err := v.Validate(map[string]any{}); err == nil {
		t.Fatal("required keyword not enforced via YAML schema")
	}
}

func TestJSONSchemaValidator_TOMLInput(t *testing.T) {
	v, err := NewJSONSchemaValidatorTOML([]byte(`
type = "object"

[properties.port]
type = "integer"
minimum = 1
`))
	if err != nil {
		t.Fatalf("NewJSONSchemaValidatorTOML: %v", err)
	}
	if err := v.Validate(map[string]any{"port": int64(0)}); err == nil {
		t.Fatal("minimum keyword not enforced via TOML schema")
	}
}

func TestJSONSchemaValidator_AggregatesMultipleFailures(t *testing.T) {
	v := mustValidator(t, `{
		"type": "object",
		"properties": {
			"port": {"type": "integer", "minimum": 1},
			"env":  {"type": "string", "enum": ["dev", "prod"]}
		}
	}`)
	err := v.Validate(map[string]any{
		"port": int64(0),
		"env":  "staging",
	})
	if err == nil {
		t.Fatal("expected multi-error, got nil")
	}
	var multi *MultiError
	if !errors.As(err, &multi) {
		t.Fatalf("err is %T, want *MultiError", err)
	}
	if len(multi.Errors) < 2 {
		t.Fatalf("MultiError has %d errs, want ≥ 2", len(multi.Errors))
	}
	// Every aggregated entry is a *ValidationError tagged with a
	// keyword from the schema.
	gotKeywords := make([]string, 0, len(multi.Errors))
	for _, sub := range multi.Errors {
		var ve *ValidationError
		if !errors.As(sub, &ve) {
			t.Fatalf("sub err is %T, want *ValidationError", sub)
		}
		gotKeywords = append(gotKeywords, ve.Rule)
	}
	if !slices.Contains(gotKeywords, "minimum") || !slices.Contains(gotKeywords, "enum") {
		t.Fatalf("keywords=%v, want both minimum and enum", gotKeywords)
	}
}

func TestJSONSchemaValidator_PathFromInstanceLocation(t *testing.T) {
	v := mustValidator(t, `{
		"type": "object",
		"properties": {
			"server": {
				"type": "object",
				"properties": {
					"port": {"type": "integer", "minimum": 1}
				}
			}
		}
	}`)
	err := v.Validate(map[string]any{
		"server": map[string]any{"port": int64(0)},
	})
	var multi *MultiError
	if !errors.As(err, &multi) {
		t.Fatalf("err is %T", err)
	}
	for _, sub := range multi.Errors {
		var ve *ValidationError
		if errors.As(sub, &ve) && ve.Rule == "minimum" {
			if ve.Path.String() != "server.port" {
				t.Fatalf("path=%q, want server.port", ve.Path.String())
			}
			return
		}
	}
	t.Fatal("no *ValidationError with rule=minimum found")
}

func TestJSONSchemaValidator_PathUnescapesPointerEscapes(t *testing.T) {
	// JSON Pointer escapes ~0 → ~ and ~1 → /; the path translator must
	// reverse them so callers see the original key spelling.
	if got := jsonPointerToPath("/a~1b/c~0d"); got.String() != "a/b.c~d" {
		t.Fatalf("path=%q, want a/b.c~d", got.String())
	}
	if got := jsonPointerToPath(""); len(got) != 0 {
		t.Fatalf("root pointer should yield empty path, got %v", got)
	}
}

// TestRegistry_ValidatorRunsOnRebuild verifies the registry-level
// integration: a Reload that produces an invalid snapshot returns the
// validator's error, while a Set that triggers an invalid snapshot is
// logged-and-swallowed (the snapshot still installs). The choice keeps
// in-progress write sequences from being aborted by an intermediate
// invalid state — write-then-write-then-validate is a common pattern.
func TestRegistry_ValidatorRunsOnRebuild(t *testing.T) {
	v := mustValidator(t, `{
		"type": "object",
		"required": ["port"],
		"properties": {"port": {"type": "integer", "minimum": 1}}
	}`)
	r, err := New(WithValidator(v))
	if err != nil {
		// New uses the write-path (log-and-discard) variant, so a
		// validation failure at construction time must NOT escape.
		t.Fatalf("New propagated validation err: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// Reload propagates the error.
	if rerr := r.Reload(); rerr == nil {
		t.Fatal("Reload should propagate validation failure")
	} else if !errors.Is(rerr, ErrValidation) {
		t.Fatalf("Reload err=%v, want wrap of ErrValidation", rerr)
	}

	// Fix the snapshot via Set — write path swallows the error.
	if serr := r.Set("port", 8080); serr != nil {
		t.Fatalf("Set: %v", serr)
	}
	// Reload now passes.
	if rerr := r.Reload(); rerr != nil {
		t.Fatalf("Reload after fix: %v", rerr)
	}
}

// mustValidator compiles a schema or fails the test. Centralised so
// every schema-based test stays concise.
func mustValidator(t *testing.T, schemaJSON string) *JSONSchemaValidator {
	t.Helper()
	v, err := NewJSONSchemaValidator([]byte(schemaJSON))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return v
}

// assertValidationErrorKeyword fails the test unless err wraps a
// *ValidationError whose Rule matches keyword.
func assertValidationErrorKeyword(t *testing.T, err error, keyword string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error for keyword %q, got nil", keyword)
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err=%v, want wrap of ErrValidation", err)
	}
	var multi *MultiError
	if !errors.As(err, &multi) {
		t.Fatalf("err is %T, want *MultiError; err=%v", err, err)
	}
	for _, sub := range multi.Errors {
		var ve *ValidationError
		if errors.As(sub, &ve) && ve.Rule == keyword {
			return
		}
	}
	// Fallback: dump all rules we did see so the failure message is
	// debuggable.
	got := make([]string, 0, len(multi.Errors))
	for _, sub := range multi.Errors {
		var ve *ValidationError
		if errors.As(sub, &ve) {
			got = append(got, ve.Rule)
		}
	}
	t.Fatalf("no *ValidationError with rule=%q; saw rules=%v (%s)",
		keyword, got, strings.Join(got, ","))
}
