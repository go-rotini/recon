package recon

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	rotinifs "github.com/go-rotini/fs"
)

// readFromFile is the indirection used by `fromFile`-tagged field
// reads. Defaults to [rotinifs.ReadFile] so [FileSource]'s size caps
// apply; tests override the var to avoid touching disk.
var readFromFile = rotinifs.ReadFile

// bindWalker carries the state one [Registry.Bind] call needs as it
// descends into nested structs. consulted is the set of paths the
// walker resolved; strict mode reports every snapshot key not in it.
type bindWalker struct {
	registry  *Registry
	opts      decodeOptions
	errs      *MultiError
	consulted map[string]struct{}
}

// walk descends one struct level. prefix is the parent's path; the
// walker appends each field's tag-derived segments under it.
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

		// A `format=`-tagged struct field is a leaf: the source value
		// is a string blob the codec decodes into the struct via
		// [coerceStructFromMap]. Recursing would look up non-existent
		// sub-paths instead of consulting the field's own resolved
		// string.
		if tag.Format == "" && isWalkableStructValue(fv) {
			w.walkNested(fv, prefix, sf, tag)
			continue
		}

		w.bindLeaf(fv, prefix, sf, tag)
		if w.shouldShortCircuit() {
			return
		}
	}
}

// walkNested recurses into a struct or *struct field, allocating the
// pointee if needed. `inline` and anonymous embedded fields recurse
// under the parent's prefix; otherwise the field's segments are
// appended.
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

// bindLeaf resolves the field's path, applies the value-transform tags
// (`fromFile`, `expand`, `format=`), coerces, and assigns. Tag side
// effects on the registry: `secret` marks; `immutable` baselines;
// `deprecated` queues a [DeprecationWarning]; `unset` clears the
// explicit-layer value after a successful bind.
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

	// A custom decoder takes over the field outright.
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

	// Value-transform pipeline: fromFile → expand → format=. The order
	// lets a file's contents carry ${refs}, and an env-var-supplied
	// path's contents be re-decoded through a codec.
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

// emitUnknownKeyErrors appends one [*UnknownKeyError] for every
// snapshot key the bind target did not consult. Scoped to the
// registry's prefix; alias keys are skipped.
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

// queueDeprecation enqueues a [DeprecationWarning] for path. Called
// only when a source actually supplied a value — falling back to a
// default or hitting required-with-no-value does not emit.
func (w *bindWalker) queueDeprecation(path Path, source string, tag FieldTag) {
	msg := tag.DeprecationMessage
	if msg == "" {
		msg = "recon: key " + path.String() + " is deprecated"
	}
	w.registry.queueWarning(DeprecationWarning{
		Path: path, Source: source, Message: msg,
	})
}

// applyFromFile reads the file whose path is v's string form and
// returns its contents as a new Value. Trailing newlines are not
// stripped — a Docker / Kubernetes secret typically contains exactly
// the bytes the operator committed.
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

// applyExpand substitutes ${other.key} references in v's string form.
// A non-string-projectable value passes through unchanged: `expand`
// on a non-string field is a no-op rather than an error.
func (w *bindWalker) applyExpand(v Value) (Value, error) {
	s, asErr := valueAsString(v)
	if asErr != nil {
		return v, nil //nolint:nilerr // documented best-effort no-op
	}
	expanded, err := expandValueRefs(s, w.registry)
	if err != nil {
		return Value{}, err
	}
	return NewValue(expanded), nil
}

// applyFormatDecode runs v's string form through the codec named
// format, returning a Value carrying the decoded shape.
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

// applyPostBind runs `unset` after a successful field bind. The Unset
// error is logged but not returned: the bind itself succeeded.
func (w *bindWalker) applyPostBind(path Path, tag FieldTag) {
	if tag.Unset {
		if err := w.registry.Unset(path.String()); err != nil {
			w.registry.state.logger.Warn("recon: unset post-bind failed",
				"path", path.String(), "err", err)
		}
	}
}

// lookup resolves path against the snapshot, falling back through
// tag.Aliases. When the tag pins a source, hits from any other source
// are rejected so the field falls through to default / required logic.
func (w *bindWalker) lookup(path Path, tag FieldTag) (Value, bool) {
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

// lookupSnapshot reads the snapshot directly. The walker passes
// already-prefixed absolute paths, so bypassing [Registry.GetPath]
// avoids double-prefixing on Sub-view binds.
func (w *bindWalker) lookupSnapshot(path Path) (Value, bool) {
	snap := w.registry.state.snapshot.Load()
	if snap == nil {
		return Value{}, false
	}
	return snap.Get(path)
}

// pathFor computes the canonical Path the leaf binder uses. Precedence:
// explicit `path=` > tag.Name (transformed) > Go field name
// (snake-cased). A tag.Name containing the delimiter is parsed as a
// path so `recon:"db.dsn"` works.
func (w *bindWalker) pathFor(prefix Path, sf reflect.StructField, tag FieldTag) Path {
	if tag.Path != "" {
		return ParsePath(tag.Path)
	}
	segments := w.segmentsFor(sf, tag)
	return prefix.Append(segments...)
}

// segmentsFor returns the path segments representing sf. With
// `transform=` set, each segment is re-spelled. A delimited tag.Name
// splits into multiple segments.
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

// applyTransform rewrites segment per the named transform. Unknown
// names return segment unchanged.
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

// toSnakeCase rewrites GoFieldName as go_field_name.
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

// toCamelCase rewrites snake_case as snakeCase.
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

// tagFor extracts the [FieldTag] for sf, consulting the primary tag
// first and falling back through env / json / yaml / toml. Untagged
// fields synthesize a tag from the snake_case Go field name.
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
	return FieldTag{Name: toSnakeCase(sf.Name)}
}

// shouldShortCircuit reports whether the walker should stop after the
// most recent error. Honors [FailFast] vs [FailCollect].
func (w *bindWalker) shouldShortCircuit() bool {
	if w.opts.errorBehavior == nil {
		return false
	}
	if *w.opts.errorBehavior != FailFast {
		return false
	}
	return len(w.errs.Errors) > 0
}

func (w *bindWalker) appendErr(err error) {
	if err == nil {
		return
	}
	w.errs.Append(err)
}

// runValidatorHooks invokes the optional [Validator] /
// [ValidatorContext] interface on the bind target. Hook errors join
// the same MultiError as field errors.
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

// isWalkableStructValue reports whether fv should be recursed into
// rather than coerced. Leaf cases (must not recurse): time.Time, and
// any type implementing [Unmarshaler], UnmarshalEnv, or
// encoding.TextUnmarshaler.
func isWalkableStructValue(fv reflect.Value) bool {
	if fv.Kind() == reflect.Pointer {
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

// implementsUnmarshalHook reports whether v satisfies any of recon's
// leaf-coercion hooks. Used by both [isWalkableStructValue] and
// [tryUnmarshalerHooks] so they agree.
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

// assignCustomDecoded sets fv to the value a [WithCustomDecoder]
// callback returned. The callback's return type must be assignable to
// fv's type.
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
