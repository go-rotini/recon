package recon

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Get returns the [Value] resolved for key. The bool reports whether
// any layer of the registry supplied a value; an empty string counts
// as set. For a key that only has keys below it, such as a map section
// of a configuration file, Get returns a [MapKind] value built from
// them (see [Snapshot.Get]). The error is non-nil only when the
// registry is closed.
//
// The typed getters (GetString, GetInt and so on, and [Get]) read the
// value set at key itself, and return the type's zero value, true and
// no error for a key set to null. GetStringMap reads key as a map: a
// null or scalar at key yields to keys below it set by a layer of
// higher precedence, or by the same layer.
func (r *Registry) Get(key string) (Value, bool, error) {
	return r.get(key, readGet)
}

// get is [Registry.Get] with the read mode mode.
func (r *Registry) get(key string, mode readMode) (Value, bool, error) {
	if err := r.validateNotClosed(); err != nil {
		return Value{}, false, err
	}
	snap := r.state.snapshot.Load()
	if snap == nil {
		return Value{}, false, nil
	}
	v, ok := r.lookupKey(snap, key, mode)
	return v, ok, nil
}

// GetPath is the [Path]-typed variant of [Registry.Get].
func (r *Registry) GetPath(p Path) (Value, bool, error) {
	if err := r.validateNotClosed(); err != nil {
		return Value{}, false, err
	}
	snap := r.state.snapshot.Load()
	if snap == nil {
		return Value{}, false, nil
	}
	v, ok := snap.Get(r.fullPath(p))
	return v, ok, nil
}

// GetString returns the string value at key. Returns
// [ErrTypeMismatch] when the resolved kind is not [StringKind].
func (r *Registry) GetString(key string) (string, bool, error) {
	v, ok, err := r.get(key, readExact)
	if err != nil || !ok {
		return "", ok, err
	}
	if v.Kind() == NullKind {
		return "", true, nil
	}
	s, asErr := v.AsString()
	if asErr != nil {
		return "", true, asErr
	}
	return s, true, nil
}

// GetInt returns the value at key as int, converting from int64
// without an overflow check; on 32-bit targets prefer
// [Registry.GetInt64].
func (r *Registry) GetInt(key string) (int, bool, error) {
	i64, ok, err := r.GetInt64(key)
	return int(i64), ok, err
}

// GetInt64 returns the value at key as int64. The value must be
// [IntKind]: strings are not parsed and floats are not converted (both
// return [ErrTypeMismatch]); use [Registry.Bind] for conversion.
func (r *Registry) GetInt64(key string) (int64, bool, error) {
	v, ok, err := r.get(key, readExact)
	if err != nil || !ok {
		return 0, ok, err
	}
	if v.Kind() == NullKind {
		return 0, true, nil
	}
	i, asErr := v.AsInt64()
	if asErr != nil {
		return 0, true, asErr
	}
	return i, true, nil
}

// GetFloat returns the value at key as float64. [FloatKind] and
// [IntKind] values are accepted; strings are not parsed.
func (r *Registry) GetFloat(key string) (float64, bool, error) {
	v, ok, err := r.get(key, readExact)
	if err != nil || !ok {
		return 0, ok, err
	}
	if v.Kind() == NullKind {
		return 0, true, nil
	}
	f, asErr := v.AsFloat64()
	if asErr != nil {
		return 0, true, asErr
	}
	return f, true, nil
}

// GetBool returns the value at key as bool. The value must be
// [BoolKind]; strings are not parsed.
func (r *Registry) GetBool(key string) (bool, bool, error) {
	v, ok, err := r.get(key, readExact)
	if err != nil || !ok {
		return false, ok, err
	}
	if v.Kind() == NullKind {
		return false, true, nil
	}
	b, asErr := v.AsBool()
	if asErr != nil {
		return false, true, asErr
	}
	return b, true, nil
}

// GetDuration returns the value at key as time.Duration. A native
// time.Duration, a string parseable by [time.ParseDuration], or an
// integer number of nanoseconds is accepted.
func (r *Registry) GetDuration(key string) (time.Duration, bool, error) {
	v, ok, err := r.get(key, readExact)
	if err != nil || !ok {
		return 0, ok, err
	}
	if v.Kind() == NullKind {
		return 0, true, nil
	}
	d, asErr := v.AsDuration()
	if asErr != nil {
		return 0, true, asErr
	}
	return d, true, nil
}

// GetTime returns the value at key as time.Time. A native time.Time
// or an RFC 3339 string is accepted.
func (r *Registry) GetTime(key string) (time.Time, bool, error) {
	v, ok, err := r.get(key, readExact)
	if err != nil || !ok {
		return time.Time{}, ok, err
	}
	if v.Kind() == NullKind {
		return time.Time{}, true, nil
	}
	t, asErr := v.AsTime()
	if asErr != nil {
		return time.Time{}, true, asErr
	}
	return t, true, nil
}

// GetStringSlice returns the value at key as []string. The wire kind
// must be [SliceKind]; each element is projected via [Value.AsString].
func (r *Registry) GetStringSlice(key string) ([]string, bool, error) {
	v, ok, err := r.get(key, readExact)
	if err != nil || !ok {
		return nil, ok, err
	}
	if v.Kind() == NullKind {
		return nil, true, nil
	}
	slice, asErr := v.AsSlice()
	if asErr != nil {
		return nil, true, asErr
	}
	out := make([]string, len(slice))
	for i, el := range slice {
		s, sErr := el.AsString()
		if sErr != nil {
			return nil, true, fmt.Errorf("element %d: %w", i, sErr)
		}
		out[i] = s
	}
	return out, true, nil
}

// GetStringMap returns the value at key as map[string]string. The
// wire kind must be [MapKind].
func (r *Registry) GetStringMap(key string) (map[string]string, bool, error) {
	v, ok, err := r.get(key, readSection)
	if err != nil || !ok {
		return nil, ok, err
	}
	if v.Kind() == NullKind {
		return nil, true, nil
	}
	m, asErr := v.AsMap()
	if asErr != nil {
		return nil, true, asErr
	}
	out := make(map[string]string, len(m))
	for k, el := range m {
		s, sErr := el.AsString()
		if sErr != nil {
			return nil, true, fmt.Errorf("entry %q: %w", k, sErr)
		}
		out[k] = s
	}
	return out, true, nil
}

// GetAny returns the underlying Go value at key — the same shape
// [Value.Any] returns.
func (r *Registry) GetAny(key string) (any, bool, error) {
	v, ok, err := r.Get(key)
	if err != nil || !ok {
		return nil, ok, err
	}
	return v.Any(), true, nil
}

// IsSet reports whether any layer of the registry has a value for key,
// or for a key below it (a configuration file section counts as set).
// An empty string still counts as set.
func (r *Registry) IsSet(key string) bool {
	if r.validateNotClosed() != nil {
		return false
	}
	snap := r.state.snapshot.Load()
	if snap == nil {
		return false
	}
	_, ok := r.lookupKey(snap, key, readGet)
	return ok
}

// AllKeys returns every known key (canonical and alias) in sorted
// order. On a sub view, only keys under the sub's prefix are returned
// with the prefix stripped.
func (r *Registry) AllKeys() []string {
	if r.validateNotClosed() != nil {
		return nil
	}
	snap := r.state.snapshot.Load()
	if snap == nil {
		return nil
	}
	paths := snap.Keys()
	if len(r.prefix) == 0 {
		out := make([]string, len(paths))
		for i, p := range paths {
			out[i] = p.String()
		}
		return out
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !p.HasPrefix(r.prefix) {
			continue
		}
		out = append(out, p.After(r.prefix).String())
	}
	return out
}

// Get is the generic typed accessor. Supported T: string, bool, int,
// int64, uint, uint64, float64, time.Time, time.Duration, []string, [Value]. T must
// match the value's wire kind as the matching [Value] As* method
// requires: only time.Time and time.Duration are parsed from strings,
// so an environment variable read as int returns [ErrTypeMismatch].
// Unsupported types also return wrapped [ErrTypeMismatch]. Use
// [Registry.Bind] for string conversion and struct binding.
func Get[T any](r *Registry, key string) (T, bool, error) {
	var zero T
	mode := readExact
	if _, isValue := any(zero).(Value); isValue {
		mode = readGet
	}
	v, ok, err := r.get(key, mode)
	if err != nil || !ok {
		return zero, ok, err
	}
	if v.Kind() == NullKind {
		return zero, true, nil
	}
	out, cErr := coerceValueTo[T](v)
	if cErr != nil {
		return zero, true, cErr
	}
	return out, true, nil
}

// MustGet is [Get] that panics on error, or with [ErrKeyNotFound] when
// the key is not set.
func MustGet[T any](r *Registry, key string) T {
	v, ok, err := Get[T](r, key)
	switch {
	case err != nil:
		panic(fmt.Errorf("recon.MustGet[%T](%q): %w", v, key, err))
	case !ok:
		panic(fmt.Errorf("recon.MustGet[%T](%q): %w", v, key, ErrKeyNotFound))
	}
	return v
}

// coerceValueTo is the typed half of the dispatch pair. The
// type-erased [coerceValueAny] handles the switch; this layer
// type-asserts back into T.
func coerceValueTo[T any](v Value) (T, error) {
	var zero T
	out, err := coerceValueAny(v, &zero)
	if err != nil {
		return zero, err
	}
	if out == nil {
		return zero, nil
	}
	typed, ok := out.(T)
	if !ok {
		return zero, fmt.Errorf("%w: cannot coerce %s to %T",
			ErrTypeMismatch, v.Kind(), zero)
	}
	return typed, nil
}

// coerceValueAny dispatches by T using a zero-value pointer. Adding a
// new supported T is a matter of editing this switch.
func coerceValueAny[T any](v Value, zero *T) (any, error) {
	switch any(*zero).(type) {
	case string:
		s, err := v.AsString()
		return s, err
	case bool:
		b, err := v.AsBool()
		return b, err
	case int:
		i, err := v.AsInt64()
		return int(i), err
	case int64:
		i, err := v.AsInt64()
		return i, err
	case uint:
		u, err := valueAsUint64(v)
		if err == nil && u > math.MaxUint {
			return uint(0), fmt.Errorf("%w: %d overflows uint", ErrCoercion, u)
		}
		return uint(u), err
	case uint64:
		return valueAsUint64(v)
	case float64:
		f, err := v.AsFloat64()
		return f, err
	case time.Time:
		t, err := v.AsTime()
		return t, err
	case time.Duration:
		d, err := v.AsDuration()
		return d, err
	case Value:
		return v, nil
	case []string:
		s, err := v.AsSlice()
		if err != nil {
			return nil, err
		}
		out := make([]string, len(s))
		for i, el := range s {
			es, esErr := el.AsString()
			if esErr != nil {
				return nil, fmt.Errorf("element %d: %w", i, esErr)
			}
			out[i] = es
		}
		return out, nil
	}
	return nil, fmt.Errorf("%w: %T not handled by Get[T]", ErrTypeMismatch, *zero)
}

// valueAsUint64 is [Value.AsInt64] for an unsigned result: an integer
// that is not negative, or one above [math.MaxInt64].
func valueAsUint64(v Value) (uint64, error) {
	if v.bigUint {
		u, err := strconv.ParseUint(mustAsString(v), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %w", ErrCoercion, err)
		}
		return u, nil
	}
	i, err := v.AsInt64()
	if err != nil {
		return 0, err
	}
	if i < 0 {
		return 0, fmt.Errorf("%w: %d is negative", ErrCoercion, i)
	}
	return uint64(i), nil
}

// lookupKey resolves key in snap as mode asks. On a root registry a
// key without "[" is already in canonical form and is looked up as
// given.
func (r *Registry) lookupKey(snap *Snapshot, key string, mode readMode) (Value, bool) {
	if len(r.prefix) == 0 && strings.IndexByte(key, '[') < 0 {
		return snap.read(key, nil, mode)
	}
	p := r.fullPath(ParsePath(key))
	return snap.read(p.String(), p, mode)
}
