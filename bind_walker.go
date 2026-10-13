package recon

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// readFromFile is the indirection used by `fromFile`-tagged field
// reads. Defaults to [readFileCapped] so [FileSource]'s size cap
// applies; tests override the var to avoid touching disk.
var readFromFile = readFileCapped

// bindWalker carries the state one [Registry.Bind] call needs as it
// descends into nested structs. consulted is the set of paths the
// walker resolved; strict mode reports every snapshot key not in it or
// below a map value the walker bound (consultedTrees).
type bindWalker struct {
	registry       *Registry
	opts           decodeOptions
	errs           *MultiError
	consulted      map[string]struct{}
	consultedTrees []string

	// stack lists the struct types on the current walk path. A pointer
	// to a type already on it is followed only when keys exist below
	// its path, so self-referencing types stop where the data does.
	stack []reflect.Type

	// secrets collects the paths the walk found secret; Bind marks them
	// all at the end.
	secrets []string

	rules elemRules // built on first use by elemRules
}

// markSecret records p for marking secret when the walk ends.
func (w *bindWalker) markSecret(p string) {
	w.secrets = append(w.secrets, p)
}

// elemRules returns the rules struct elements of slices and maps bind
// with in this walk.
func (w *bindWalker) elemRules() elemRules {
	if w.rules.expand == nil {
		w.rules = elemRules{
			tagName: w.opts.tagName,
			expand:  func(s string) (string, error) { return expandValueRefs(s, w.registry) },
		}
	}
	return w.rules
}

// root returns a view of the walker's registry with no Sub prefix. The
// walker's paths already include the view's prefix, so key-taking
// registry methods must run on the root to avoid prefixing twice.
func (w *bindWalker) root() *Registry {
	if len(w.registry.prefix) == 0 {
		return w.registry
	}
	return &Registry{state: w.registry.state}
}

// walk descends one struct level. prefix is the parent's path; the
// walker appends each field's tag-derived segments under it. secret is
// true inside a struct field tagged `secret`.
func (w *bindWalker) walk(rv reflect.Value, prefix Path, secret bool) {
	t := rv.Type()
	w.stack = append(w.stack, t)
	defer func() { w.stack = w.stack[:len(w.stack)-1] }()
	plans := w.plan(t)
	for i := range plans {
		fp := &plans[i]
		fv := rv.Field(fp.index)
		if fp.walkable {
			w.walkNested(fv, prefix, fp, secret)
		} else {
			w.bindLeaf(fv, prefix, fp, secret)
		}
		if w.shouldShortCircuit() {
			return
		}
	}
}

// fieldPlan is the part of binding one field that depends only on the
// struct type and the tag name.
type fieldPlan struct {
	index     int
	anonymous bool
	tag       FieldTag
	segs      []string // the field's own path segments
	absPath   Path     // the `path=` option, parsed
	aliases   []Path   // the `aliases=` option, parsed
	secret    bool     // the field's type is or holds a [Secret] or secret field
	// walkable is true for a struct field recursed into rather than
	// bound as a leaf. A `format=` struct field is a leaf: its value is
	// a blob the codec decodes into the struct.
	walkable bool
}

type planKey struct {
	t       reflect.Type
	tagName string
}

var planCache sync.Map // planKey → []fieldPlan

// plan returns the field plans for the exported, not skipped fields of
// struct type t.
func (w *bindWalker) plan(t reflect.Type) []fieldPlan {
	key := planKey{t, w.opts.tagName}
	if p, ok := planCache.Load(key); ok {
		plans, _ := p.([]fieldPlan)
		return plans
	}
	var out []fieldPlan
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		tag := w.tagFor(sf)
		if tag.Skip {
			continue
		}
		fp := fieldPlan{
			index:     i,
			anonymous: sf.Anonymous,
			tag:       tag,
			segs:      w.segmentsFor(sf, tag),
			secret:    isSecretType(sf.Type) || holdsSecrets(sf.Type, w.opts.tagName),
			walkable:  tag.Format == "" && isWalkableStructType(sf.Type),
		}
		if tag.Path != "" {
			fp.absPath = ParsePath(tag.Path)
		}
		for _, a := range tag.Aliases {
			fp.aliases = append(fp.aliases, ParsePath(a))
		}
		out = append(out, fp)
	}
	planCache.Store(key, out)
	return out
}

// nestedPath is the path a walkable field's own fields bind under.
// `inline` and unnamed embedded fields add no segment.
func (fp *fieldPlan) nestedPath(prefix Path) Path {
	if fp.tag.Inline || (fp.anonymous && fp.tag.Name == "") {
		return prefix
	}
	return prefix.Append(fp.segs...)
}

// leafPath is the path a leaf field binds at: the `path=` option, else
// the field's segments under prefix (tag.Name transformed, or the
// snake_case Go name; a dotted tag.Name gives several segments).
func (fp *fieldPlan) leafPath(prefix Path) Path {
	if fp.absPath != nil {
		return fp.absPath.Clone()
	}
	return prefix.Append(fp.segs...)
}

// collectPaths appends to out every path (and `aliases=` path) the walk
// of struct type t under prefix would look up, without binding
// anything.
func (w *bindWalker) collectPaths(t reflect.Type, prefix Path, out *[]Path) {
	w.stack = append(w.stack, t)
	defer func() { w.stack = w.stack[:len(w.stack)-1] }()
	plans := w.plan(t)
	for i := range plans {
		fp := &plans[i]
		if fp.walkable {
			st := t.Field(fp.index).Type
			nested := fp.nestedPath(prefix)
			if st.Kind() == reflect.Pointer {
				st = st.Elem()
				if slices.Contains(w.stack, st) && (len(nested) == len(prefix) || !w.hasKeysUnder(nested)) {
					continue
				}
			}
			w.collectPaths(st, nested, out)
			continue
		}
		*out = append(*out, fp.leafPath(prefix))
		*out = append(*out, fp.aliases...)
	}
}

// walkNested recurses into a struct or *struct field, allocating the
// pointee if needed. `inline` and anonymous embedded fields recurse
// under the parent's prefix; otherwise the field's segments are
// appended. A pointer to a struct type already being walked is left as
// it is unless keys exist under the field's path, and with `omitempty`
// a nil pointer stays nil when no key exists under the field's path.
func (w *bindWalker) walkNested(fv reflect.Value, prefix Path, fp *fieldPlan, secret bool) {
	tag := fp.tag
	nested := fp.nestedPath(prefix)
	if fv.Kind() == reflect.Pointer {
		if slices.Contains(w.stack, fv.Type().Elem()) && (len(nested) == len(prefix) || !w.hasKeysUnder(nested)) {
			return
		}
		if fv.IsNil() {
			if tag.OmitEmpty && !w.hasKeysUnder(nested) {
				return
			}
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		fv = fv.Elem()
	}
	if tag.Secret && len(nested) > len(prefix) {
		w.markSecret(nested.String())
	}
	w.walk(fv, nested, secret || tag.Secret)
}

// hasKeysUnder reports whether the snapshot has a value at p or below
// it.
func (w *bindWalker) hasKeysUnder(p Path) bool {
	snap := w.registry.state.snapshot.Load()
	if snap == nil {
		return false
	}
	_, ok := snap.lookup(p.String(), p)
	return ok
}

// bindLeaf resolves the field's path, applies the value-transform tags
// (`fromFile`, `expand`, `format=`), coerces, and assigns. Tag side
// effects on the registry: `secret` (or a [Secret] field type, or a
// secret parent struct) marks; `immutable` baselines; `deprecated`
// queues a [DeprecationWarning]; `unset` clears the explicit-layer
// value after a successful bind.
func (w *bindWalker) bindLeaf(fv reflect.Value, prefix Path, fp *fieldPlan, inherited bool) {
	tag := fp.tag
	path := fp.leafPath(prefix)
	pathStr := path.String()
	w.consulted[pathStr] = struct{}{}
	for _, alias := range fp.aliases {
		w.consulted[alias.String()] = struct{}{}
	}
	secret := tag.Secret || inherited || fp.secret
	if secret {
		w.markSecret(pathStr)
	}
	value, found := w.lookup(path, tag.Source, fp.aliases)
	var nullFrom []string
	if found && tag.Required && value.Kind() == NullKind {
		found, nullFrom = false, []string{value.Source()}
	}
	if found {
		w.noteFound(path, pathStr, value, tag)
	}
	coerceErr := func(cause error) *CoercionError {
		return &CoercionError{
			Path: path, Source: value.Source(),
			WireType: value.Kind().String(),
			Target:   fv.Type().String(), Cause: cause,
			Secret: secret || w.root().IsSecret(pathStr),
		}
	}

	if !found {
		switch {
		case tag.HasDefault:
			value = NewValue(tag.DefaultValue)
		case tag.Required:
			w.appendErr(&MissingRequiredError{Path: path, Sources: nullFrom})
			return
		default:
			return // field stays at its Go zero value
		}
	}

	// A custom decoder takes over the field outright, for a source
	// value and a `default=` value alike.
	if dec, ok := w.opts.customDecoders[fv.Type()]; ok {
		out, err := dec(value)
		if err != nil {
			w.appendErr(coerceErr(err))
			return
		}
		if err := assignCustomDecoded(fv, out); err != nil {
			e := coerceErr(err)
			e.Source, e.WireType = "", ""
			w.appendErr(e)
			return
		}
		w.applyPostBind(path, tag)
		return
	}

	next, err := w.applyTransforms(value, tag)
	if err != nil {
		w.appendErr(coerceErr(err))
		return
	}
	value = next

	if tag.NotEmpty && isEmptyValue(value) {
		w.appendErr(&EmptyValueError{Path: path, Source: value.Source()})
		return
	}

	if err := coerceWith(value, fv, tag, w.elemRules()); err != nil {
		w.appendErr(coerceErr(err))
		return
	}
	w.applyPostBind(path, tag)
}

// noteFound records what a leaf's found value implies: a map value's
// keys count as consulted, `immutable` baselines it, and `deprecated`
// queues a warning.
func (w *bindWalker) noteFound(path Path, pathStr string, value Value, tag FieldTag) {
	if value.Kind() == MapKind {
		w.consultedTrees = append(w.consultedTrees, pathStr)
	}
	if tag.Immutable {
		w.registry.markImmutable(path, value)
	}
	if tag.Deprecated {
		w.queueDeprecation(path, value.Source(), tag)
	}
}

// applyTransforms runs the value-transform pipeline: fromFile → expand
// → format=. The order lets a file's contents carry ${refs}, and an
// env-var-supplied path's contents be re-decoded through a codec. A map
// value (a file section) is already decoded, so format= leaves it
// alone.
func (w *bindWalker) applyTransforms(value Value, tag FieldTag) (Value, error) {
	var err error
	if tag.FromFile {
		if value, err = w.applyFromFile(value); err != nil {
			return Value{}, err
		}
	}
	if tag.Expand {
		if value, err = w.applyExpand(value); err != nil {
			return Value{}, err
		}
	}
	if tag.Format != "" && value.Kind() != MapKind {
		if value, err = w.applyFormatDecode(value, tag.Format); err != nil {
			return Value{}, err
		}
	}
	return value, nil
}

// isEmptyValue reports whether v counts as empty for `notEmpty`: null,
// an empty string, or a list or map with no entries.
func isEmptyValue(v Value) bool {
	switch v.Kind() {
	case NullKind:
		return true
	case SliceKind:
		s, _ := v.raw.([]Value)
		return len(s) == 0
	case MapKind:
		m, _ := v.raw.(map[string]Value)
		return len(m) == 0
	default:
		return v.String() == ""
	}
}

// isSecretType reports whether t is a [Secret], or a pointer, slice,
// array or map whose elements are.
func isSecretType(t reflect.Type) bool {
	for {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			t = t.Elem()
			continue
		}
		break
	}
	return t.PkgPath() == secretTypePkg && strings.HasPrefix(t.Name(), "Secret[")
}

// holdsSecrets reports whether t, or the struct a pointer, slice, array
// or map in t leads to, has a field that is secret: tagged `secret`
// under tagName or its fallbacks, of a [Secret] type, or itself holding
// secrets. Such a value is redacted as a whole, since its elements are
// not bound field by field.
func holdsSecrets(t reflect.Type, tagName string) bool {
	key := secretScanKey{t, tagName}
	if v, ok := secretScanCache.Load(key); ok {
		held, _ := v.(bool)
		return held
	}
	got := scanSecrets(t, tagName, nil)
	secretScanCache.Store(key, got)
	return got
}

type secretScanKey struct {
	t       reflect.Type
	tagName string
}

var secretScanCache sync.Map // secretScanKey → bool

// scanSecrets is [holdsSecrets] without the cache. seen lists the
// struct types on the current path.
func scanSecrets(t reflect.Type, tagName string, seen []reflect.Type) bool {
	for {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			t = t.Elem()
			continue
		}
		break
	}
	if t.Kind() != reflect.Struct || isTimeTime(t) || slices.Contains(seen, t) {
		return false
	}
	seen = append(seen, t)
	for sf := range t.Fields() {
		if !sf.IsExported() {
			continue
		}
		tag, _ := fieldTag(sf, tagName)
		if tag.Skip {
			continue
		}
		if tag.Secret || isSecretType(sf.Type) || scanSecrets(sf.Type, tagName, seen) {
			return true
		}
	}
	return false
}

// secretTypePkg is the import path of the package defining [Secret].
var secretTypePkg = reflect.TypeFor[Secret[string]]().PkgPath()

// emitUnknownKeyErrors appends one [*UnknownKeyError] for every
// snapshot key the bind target did not consult. Scoped to the
// registry's prefix; alias keys, keys below a bound map value, and keys
// a source spells the same way as a consulted key (an environment
// variable read through both APP_MAX_CONNS → max.conns and max_conns)
// are skipped.
func (w *bindWalker) emitUnknownKeyErrors() {
	snap := w.registry.state.snapshot.Load()
	if snap == nil {
		return
	}
	prefix := w.registry.prefix
	spellings := w.consultedSpellings()
	for i, p := range snap.keys {
		ks := snap.keyStrs[i]
		if _, isAlias := snap.aliases[ks]; isAlias {
			continue
		}
		if len(prefix) > 0 && !p.HasPrefix(prefix) {
			continue
		}
		if _, ok := w.consulted[ks]; ok {
			continue
		}
		if w.underConsultedTree(ks) {
			continue
		}
		src := ""
		if srcs := snap.sources[ks]; len(srcs) > 0 {
			src = srcs[0]
		}
		if spellings.has(src, p) {
			continue
		}
		w.appendErr(&UnknownKeyError{Path: p, Source: src})
	}
}

// underConsultedTree reports whether ks lies below a map value the
// walker bound.
func (w *bindWalker) underConsultedTree(ks string) bool {
	for _, t := range w.consultedTrees {
		if strings.HasPrefix(ks, t+DefaultDelimiter) {
			return true
		}
	}
	return false
}

// nativeKeyer is the optional [Source] capability of reporting the
// name a path is stored under, such as an environment variable's.
type nativeKeyer interface {
	nativeKey(p Path) string
}

// keySpellings maps a source name to the native names of the consulted
// paths, for sources that report one.
type keySpellings struct {
	bySource map[string]nativeKeyer
	names    map[string]map[string]struct{}
}

// has reports whether p, as source src spells it, matches a consulted
// path's spelling there.
func (k keySpellings) has(src string, p Path) bool {
	nk, ok := k.bySource[src]
	if !ok {
		return false
	}
	_, hit := k.names[src][nk.nativeKey(p)]
	return hit
}

// consultedSpellings builds [keySpellings] for the registry's sources.
func (w *bindWalker) consultedSpellings() keySpellings {
	out := keySpellings{bySource: map[string]nativeKeyer{}, names: map[string]map[string]struct{}{}}
	w.registry.state.mu.Lock()
	sources := slices.Clone(w.registry.state.sources)
	w.registry.state.mu.Unlock()
	for _, s := range sources {
		nk, ok := s.(nativeKeyer)
		if !ok {
			continue
		}
		names := make(map[string]struct{}, len(w.consulted))
		for ks := range w.consulted {
			names[nk.nativeKey(ParsePath(ks))] = struct{}{}
		}
		out.bySource[s.Name()] = nk
		out.names[s.Name()] = names
	}
	return out
}

// queueDeprecation enqueues a [DeprecationWarning] for path. Called
// only when a source actually supplied a value — falling back to a
// default or hitting required-with-no-value does not emit.
func (w *bindWalker) queueDeprecation(path Path, source string, tag FieldTag) {
	msg := tag.DeprecationMessage
	if msg == "" {
		msg = genericDeprecationMessage(path)
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
	c, err := lookupCodec(w.registry.state.opts.codecs, format)
	if err != nil {
		return Value{}, err
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
		if err := w.root().Unset(path.String()); err != nil {
			w.registry.state.logger.Warn("recon: unset post-bind failed",
				"path", path.String(), "err", err)
		}
	}
}

// lookup resolves path against the snapshot, falling back through
// aliases. When source names a pinned source, a hit at path from any
// other source is rejected so the field falls through to default /
// required logic.
func (w *bindWalker) lookup(path Path, source string, aliases []Path) (Value, bool) {
	v, ok := w.lookupSnapshot(path)
	if !ok {
		for _, alias := range aliases {
			av, aok := w.lookupSnapshot(alias)
			if aok {
				return av, true
			}
		}
		return v, false
	}
	if source != "" && v.Source() != source {
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
	if tag, ok := fieldTag(sf, w.opts.tagName); ok {
		return tag
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

// isWalkableStructType is [isWalkableStructValue] for a field of type t
// in an addressable struct.
func isWalkableStructType(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || isTimeTime(t) {
		return false
	}
	return !implementsUnmarshalHook(reflect.New(t).Interface())
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
