package recon

import (
	"fmt"

	"github.com/go-rotini/jsonschema"
)

// JSONSchemaValidator is the bundled [SchemaValidator] backed by
// go-rotini/jsonschema. Compile the schema once at construction;
// validation against snapshots is then cheap and lock-free.
//
// The validator accepts any input go-rotini/jsonschema does: raw JSON
// bytes via [NewJSONSchemaValidator], YAML/TOML/JSONC via
// [NewJSONSchemaValidatorYAML] / [NewJSONSchemaValidatorTOML] /
// [NewJSONSchemaValidatorJSONC], or a pre-compiled *jsonschema.Schema
// via [NewJSONSchemaValidatorFromSchema] for callers that want
// fine-grained control over compile options.
type JSONSchemaValidator struct {
	schema *jsonschema.Schema
}

// NewJSONSchemaValidator compiles schemaJSON and returns a validator
// ready to plug into [WithValidator]. Returns an error wrapping
// [ErrSchemaInvalid] when the schema fails to compile.
func NewJSONSchemaValidator(schemaJSON []byte) (*JSONSchemaValidator, error) {
	s, err := jsonschema.Compile(schemaJSON)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSchemaInvalid, err)
	}
	return &JSONSchemaValidator{schema: s}, nil
}

// NewJSONSchemaValidatorYAML compiles a YAML-encoded schema. The schema
// itself is parsed from YAML; the snapshot is still validated as a Go
// value (no YAML round-trip per Validate call).
func NewJSONSchemaValidatorYAML(schemaYAML []byte) (*JSONSchemaValidator, error) {
	s, err := jsonschema.LoadYAML(schemaYAML)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSchemaInvalid, err)
	}
	return &JSONSchemaValidator{schema: s}, nil
}

// NewJSONSchemaValidatorTOML is the TOML-encoded counterpart of
// [NewJSONSchemaValidatorYAML].
func NewJSONSchemaValidatorTOML(schemaTOML []byte) (*JSONSchemaValidator, error) {
	s, err := jsonschema.LoadTOML(schemaTOML)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSchemaInvalid, err)
	}
	return &JSONSchemaValidator{schema: s}, nil
}

// NewJSONSchemaValidatorJSONC is the JSONC-encoded counterpart of
// [NewJSONSchemaValidatorYAML]. Useful when shipping a heavily-commented
// schema next to the codebase.
func NewJSONSchemaValidatorJSONC(schemaJSONC []byte) (*JSONSchemaValidator, error) {
	s, err := jsonschema.LoadJSONC(schemaJSONC)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSchemaInvalid, err)
	}
	return &JSONSchemaValidator{schema: s}, nil
}

// NewJSONSchemaValidatorFromSchema wraps an already-compiled
// *jsonschema.Schema. Use when the caller has assembled the schema with
// custom CompileOptions (remote $ref loaders, draft pinning) and wants
// to share it across validators.
func NewJSONSchemaValidatorFromSchema(s *jsonschema.Schema) *JSONSchemaValidator {
	return &JSONSchemaValidator{schema: s}
}

// Validate implements [SchemaValidator]. It runs the supplied snapshot
// through the compiled schema; on failure every constraint violation is
// translated into a recon [*ValidationError] and aggregated under a
// [*MultiError] so the caller sees the full list rather than just the
// first failure.
//
// A nil snapshot is treated as the empty object — same handling as
// [Snapshot.AsMap] on an empty registry.
func (v *JSONSchemaValidator) Validate(snapshot map[string]any) error {
	if v == nil || v.schema == nil {
		return nil
	}
	input := snapshot
	if input == nil {
		input = map[string]any{}
	}
	result, err := v.schema.ValidateValue(input)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrValidation, err)
	}
	if result.Valid {
		return nil
	}
	multi := &MultiError{}
	for i := range result.Errors {
		multi.Append(translateSchemaError(&result.Errors[i]))
	}
	return multi
}

// translateSchemaError converts one jsonschema.ValidationError into a
// recon *ValidationError. The instance pointer ("/server/port") is
// rewritten into a [Path] (server.port); the schema keyword
// ("minimum") becomes the Rule field.
//
// Compound applicators (anyOf, $ref, oneOf) carry their nested causes
// on jsonschema.ValidationError.Causes; we currently flatten just the
// top-level error per spec call — Phase 11 may add structured
// presentation of the cause tree.
func translateSchemaError(e *jsonschema.ValidationError) *ValidationError {
	return &ValidationError{
		Path: jsonPointerToPath(e.InstanceLocation),
		Rule: e.Keyword,
		Msg:  e.Message,
	}
}

// jsonPointerToPath rewrites a JSON Pointer ("/server/port") into a
// recon [Path] (`Path{"server", "port"}`). Tilde escapes (~0, ~1) are
// reversed per RFC 6901. An empty pointer (the document root) returns
// an empty Path.
func jsonPointerToPath(pointer string) Path {
	if pointer == "" || pointer == "/" {
		return Path{}
	}
	if pointer[0] == '/' {
		pointer = pointer[1:]
	}
	segments := splitJSONPointer(pointer)
	for i, seg := range segments {
		segments[i] = unescapeJSONPointer(seg)
	}
	return Path(segments)
}

// splitJSONPointer splits the pointer on '/'. Implemented inline rather
// than calling strings.Split so the caller's intent stays visible in
// the function name.
func splitJSONPointer(s string) []string {
	out := []string{}
	start := 0
	for i := range len(s) {
		if s[i] == '/' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// unescapeJSONPointer reverses RFC 6901's tilde escapes: ~1 → /, ~0 → ~.
func unescapeJSONPointer(s string) string {
	if s == "" {
		return s
	}
	// Two-pass to keep the common (no-escape) path allocation-free.
	hasTilde := false
	for i := range len(s) {
		if s[i] == '~' {
			hasTilde = true
			break
		}
	}
	if !hasTilde {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '~' && i+1 < len(s) {
			switch s[i+1] {
			case '0':
				out = append(out, '~')
				i++
				continue
			case '1':
				out = append(out, '/')
				i++
				continue
			}
		}
		out = append(out, s[i])
	}
	return string(out)
}
