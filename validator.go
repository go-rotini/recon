package recon

import "context"

// SchemaValidator validates a fully-resolved snapshot, given as the
// nested map [Snapshot.AsMap] returns. Implementations must be safe for
// concurrent use; the registry calls Validate on every snapshot
// rebuild and rejects the rebuild on error. The bundled
// [JSONSchemaValidator] is backed by go-rotini/jsonschema.
type SchemaValidator interface {
	Validate(snapshot map[string]any) error
}

// Validator is the optional whole-struct validation hook a bind
// target may implement. [Registry.Bind] calls Validate after populating
// the fields (skipped under [FailFast] once a field failed) and
// returns its error together with any field errors; the fields stay
// populated.
type Validator interface {
	Validate() error
}

// ValidatorContext is the context-aware variant of [Validator], used
// in its place when a target implements both. The context is the one
// passed to [Registry.BindContext] or [WithDecodeContext].
type ValidatorContext interface {
	Validate(ctx context.Context) error
}
