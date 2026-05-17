package recon

import "context"

// SchemaValidator validates a fully-resolved snapshot (as a nested
// map[string]any) and returns a non-nil error if validation fails.
// Implementations are expected to be cheap to construct and safe for
// concurrent use — the registry may call Validate on every load and reload.
//
// The bundled implementation [JSONSchemaValidator] is backed by
// go-rotini/jsonschema (lands in Phase 5). Users can plug in any other
// validator — CEL, gojsonschema, a custom DSL — behind this interface via
// [WithValidator].
type SchemaValidator interface {
	Validate(snapshot map[string]any) error
}

// Validator is the optional whole-struct validation hook a bind-target may
// implement. The decoder calls Validate after every field has been
// populated; a non-nil return aborts the bind with the wrapped error.
//
// Mirrors the same-named interface in go-rotini/env so a struct can
// implement Validator once and be acceptable to either package.
type Validator interface {
	Validate() error
}

// ValidatorContext is like [Validator] but receives the context threaded
// through the bind path (via [WithDecodeContext] or [Registry.LoadContext]).
// Implement this in preference to Validator when the validation needs to
// honor cancellation or carry request-scoped values.
type ValidatorContext interface {
	Validate(ctx context.Context) error
}
