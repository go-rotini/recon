package recon

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	rotinifs "github.com/go-rotini/fs"
)

// readFromFile is the file-reading indirection the
// [bindWalker.applyFromFile] helper uses. Defaults to
// [rotinifs.ReadFile] so the `fromFile` tag observes the same size
// caps and error semantics as [FileSource] reads; tests that want
// to drive the codepath without touching disk swap this var.
var readFromFile = rotinifs.ReadFile

// bindWalker carries the state a single Bind call needs as it
// descends into nested structs: the source registry, the merged
// decode options, the accumulator for per-field errors, and the set
// of registry paths the walker consulted (for strict-mode
// unknown-key detection).
type bindWalker struct {
	registry  *Registry
	opts      decodeOptions
	errs      *MultiError
	consulted map[string]struct{}
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
		//
		// A struct field tagged `format=<codec>` is intentionally
		// NOT walked — it's a leaf that consumes a single string
		// value (the encoded blob) and decodes it through the
		// codec into the destination struct via
		// [coerceStructFromMap]. Recursing here would look up
		// non-existent sub-paths instead of consulting the field's
		// own resolved string.
		if tag.Format == "" && isWalkableStructValue(fv) {
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

// bindLeaf resolves the field's key, applies the value-transform
// tags (`fromFile`, `expand`, `format=`), runs the result through
// coerce, and writes into fv. Tag side effects on the registry:
//
//   - `secret` propagates to MarkSecret so Describe / Save / errors
//     redact.
//   - `immutable` baselines the resolved value so subsequent
//     snapshot candidates that change it are rejected.
//   - `deprecated` queues a [DeprecationWarning] for the next watch
//     event or [Registry.DrainWarnings] call.
//   - `unset` clears the explicit-layer value (when present) AFTER
//     a successful bind, so one-shot secrets aren't readable twice.
func (w *bindWalker) bindLeaf(fv reflect.Value, prefix Path, sf reflect.StructField, tag FieldTag) {
	path := w.pathFor(prefix, sf, tag)
	w.consulted[path.String()] = struct{}{}
	for _, alias := range tag.Aliases {
		w.consulted[ParsePath(alias).String()] = struct{}{}
	}
	if tag.Secret {
		w.registry.MarkSecret(path.String())
	}
	value, found := w.lookup(path, tag)
	if tag.Immutable && found {
		w.registry.markImmutable(path, value)
	}
	if found && tag.Deprecated {
		w.queueDeprecation(path, value.Source(), tag)
	}

	// Custom decoder dispatch wins outright when one matches the field
	// type — it's the "I'll handle every coercion concern myself" hook.
	if dec, ok := w.opts.customDecoders[fv.Type().String()]; ok && found {
		out, err := dec(value)
		if err != nil {
			w.appendErr(&CoercionError{
				Path: path, Source: value.Source(),
				WireType: value.Kind().String(),
				Target:   fv.Type().String(), Cause: err, Secret: tag.Secret,
			})
			return
		}
		if err := assignCustomDecoded(fv, out); err != nil {
			w.appendErr(&CoercionError{
				Path: path, Target: fv.Type().String(), Cause: err,
				Secret: tag.Secret,
			})
			return
		}
		w.applyPostBind(path, tag)
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

	// Value-transform pipeline. Each transform runs only when its
	// tag option is set; the order is fromFile → expand → format=
	// so a file's contents can carry ${VAR} references, an env
	// var can hold a path whose file contents are then re-decoded,
	// and so on.
	if tag.FromFile {
		next, err := w.applyFromFile(value)
		if err != nil {
			w.appendErr(&CoercionError{
				Path: path, Source: value.Source(),
				WireType: value.Kind().String(),
				Target:   fv.Type().String(), Cause: err, Secret: tag.Secret,
			})
			return
		}
		value = next
	}
	if tag.Expand {
		next, err := w.applyExpand(value)
		if err != nil {
			w.appendErr(&CoercionError{
				Path: path, Source: value.Source(),
				WireType: value.Kind().String(),
				Target:   fv.Type().String(), Cause: err, Secret: tag.Secret,
			})
			return
		}
		value = next
	}
	if tag.Format != "" {
		next, err := w.applyFormatDecode(value, tag.Format)
		if err != nil {
			w.appendErr(&CoercionError{
				Path: path, Source: value.Source(),
				WireType: value.Kind().String(),
				Target:   fv.Type().String(), Cause: err, Secret: tag.Secret,
			})
			return
		}
		value = next
	}

	if tag.NotEmpty {
		s, asErr := valueAsString(value)
		if asErr != nil {
			w.appendErr(&CoercionError{
				Path: path, Source: value.Source(),
				WireType: value.Kind().String(),
				Target:   fv.Type().String(), Cause: asErr, Secret: tag.Secret,
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
			Target:   fv.Type().String(), Cause: err, Secret: tag.Secret,
		})
		return
	}
	w.applyPostBind(path, tag)
}

// emitUnknownKeyErrors appends one [*UnknownKeyError] to the walker's
// accumulator for every snapshot key that wasn't consulted by the
// bind target. Strict-mode opt-in.
//
// Scope: the registry's prefix limits which keys are considered. A
// Sub(prefix).Bind only complains about keys under prefix; root-
// registry Bind covers the whole snapshot.
//
// Provenance: the source name comes from the snapshot's per-key
// chain — the highest-precedence contributor's name lands in
// UnknownKeyError.Source. Alias keys are skipped (they were
// already consumed by their canonical's Bind).
func (w *bindWalker) emitUnknownKeyErrors() {
	snap := w.registry.state.snapshot.Load()
	if snap == nil {
		return
	}
	prefix := w.registry.prefix
	for _, p := range snap.keys {
		ks := p.String()
		if _, isAlias := snap.aliases[ks]; isAlias {
			continue
		}
		if len(prefix) > 0 && !p.HasPrefix(prefix) {
			continue
		}
		if _, ok := w.consulted[ks]; ok {
			continue
		}
		src := ""
		if srcs := snap.sources[ks]; len(srcs) > 0 {
			src = srcs[0]
		}
		w.appendErr(&UnknownKeyError{Path: p, Source: src})
	}
}

// queueDeprecation pushes a [DeprecationWarning] onto the registry's
// pending-warning queue. Used by [bindLeaf] when a tagged field has
// `deprecated` set and a source actually supplied a value (deprecated
// fields that fall through to default / required handling do NOT
// generate warnings — the deprecation only matters when the
// deprecated key was actively consulted).
func (w *bindWalker) queueDeprecation(path Path, source string, tag FieldTag) {
	msg := tag.DeprecationMessage
	if msg == "" {
		msg = "recon: key " + path.String() + " is deprecated"
	}
	w.registry.queueWarning(DeprecationWarning{
		Path: path, Source: source, Message: msg,
	})
}

// applyFromFile reads the file whose path is the resolved value's
// string form and returns a new Value carrying the file's contents.
// Used by [bindLeaf] when the field's `fromFile` tag is set.
//
// Trailing newlines are NOT stripped — secrets stored in
// /run/secrets/... typically carry exactly the bytes the operator
// committed; trimming would change the secret's value.
func (w *bindWalker) applyFromFile(v Value) (Value, error) {
	pathStr, err := valueAsString(v)
	if err != nil {
		return Value{}, fmt.Errorf("recon: fromFile: path projection: %w", err)
	}
	if pathStr == "" {
		return Value{}, fmt.Errorf("%w: fromFile target path is empty", ErrInvalidPath)
	}
	data, err := readFromFile(pathStr)
	if err != nil {
		return Value{}, fmt.Errorf("recon: fromFile %q: %w", pathStr, err)
	}
	return NewValue(string(data)), nil
}

// applyExpand substitutes ${other.key} references in the resolved
// value's string form. Non-string-projectable values pass through
// untouched — `expand` on a non-string field is a no-op rather than
// an error, matching the "best-effort apply tag option" pattern.
func (w *bindWalker) applyExpand(v Value) (Value, error) {
	s, asErr := valueAsString(v)
	if asErr != nil {
		// Intentional: a non-string-projectable value (slice, map,
		// nested struct) cannot carry ${ref} markers, so `expand` is
		// a tag-options no-op for that field — return the input value
		// unchanged rather than failing the whole bind.
		return v, nil //nolint:nilerr // documented best-effort no-op
	}
	expanded, err := expandValueRefs(s, w.registry)
	if err != nil {
		return Value{}, err
	}
	return NewValue(expanded), nil
}

// applyFormatDecode runs the resolved value's string form through
// the registry's codec set, returning a new Value carrying the
// decoded shape. Used by `format=<codec>` to handle "this string
// holds a JSON / YAML / TOML / etc. blob" patterns.
func (w *bindWalker) applyFormatDecode(v Value, format string) (Value, error) {
	codecs := w.registry.state.opts.codecs
	if codecs == nil {
		codecs = DefaultCodecs()
	}
	c, ok := codecs.ByName(format)
	if !ok {
		return Value{}, fmt.Errorf("%w: no codec registered for format %q",
			ErrUnsupportedFormat, format)
	}
	s, err := valueAsString(v)
	if err != nil {
		return Value{}, fmt.Errorf("recon: format=%s: %w", format, err)
	}
	decoded, err := c.Decode([]byte(s))
	if err != nil {
		return Value{}, fmt.Errorf("recon: format=%s decode: %w", format, err)
	}
	return NewValue(decoded), nil
}

// applyPostBind handles tag side effects that should run only AFTER
// the field has been successfully decoded and assigned. Currently
// just `unset`, which clears the explicit-layer value to support
// the "one-shot secret from env" pattern.
//
// A best-effort Unset error is logged via the registry's logger
// rather than surfaced — the bind itself succeeded, so a follow-up
// rebuild rejection on the Unset is a diagnostic concern, not a
// bind failure.
func (w *bindWalker) applyPostBind(path Path, tag FieldTag) {
	if tag.Unset {
		if err := w.registry.Unset(path.String()); err != nil {
			w.registry.state.logger.Warn("recon: unset post-bind failed",
				"path", path.String(), "err", err)
		}
	}
}

// lookup resolves a path through the registry's snapshot. When the
// tag pins a specific source (via `source=<name>`), the value MUST
// come from that source — a hit from another source is reported as
// "not found" so the field falls through to default / required logic.
func (w *bindWalker) lookup(path Path, tag FieldTag) (Value, bool) {
	// path is already the absolute canonical path — the walker built
	// it from the registry's prefix plus the per-field segments.
	// Look up directly against the snapshot so the registry's
	// own prefix-prepending GetPath wrapper doesn't double the
	// sub-view prefix.
	v, ok := w.lookupSnapshot(path)
	if !ok {
		for _, alias := range tag.Aliases {
			av, aok := w.lookupSnapshot(ParsePath(alias))
			if aok {
				return av, true
			}
		}
		return v, false
	}
	if tag.Source != "" && v.Source() != tag.Source {
		return Value{}, false
	}
	return v, true
}

// lookupSnapshot reads the current snapshot directly, bypassing the
// registry's prefix-prepending [Registry.Get] / [Registry.GetPath]
// wrappers. The bind walker passes absolute paths (already
// prepended with the registry's prefix) so consulting GetPath
// would double the prefix on Sub-view binds.
func (w *bindWalker) lookupSnapshot(path Path) (Value, bool) {
	snap := w.registry.state.snapshot.Load()
	if snap == nil {
		return Value{}, false
	}
	return snap.Get(path)
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
// case (a common config-file → struct path).
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
	// No tag at all: synthesize a tag from the Go field name in
	// snake_case form. "ServerPort" → "server_port" — matches the
	// shape file-format keys conventionally take.
	return FieldTag{Name: toSnakeCase(sf.Name)}
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
		if isTimeTime(probe.Elem().Type()) {
			return false
		}
		return true
	}
	if fv.Kind() != reflect.Struct {
		return false
	}
	if isTimeTime(fv.Type()) {
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
