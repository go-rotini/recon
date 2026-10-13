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
// "1_000" is 1000 (a number the format itself reads, such as YAML
// `port: 010`, arrives as that number: 10); floats use
// [strconv.ParseFloat]; bools accept [strconv.ParseBool]'s forms and
// true/false, yes/no and on/off in any case; durations use
// [time.ParseDuration]. Numbers a format reads as numbers convert too:
// a whole-number float (YAML `450.0`, JSON `1e3`) fills an integer
// field, and the integers 0 and 1 fill a bool field. A string field
// takes a non-string value's [Value.String] form. Slices and maps split
// strings on the `separator=` and `kvSeparator=` tag options.
// A field whose type implements [Unmarshaler], UnmarshalEnv(string)
// error, or [encoding.TextUnmarshaler] decodes itself, in that order.
// A map, `any` or struct field also binds from a configuration file
// section held as leaf keys. An `any` field, or an `any` element of a
// map or slice, takes the value [Value.Any] gives: an integer above the
// int64 range arrives as a uint64, and one beyond the uint64 range as
// a [encoding/json.Number].
//
// Struct elements of slices and maps take their field names from the
// same tags as top-level fields (a lowercased Go name is accepted too)
// and honor these options: the name (a dotted name reads nested
// maps), `aliases=`, `default=`, `required`, `notEmpty`, `fromFile`,
// `expand`, `transform=` and the value-format options. `path=`,
// `source=`, `format=`, `immutable`, `deprecated` and `unset` apply to
// top-level fields only. A slice or map whose element type has a secret
// field is marked secret as a whole, so its other fields are redacted
// too.
//
// A null value sets the field to its zero value, unless the field has a
// `default=`, which fills it; a null does not satisfy `required`. A
// pointer to a struct type already being bound is followed only where
// keys exist below its path, so self-referencing types bind as deep as
// the data goes.
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
	if snap := r.state.snapshot.Load(); snap != nil {
		var missing []bindKey
		w.collectPaths(rv.Type(), r.prefix, snap, &missing)
		r.addBoundPaths(missing)
	}
	w.walk(rv, r.prefix, false)
	r.markSecrets(w.secrets)
	r.recordConsulted(w.consulted)
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
//
// A target that points to something other than a struct whose fields
// are bound (a map, slice, scalar, time.Time, [Secret], or a type that
// decodes itself) receives the value at key itself, converted as a
// bound field of that type would be, custom decoders included; a key no
// layer sets leaves it unchanged. Like a bound field, key is looked up
// in every source, so "max_conns" finds APP_MAX_CONNS; [Registry.Get]
// sees only the keys sources list (max.conns) and the ones a bind read.
func (r *Registry) UnmarshalKey(key string, target any, opts ...DecodeOption) error {
	if rv := reflect.ValueOf(target); key != "" && rv.Kind() == reflect.Pointer && !rv.IsNil() &&
		!isWalkableStructType(rv.Elem().Type()) {
		return r.unmarshalKeyValue(key, rv.Elem(), opts)
	}
	if key == "" {
		return r.Bind(target, opts...)
	}
	return r.Sub(key).Bind(target, opts...)
}

// unmarshalKeyValue is [Registry.UnmarshalKey] for a dest that takes
// the value at key whole.
func (r *Registry) unmarshalKeyValue(key string, dest reflect.Value, opts []DecodeOption) error {
	if err := r.validateNotClosed(); err != nil {
		return err
	}
	cfg := r.buildDecodeOptions(context.Background(), opts)
	if isSecretType(dest.Type()) || holdsSecrets(dest.Type(), cfg.tagName) {
		r.MarkSecret(key)
	}
	full := r.fullPath(ParsePath(key))
	r.resolveBindPaths([]bindKey{{str: full.String(), path: full}})
	v, ok, err := r.get(key, leafReadMode(dest.Type(), FieldTag{}))
	if err != nil || !ok {
		return err
	}
	var cerr error
	if dec, has := cfg.customDecoders[dest.Type()]; has {
		var out any
		if out, cerr = dec(v); cerr == nil {
			cerr = assignCustomDecoded(dest, out)
		}
	} else {
		w := &bindWalker{registry: r, opts: cfg}
		cerr = coerceWith(v, dest, FieldTag{}, w.elemRules())
	}
	if cerr != nil {
		return &CoercionError{
			Path: r.fullPath(ParsePath(key)), Source: v.Source(),
			WireType: v.Kind().String(), Target: dest.Type().String(),
			Cause: cerr, Secret: r.IsSecret(key),
		}
	}
	return nil
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

// resolveBindPaths makes sure the snapshot answers every path a bind is
// about to read. A path no source's Keys reported (an environment
// variable such as APP_MAX_CONNS is enumerated as max.conns, yet
// answers a lookup of max_conns) is remembered, so this and every later
// rebuild resolve it through each source's Get; the snapshot is rebuilt
// at once only when a source has a value for one.
func (r *Registry) resolveBindPaths(keys []bindKey) {
	snap := r.state.snapshot.Load()
	if snap == nil || len(keys) == 0 {
		return
	}
	var missing []bindKey
	for _, k := range keys {
		if !snap.answers(k.str, k.path) {
			missing = append(missing, k)
		}
	}
	r.addBoundPaths(missing)
}

// answers reports whether s has a value at ks, whose path is p or, when
// p is nil, parsed from ks as needed.
func (s *Snapshot) answers(ks string, p Path) bool {
	_, ok := s.lookup(ks, p)
	return ok
}

// addBoundPaths is the rest of [Registry.resolveBindPaths], for the
// paths the snapshot did not answer.
func (r *Registry) addBoundPaths(missing []bindKey) {
	if len(missing) == 0 {
		return
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	var added []Path
	var addedKeys []string
	for _, k := range missing {
		if _, ok := r.state.bound[k.str]; !ok {
			added = append(added, k.pathOf())
			addedKeys = append(addedKeys, k.str)
		}
	}
	if len(added) == 0 {
		return
	}
	prev := r.state.bound
	next := make(map[string]struct{}, len(prev)+len(added))
	for k := range prev {
		next[k] = struct{}{}
	}
	for _, k := range addedKeys {
		next[k] = struct{}{}
	}
	r.state.bound = next
	if !r.anySourceHasLocked(added) {
		return
	}
	if err := r.rebuildSnapshotLocked(); err != nil {
		r.state.bound = prev
		r.state.logger.Warn("recon: bind rebuild rejected", "err", err)
	}
}

// anySourceHasLocked reports whether a registered source has a value
// for one of paths. Caller must hold r.state.mu.
func (r *Registry) anySourceHasLocked(paths []Path) bool {
	for _, s := range r.state.sources {
		for _, p := range paths {
			if _, ok, err := s.Get(p); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// recordConsulted adds the paths a bind read to the registry's record.
func (r *Registry) recordConsulted(paths map[string]struct{}) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if r.state.consulted == nil {
		r.state.consulted = make(map[string]struct{}, len(paths))
	}
	for k := range paths {
		r.state.consulted[k] = struct{}{}
	}
}
