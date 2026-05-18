package recon

import (
	"fmt"
	"time"
)

// Get returns the [Value] resolved for key. The bool reports whether
// any layer of the registry supplied a value; an empty string counts
// as set. The error is non-nil only when the registry is closed.
func (r *Registry) Get(key string) (Value, bool, error) {
	if err := r.validateNotClosed(); err != nil {
		return Value{}, false, err
	}
	snap := r.state.snapshot.Load()
	if snap == nil {
		return Value{}, false, nil
	}
	v, ok := snap.Get(ParsePath(r.fullKey(key)))
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
	v, ok, err := r.Get(key)
	if err != nil || !ok {
		return "", ok, err
	}
	s, asErr := v.AsString()
	if asErr != nil {
		return "", true, asErr
	}
	return s, true, nil
}

// GetInt returns the value at key as int. Wraps int64 → int without
// overflow check; 32-bit-target callers should prefer [GetInt64].
func (r *Registry) GetInt(key string) (int, bool, error) {
	i64, ok, err := r.GetInt64(key)
	return int(i64), ok, err
}

// GetInt64 returns the value at key as int64.
func (r *Registry) GetInt64(key string) (int64, bool, error) {
	v, ok, err := r.Get(key)
	if err != nil || !ok {
		return 0, ok, err
	}
	i, asErr := v.AsInt64()
	if asErr != nil {
		return 0, true, asErr
	}
	return i, true, nil
}

// GetFloat returns the value at key as float64.
func (r *Registry) GetFloat(key string) (float64, bool, error) {
	v, ok, err := r.Get(key)
	if err != nil || !ok {
		return 0, ok, err
	}
	f, asErr := v.AsFloat64()
	if asErr != nil {
		return 0, true, asErr
	}
	return f, true, nil
}

// GetBool returns the value at key as bool.
func (r *Registry) GetBool(key string) (bool, bool, error) {
	v, ok, err := r.Get(key)
	if err != nil || !ok {
		return false, ok, err
	}
	b, asErr := v.AsBool()
	if asErr != nil {
		return false, true, asErr
	}
	return b, true, nil
}

// GetDuration returns the value at key as time.Duration. A native
// time.Duration value or a string parseable by time.ParseDuration is
// accepted.
func (r *Registry) GetDuration(key string) (time.Duration, bool, error) {
	v, ok, err := r.Get(key)
	if err != nil || !ok {
		return 0, ok, err
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
	v, ok, err := r.Get(key)
	if err != nil || !ok {
		return time.Time{}, ok, err
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
	v, ok, err := r.Get(key)
	if err != nil || !ok {
		return nil, ok, err
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
	v, ok, err := r.Get(key)
	if err != nil || !ok {
		return nil, ok, err
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

// IsSet reports whether any layer of the registry has a value for key.
// An empty string still counts as set.
func (r *Registry) IsSet(key string) bool {
	if r.validateNotClosed() != nil {
		return false
	}
	snap := r.state.snapshot.Load()
	if snap == nil {
		return false
	}
	_, ok := snap.Get(ParsePath(r.fullKey(key)))
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
// int64, float64, time.Time, time.Duration, []string, [Value].
// Unsupported types return wrapped [ErrTypeMismatch]; callers wanting
// struct binding should use [Registry.Bind].
func Get[T any](r *Registry, key string) (T, bool, error) {
	var zero T
	v, ok, err := r.Get(key)
	if err != nil || !ok {
		return zero, ok, err
	}
	out, cErr := coerceValueTo[T](v)
	if cErr != nil {
		return zero, true, cErr
	}
	return out, true, nil
}

// MustGet panics on error or not-set. Useful in main() when a missing
// key is a programmer error.
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
