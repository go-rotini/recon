package recon

import (
	"context"
	"fmt"
	"reflect"
)

// Bind populates target from the current snapshot. target must be a
// non-nil pointer to a struct. Errors aggregate into a [*MultiError]
// under [FailCollect] (the default) or short-circuit on the first
// under [FailFast]. Tag grammar lives on [FieldTag].
func (r *Registry) Bind(target any, opts ...DecodeOption) error {
	return r.BindContext(context.Background(), target, opts...)
}

// BindContext is the context-aware [Bind]. ctx is threaded into any
// [ValidatorContext] hook the target implements.
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
	w.runValidatorHooks(ctx, target)

	// Strict mode: every snapshot key the struct didn't consult is
	// reported as a *UnknownKeyError, scoped to the registry's prefix.
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

// Unmarshal is an alias for [Registry.Bind], named to mirror the
// stdlib encoding/Marshal-Unmarshal convention.
func (r *Registry) Unmarshal(target any, opts ...DecodeOption) error {
	return r.Bind(target, opts...)
}

// UnmarshalKey binds the registry's sub-tree at key into target —
// equivalent to r.Sub(key).Bind(target, opts...). An empty key is
// equivalent to [Bind].
func (r *Registry) UnmarshalKey(key string, target any, opts ...DecodeOption) error {
	if key == "" {
		return r.Bind(target, opts...)
	}
	return r.Sub(key).Bind(target, opts...)
}

// buildDecodeOptions merges call-time options with the registry's
// configured defaults. Per-call values override.
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

// Unmarshaler is the optional decode hook a bind-target field may
// implement to take over its own decoding. coerce tries [Unmarshaler]
// first, then UnmarshalEnv, then encoding.TextUnmarshaler.
type Unmarshaler interface {
	UnmarshalRecon(v Value) error
}
