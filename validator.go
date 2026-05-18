package recon

import "context"

// SchemaValidator validates a fully-resolved snapshot. Implementations
// must be cheap to construct and safe for concurrent use; the
// registry calls Validate on every reload. The bundled
// [JSONSchemaValidator] is backed by go-rotini/jsonschema.
type SchemaValidator interface {
	Validate(snapshot map[string]any) error
}

// Validator is the optional whole-struct validation hook a bind
// target may implement. The decoder calls Validate after every field
// has been populated; a non-nil return aborts the bind.
type Validator interface {
	Validate() error
}

// ValidatorContext is the context-aware variant of [Validator]. The
// context is the one threaded through [Registry.BindContext] or
// [WithDecodeContext]. Implement this when validation must honor
// cancellation or carry request-scoped values.
type ValidatorContext interface {
	Validate(ctx context.Context) error
}
