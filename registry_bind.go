package recon

import (
	"context"
	"fmt"
	"reflect"
)

// Bind populates target from the registry's current snapshot. target
// MUST be a non-nil pointer to a struct; the walker recurses
// through the struct, parsing each field's tag, resolving the path
// against [Registry.Get], coercing the value into the field's Go
// type, and assigning. Errors aggregate into a [*MultiError] under
// FailCollect (the default) and short-circuit on the first failure
// under FailFast.
//
// Tag grammar and the `recon` → `env` → `json` → `yaml` → `toml`
// fallback chain are documented on [TagName] and [FieldTag].
func (r *Registry) Bind(target any, opts ...DecodeOption) error {
	return r.BindContext(context.Background(), target, opts...)
}

// BindContext is the context-aware variant of [Registry.Bind]. The
// context is threaded into any [UnmarshalerContext] / [ValidatorContext]
// hooks the bind target may implement.
func (r *Registry) BindContext(ctx context.Context, target any, opts ...DecodeOption) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	cfg := r.buildDecodeOptions(ctx, opts)

	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("%w: Bind target must be a non-nil pointer, got %T",
			ErrInvalidPath, target)
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("%w: Bind target must point to a struct, got %s",
			ErrInvalidPath, rv.Type())
	}

	w := &bindWalker{
		registry:  r,
		opts:      cfg,
		errs:      &MultiError{},
		consulted: map[string]struct{}{},
	}
	w.walk(rv, r.prefix)

	// Optional whole-target validation: a target implementing
	// Validator / ValidatorContext gets a final pass after every field
	// has been populated.
	w.runValidatorHooks(ctx, target)

	// Strict mode: every snapshot key the struct didn't consult is
	// reported as a *UnknownKeyError. Scoped to the registry's prefix
	// so a Sub(...).Bind only complains about its own sub-tree.
	if cfg.strict != nil && *cfg.strict {
		w.emitUnknownKeyErrors()
	}

	if len(w.errs.Errors) == 0 {
		return nil
	}
	if len(w.errs.Errors) == 1 {
		return w.errs.Errors[0]
	}
	return w.errs
}

// Unmarshal is an alias for [Registry.Bind]. The name matches the
// stdlib encoding-package convention (Marshal / Unmarshal) so the
// verb is interchangeable with other decoder APIs callers may
// already be using.
func (r *Registry) Unmarshal(target any, opts ...DecodeOption) error {
	return r.Bind(target, opts...)
}

// UnmarshalKey binds the registry's sub-tree rooted at key into
// target. Equivalent to `r.Sub(key).Bind(target, opts...)` but spelled
// at the registry level for callers who want a one-line "bind just
// this prefix" entry point.
//
// Empty key is equivalent to [Registry.Bind] on the root registry.
func (r *Registry) UnmarshalKey(key string, target any, opts ...DecodeOption) error {
	if key == "" {
		return r.Bind(target, opts...)
	}
	return r.Sub(key).Bind(target, opts...)
}

// buildDecodeOptions merges call-time DecodeOptions with the registry's
// configured defaults. Registry-level settings (strict, errorBehavior)
// supply the baseline; per-call options override.
func (r *Registry) buildDecodeOptions(ctx context.Context, opts []DecodeOption) decodeOptions {
	cfg := decodeOptions{
		tagName: TagName,
		ctx:     ctx,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.tagName == "" {
		cfg.tagName = TagName
	}
	if cfg.ctx == nil {
		cfg.ctx = ctx
	}
	// Bridge registry-level defaults that the per-call cfg did not set.
	if cfg.strict == nil {
		s := r.state.opts.strict
		cfg.strict = &s
	}
	if cfg.errorBehavior == nil {
		b := r.state.opts.errorBehavior
		cfg.errorBehavior = &b
	}
	return cfg
}

// Unmarshaler is the optional decode-side hook a bind-target field may
// implement to take over its own decoding. coerce dispatches to
// Unmarshaler first, then to UnmarshalEnv (env.Secret-style), then to
// UnmarshalText (encoding.TextUnmarshaler) — the first hook a target
// implements wins.
type Unmarshaler interface {
	UnmarshalRecon(v Value) error
}
