package recon

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

// Bind populates target from the registry's current snapshot. target
// MUST be a non-nil pointer to a struct; the walker recurses through
// the struct, parsing each field's tag, resolving the path against
// [Registry.Get], coercing the value into the field's Go type, and
// assigning. Errors aggregate into a [*MultiError] under FailCollect
// (the default) and short-circuit on the first failure under FailFast.
//
// See §6 of the requirements doc for the tag grammar and the
// recon → env → json → yaml → toml fallback order.
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
		registry: r,
		opts:     cfg,
		errs:     &MultiError{},
	}
	w.walk(rv, r.prefix)

	// Optional whole-target validation: a target implementing
	// Validator / ValidatorContext gets a final pass after every field
	// has been populated.
	w.runValidatorHooks(ctx, target)

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

// bindWalker carries the state a single Bind call needs as it descends
// into nested structs: the source registry, the merged decode options,
// and the accumulator for per-field errors.
type bindWalker struct {
	registry *Registry
	opts     decodeOptions
	errs     *MultiError
}

// walk descends one struct level at a time. prefix is the parent's
// path (already prepended with the registry's sub-prefix); the walker
// appends each field's name / tag-path under it.
func (w *bindWalker) walk(rv reflect.Value, prefix Path) {
	t := rv.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		fv := rv.Field(i)
		tag := w.tagFor(sf)
		if tag.Skip {
			continue
		}

		// Nested struct (non-time.Time, non-Unmarshaler): recurse,
		// honoring `inline` / `embedded` flattening. Pointer-to-
		// struct fields are treated identically — the walker
		// allocates the pointee on first descent.
		if isWalkableStructValue(fv) {
			w.walkNested(fv, prefix, sf, tag)
			continue
		}

		// Leaf field: resolve, coerce, assign.
		w.bindLeaf(fv, prefix, sf, tag)
		if w.shouldShortCircuit() {
			return
		}
	}
}

// walkNested handles fields whose Go type is itself a struct (or a
// pointer to one). The decision tree:
//
//  1. `inline` tag or anonymous embedded struct → recurse with the
//     same prefix (no field name added).
//  2. Otherwise → recurse with prefix + field-name segment.
//
// Pointer-to-struct fields have their pointee allocated on first
// descent so subsequent walks see a settable struct.
func (w *bindWalker) walkNested(fv reflect.Value, prefix Path, sf reflect.StructField, tag FieldTag) {
	if fv.Kind() == reflect.Pointer {
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		fv = fv.Elem()
	}
	switch {
	case tag.Inline || (sf.Anonymous && tag.Name == ""):
		w.walk(fv, prefix)
	default:
		segments := w.segmentsFor(sf, tag)
		w.walk(fv, prefix.Append(segments...))
	}
}

// bindLeaf resolves the field's key, runs the value through coerce,
// and applies per-field options (default, required, notEmpty,
// custom decoder). Two side effects on the registry:
//
//   - `secret` propagates to MarkSecret so Describe / Save redact.
//   - `immutable` baselines the resolved value so subsequent reload
//     candidates that change it are rejected by the snapshot rebuild.
func (w *bindWalker) bindLeaf(fv reflect.Value, prefix Path, sf reflect.StructField, tag FieldTag) {
	path := w.pathFor(prefix, sf, tag)
	if tag.Secret {
		w.registry.MarkSecret(path.String())
	}
	value, found, lookupErr := w.lookup(path, tag)
	if lookupErr != nil {
		w.appendErr(&SourceError{Source: value.Source(), Op: "get", Cause: lookupErr})
		return
	}
	if tag.Immutable && found {
		w.registry.markImmutable(path, value)
	}

	// Custom decoder dispatch wins outright when one matches the field
	// type — it's the "I'll handle every coercion concern myself" hook.
	if dec, ok := w.opts.customDecoders[fv.Type().String()]; ok && found {
		out, err := dec(value)
		if err != nil {
			w.appendErr(&CoercionError{
				Path: path, Source: value.Source(),
				WireType: value.Kind().String(),
				Target:   fv.Type().String(), Cause: err,
			})
			return
		}
		if err := assignCustomDecoded(fv, out); err != nil {
			w.appendErr(&CoercionError{
				Path: path, Target: fv.Type().String(), Cause: err,
			})
		}
		return
	}

	if !found {
		switch {
		case tag.HasDefault:
			value = NewValue(tag.DefaultValue)
		case tag.Required:
			w.appendErr(&MissingRequiredError{Path: path})
			return
		default:
			return // field stays at its Go zero value
		}
	}

	if tag.NotEmpty {
		s, asErr := valueAsString(value)
		if asErr != nil {
			w.appendErr(&CoercionError{
				Path: path, Source: value.Source(),
				WireType: value.Kind().String(),
				Target:   fv.Type().String(), Cause: asErr,
			})
			return
		}
		if s == "" {
			w.appendErr(&EmptyValueError{Path: path, Source: value.Source()})
			return
		}
	}

	if err := coerce(value, fv, tag); err != nil {
		w.appendErr(&CoercionError{
			Path: path, Source: value.Source(),
			WireType: value.Kind().String(),
			Target:   fv.Type().String(), Cause: err,
		})
	}
}

// lookup resolves a path through the registry's snapshot. When the
// tag pins a specific source (via `source=<name>`), the value MUST
// come from that source — a hit from another source is reported as
// "not found" so the field falls through to default / required logic.
func (w *bindWalker) lookup(path Path, tag FieldTag) (Value, bool, error) {
	v, ok, err := w.registry.GetPath(path)
	if !ok || err != nil {
		// Try aliases.
		for _, alias := range tag.Aliases {
			ap := ParsePath(alias)
			av, aok, aerr := w.registry.GetPath(ap)
			if aerr != nil {
				continue
			}
			if aok {
				return av, true, nil
			}
		}
		return v, ok, err
	}
	if tag.Source != "" && v.Source() != tag.Source {
		return Value{}, false, nil
	}
	return v, true, nil
}

// pathFor computes the canonical Path the leaf binder uses. The
// precedence is: explicit `path=` tag option > tag Name (transformed
// per the field's `transform=` option) > Go field name (snake-cased).
//
// A tag.Name that contains the path delimiter is parsed as a path,
// so `recon:"db.dsn"` resolves the canonical "db.dsn" key rather
// than treating "db.dsn" as a single bracket-escaped segment.
func (w *bindWalker) pathFor(prefix Path, sf reflect.StructField, tag FieldTag) Path {
	if tag.Path != "" {
		return ParsePath(tag.Path)
	}
	segments := w.segmentsFor(sf, tag)
	return prefix.Append(segments...)
}

// segmentsFor returns the path segments representing this field. With
// `transform=` set, each segment is re-spelled (snake / kebab / etc.).
// A tag Name containing the path delimiter splits into multiple
// segments — the common "server.port" idiom Just Works.
func (w *bindWalker) segmentsFor(sf reflect.StructField, tag FieldTag) []string {
	name := tag.Name
	if name == "" {
		name = sf.Name
	}
	if !strings.Contains(name, DefaultDelimiter) {
		return []string{applyTransform(name, tag.Transform)}
	}
	parsed := ParsePath(name)
	out := make([]string, len(parsed))
	for i, seg := range parsed {
		out[i] = applyTransform(seg, tag.Transform)
	}
	return out
}

// applyTransform rewrites segment per the named transform. An unknown
// or empty transform returns segment unchanged; the parser at field-
// tag time already validated the option value if recon cares.
func applyTransform(segment, transform string) string {
	switch transform {
	case "snake":
		return toSnakeCase(segment)
	case "kebab":
		return strings.ReplaceAll(toSnakeCase(segment), "_", "-")
	case "camel":
		return toCamelCase(segment)
	case "upper":
		return strings.ToUpper(segment)
	case "lower":
		return strings.ToLower(segment)
	default:
		return segment
	}
}

// toSnakeCase rewrites GoFieldName / goFieldName as go_field_name.
// Used by the snake transform and as a stepping-stone for kebab.
func toSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// toCamelCase rewrites snake_case_input → snakeCaseInput. Useful when
// a config file uses snake-case keys but the Go struct exposes camel
// case (a common rotini → struct path).
func toCamelCase(s string) string {
	parts := strings.Split(s, "_")
	if len(parts) <= 1 {
		return s
	}
	var b strings.Builder
	b.WriteString(parts[0])
	for _, p := range parts[1:] {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

// tagFor extracts the FieldTag for sf, consulting the primary tag
// (cfg.tagName) first and falling back through the
// recon → env → json → yaml → toml chain.
func (w *bindWalker) tagFor(sf reflect.StructField) FieldTag {
	primary := w.opts.tagName
	if raw, ok := sf.Tag.Lookup(primary); ok && raw != "" {
		return ParseTag(raw)
	}
	for _, alt := range fallbackTagNames {
		if alt == primary {
			continue
		}
		if raw, ok := sf.Tag.Lookup(alt); ok && raw != "" {
			return ParseTag(raw)
		}
	}
	// No tag at all: synthesize a tag with the Go field name as the
	// segment. Lower-casing keeps the YAML/TOML convention where
	// "Port" → "port".
	return FieldTag{Name: strings.ToLower(sf.Name)}
}

// shouldShortCircuit reports whether the walker should stop after the
// most recent appendErr. Honors FailFast vs FailCollect from the
// merged decode options.
func (w *bindWalker) shouldShortCircuit() bool {
	if w.opts.errorBehavior == nil {
		return false
	}
	if *w.opts.errorBehavior != FailFast {
		return false
	}
	return len(w.errs.Errors) > 0
}

// appendErr records err. Empty-error appends are dropped silently to
// keep the per-field logic readable at the call site.
func (w *bindWalker) appendErr(err error) {
	if err == nil {
		return
	}
	w.errs.Append(err)
}

// runValidatorHooks invokes the optional [Validator] /
// [ValidatorContext] interface implemented by the bind target. Both
// hooks aggregate into the same MultiError so a single Bind reports
// every problem in one pass.
func (w *bindWalker) runValidatorHooks(ctx context.Context, target any) {
	if w.shouldShortCircuit() {
		return
	}
	if vc, ok := target.(ValidatorContext); ok {
		if err := vc.Validate(ctx); err != nil {
			w.appendErr(err)
		}
		return
	}
	if v, ok := target.(Validator); ok {
		if err := v.Validate(); err != nil {
			w.appendErr(err)
		}
	}
}

// isWalkableStructValue reports whether fv is a struct (or pointer to
// one) the bind walker should recurse into rather than coerce. The
// "leaf" cases the walker must NOT recurse into:
//
//   - time.Time — handled by coerceTime;
//   - any type implementing recon's [Unmarshaler], stdlib
//     encoding.TextUnmarshaler, or env.Secret's UnmarshalEnv hook.
//
// Pointer-to-struct fields are tested via their (allocated, if nil)
// element so the leaf-vs-walk decision sees the actual struct type.
func isWalkableStructValue(fv reflect.Value) bool {
	if fv.Kind() == reflect.Pointer {
		// nil pointer: peek at the element type. If it's a struct AND
		// the addressable form doesn't implement any unmarshal hook,
		// the walker will allocate and recurse.
		if fv.Type().Elem().Kind() != reflect.Struct {
			return false
		}
		probe := reflect.New(fv.Type().Elem())
		if implementsUnmarshalHook(probe.Interface()) {
			return false
		}
		if probe.Elem().Type().PkgPath() == "time" && probe.Elem().Type().Name() == "Time" {
			return false
		}
		return true
	}
	if fv.Kind() != reflect.Struct {
		return false
	}
	if fv.Type().PkgPath() == "time" && fv.Type().Name() == "Time" {
		return false
	}
	if fv.CanAddr() && implementsUnmarshalHook(fv.Addr().Interface()) {
		return false
	}
	return true
}

// implementsUnmarshalHook reports whether v's dynamic type satisfies
// any of recon's leaf-coercion hooks. Centralized so
// [isWalkableStructValue] and [tryUnmarshalerHooks] use exactly the
// same predicate.
func implementsUnmarshalHook(v any) bool {
	if _, ok := v.(Unmarshaler); ok {
		return true
	}
	if _, ok := v.(interface {
		UnmarshalEnv(text string) error
	}); ok {
		return true
	}
	if _, ok := v.(interface {
		UnmarshalText(text []byte) error
	}); ok {
		return true
	}
	return false
}

// assignCustomDecoded copies the value returned by a WithCustomDecoder
// callback into the target field. The callback's return type must be
// assignable to fv's type — typically the same type T the callback's
// signature declares.
func assignCustomDecoded(fv reflect.Value, out any) error {
	rv := reflect.ValueOf(out)
	if !rv.IsValid() {
		return nil
	}
	if !rv.Type().AssignableTo(fv.Type()) {
		return fmt.Errorf("%w: custom decoder returned %s; not assignable to %s",
			ErrCoercion, rv.Type(), fv.Type())
	}
	fv.Set(rv)
	return nil
}

// Unmarshaler is the optional decode-side hook a bind-target field may
// implement to take over its own decoding. coerce dispatches to
// Unmarshaler first, then to UnmarshalEnv (env.Secret-style), then to
// UnmarshalText (encoding.TextUnmarshaler) — the first hook a target
// implements wins.
type Unmarshaler interface {
	UnmarshalRecon(v Value) error
}
