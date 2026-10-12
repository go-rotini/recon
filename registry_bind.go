package recon

import (
	"context"
	"fmt"
	"reflect"
)

// Bind populates target from the current snapshot. target must be a
// non-nil pointer to a struct. Errors aggregate into a [*MultiError]
// under [FailCollect] (the default) or short-circuit on the first
// under [FailFast]; a single error is returned unwrapped. Tag grammar
// lives on [FieldTag].
//
// String values are converted to the field's type: integers follow
// [strconv.ParseInt] with base 0, so "0x10" is 16, "010" is 8 and
// "1_000" is 1000; floats use [strconv.ParseFloat], bools
// [strconv.ParseBool], and durations [time.ParseDuration]. A string
// field takes a non-string value's fmt.Sprint form. Slices and maps
// split strings on the `separator=` and `kvSeparator=` tag options.
// A field whose type implements [Unmarshaler], UnmarshalEnv(string)
// error, or [encoding.TextUnmarshaler] decodes itself, in that order.
func (r *Registry) Bind(target any, opts ...DecodeOption) error {
	return r.BindContext(context.Background(), target, opts...)
}

// BindContext is [Registry.Bind] with ctx passed to the target's
// [ValidatorContext] hook, unless [WithDecodeContext] overrides it.
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
	// Validator hooks see cfg.ctx so [WithDecodeContext] can override
	// the BindContext-supplied ctx when callers reach Bind via the
	// no-ctx alias [Bind]. cfg.ctx falls back to ctx in
	// buildDecodeOptions, so this still chains correctly when no
	// override is supplied.
	w.runValidatorHooks(cfg.ctx, target) //nolint:contextcheck // intentional: WithDecodeContext override

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
// equivalent to [Registry.Bind].
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
// implement to take over its own decoding. It receives the resolved
// [Value] with its wire kind intact and takes priority over
// UnmarshalEnv and [encoding.TextUnmarshaler].
type Unmarshaler interface {
	UnmarshalRecon(v Value) error
}
