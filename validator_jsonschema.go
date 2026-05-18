package recon

import (
	"fmt"

	"github.com/go-rotini/jsonschema"
)

// JSONSchemaValidator is the bundled [SchemaValidator] backed by
// go-rotini/jsonschema. The schema is compiled once at construction;
// per-snapshot validation is then cheap and lock-free.
type JSONSchemaValidator struct {
	schema *jsonschema.Schema
}

// NewJSONSchemaValidator compiles schemaJSON and returns a validator
// for [WithValidator]. Returns a wrapped [ErrSchemaInvalid] on
// compile failure.
func NewJSONSchemaValidator(schemaJSON []byte) (*JSONSchemaValidator, error) {
	s, err := jsonschema.Compile(schemaJSON)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSchemaInvalid, err)
	}
	return &JSONSchemaValidator{schema: s}, nil
}

// NewJSONSchemaValidatorYAML compiles a YAML-encoded schema.
func NewJSONSchemaValidatorYAML(schemaYAML []byte) (*JSONSchemaValidator, error) {
	s, err := jsonschema.LoadYAML(schemaYAML)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSchemaInvalid, err)
	}
	return &JSONSchemaValidator{schema: s}, nil
}

// NewJSONSchemaValidatorTOML compiles a TOML-encoded schema.
func NewJSONSchemaValidatorTOML(schemaTOML []byte) (*JSONSchemaValidator, error) {
	s, err := jsonschema.LoadTOML(schemaTOML)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSchemaInvalid, err)
	}
	return &JSONSchemaValidator{schema: s}, nil
}

// NewJSONSchemaValidatorJSONC compiles a JSONC-encoded schema.
func NewJSONSchemaValidatorJSONC(schemaJSONC []byte) (*JSONSchemaValidator, error) {
	s, err := jsonschema.LoadJSONC(schemaJSONC)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSchemaInvalid, err)
	}
	return &JSONSchemaValidator{schema: s}, nil
}

// NewJSONSchemaValidatorFromSchema wraps an already-compiled
// jsonschema.Schema. Use when the caller assembled the schema with
// custom CompileOptions (remote $ref loaders, draft pinning).
func NewJSONSchemaValidatorFromSchema(s *jsonschema.Schema) *JSONSchemaValidator {
	return &JSONSchemaValidator{schema: s}
}

// Validate runs snapshot through the compiled schema. On failure,
// each constraint violation is translated into a [*ValidationError]
// and aggregated under a [*MultiError]. A nil snapshot is treated as
// the empty object.
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

// translateSchemaError converts a jsonschema.ValidationError into a
// recon [*ValidationError]. Compound applicators (anyOf, $ref, oneOf)
// carry nested causes on .Causes; only the top-level assertion is
// surfaced today.
func translateSchemaError(e *jsonschema.ValidationError) *ValidationError {
	return &ValidationError{
		Path: jsonPointerToPath(e.InstanceLocation),
		Rule: e.Keyword,
		Msg:  e.Message,
	}
}

// jsonPointerToPath rewrites a JSON Pointer ("/server/port") into a
// recon [Path]. RFC 6901 tilde escapes (~0, ~1) are reversed.
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

// unescapeJSONPointer reverses ~1 → / and ~0 → ~ from RFC 6901.
func unescapeJSONPointer(s string) string {
	if s == "" {
		return s
	}
	// Fast path: no tildes, return the string unchanged.
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
