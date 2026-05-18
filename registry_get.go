package recon

import (
	"fmt"
	"time"
)

// Get returns the [Value] resolved for key. The bool reports whether any
// layer of the registry — explicit override, pinned source, registered
// source, alias chain, default — supplied a value; the error wraps a
// source-side failure that surfaced during the last snapshot rebuild.
//
// An empty-string value counts as set: a source returning
// (Value{kind:StringKind, raw:""}, true, nil) is reported as found. Use
// [Value.IsZero] or check [Value.Kind] for "set to null" detection.
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

// GetPath is the [Path]-typed variant of [Get]. Useful when the caller
// already has a Path in hand and wants to avoid the string→Path parse.
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
// ("", false, ErrTypeMismatch) when the resolved kind is not StringKind.
// Use the generic [Get] / [Bind] for richer coercion.
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

// GetInt returns the value at key as int. Wraps int64 → int with no
// overflow check (Go converts via truncation on 32-bit platforms — callers
// on those targets should prefer [Registry.GetInt64]).
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

// GetDuration returns the value at key as time.Duration. Accepts a native
// time.Duration value or a parsable string (via time.ParseDuration).
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

// GetTime returns the value at key as time.Time. Accepts a native time.Time
// value or an RFC 3339 string.
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

// GetStringSlice returns the value at key as []string. The wire value must
// be SliceKind; each element is read via [Value.AsString], so a mixed-type
// slice will fail with the first non-string element's error.
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

// GetStringMap returns the value at key as map[string]string. The wire
// value must be MapKind; each map entry is read via [Value.AsString].
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

// GetAny returns the underlying Go value at key (the same shape
// [Value.Any] returns). Use this when the caller is happy with a type
// assertion at the call site and doesn't want the typed accessor wrappers.
func (r *Registry) GetAny(key string) (any, bool, error) {
	v, ok, err := r.Get(key)
	if err != nil || !ok {
		return nil, ok, err
	}
	return v.Any(), true, nil
}

// IsSet reports whether any layer of the registry has a value for key. An
// empty-string value still reports true (matching the [Source] contract).
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

// AllKeys returns every key the registry knows about (canonical paths plus
// alias paths, sorted). For a sub view, only keys under the sub's prefix
// are returned, with the prefix stripped. Useful for tab-completion and
// "config show" output.
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

// Get is the generic typed accessor. It dispatches by T's concrete type to
// the underlying As* accessor on [Value]; types not handled return
// (zero, false, ErrTypeMismatch).
//
// Compile-time type safety: callers write recon.Get[int](r, "port") and
// the compiler enforces the call-site type. The dispatch is a small switch
// — see [Value]'s As* methods for the per-kind coercion rules.
//
// Supported T: string, bool, int, int64, float64, time.Time,
// time.Duration, []string, map[string]string, [Value]. Everything
// else returns ErrTypeMismatch; callers wanting struct-field
// binding should use [Registry.Bind].
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

// MustGet panics on error or not-set. Useful in main() when a missing key
// is a programmer error rather than a runtime condition.
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

// coerceValueTo is the typed entry point of the type-erased
// dispatch pair: it forwards to [coerceValueAny] (which switches on
// T via a zero-value pointer) and type-asserts the returned `any`
// back into T. Adding a new supported T is a matter of editing the
// switch in [coerceValueAny].
//
// Returns the zero T and an error when the value cannot be coerced;
// an unhandled T returns a wrapped [ErrTypeMismatch].
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

// coerceValueAny is the type-erased half of [coerceValueTo]. The
// zero pointer carries T's concrete type so the switch can dispatch
// by type assertion; the return is `any` to let the caller perform
// the final T type-assertion in one place.
//
// Returns (any, nil) on success; (nil, err) on coercion failure.
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
