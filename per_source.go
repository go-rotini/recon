package recon

import (
	"fmt"
	"maps"
)

// ValueSource is one source's typed contribution to one key. IsSet
// reports whether the source had a value (mirroring [Source.Get]'s
// ok return); Err carries any coercion failure so callers can
// distinguish "didn't have the key" from "wrong shape".
type ValueSource[T any] struct {
	Source string
	Value  T
	IsSet  bool
	Err    error
}

// PerSource is the per-source view of one key across the registry's
// entire source chain. Use it when the default precedence isn't what
// the caller wants and they need to apply their own resolve-by-policy
// logic ("env wins in containers", "config-first for daemons").
//
// Sources is ordered to match the registry's chain (first = highest
// precedence). Explicit and Default model the reserved layers above
// and below the chain. Resolved is what [Get] would return.
type PerSource[T any] struct {
	// Path is the canonical key queried.
	Path Path

	// Sources lists every registered source's contribution in
	// precedence order.
	Sources []ValueSource[T]

	// Explicit is the [Registry.Set] override, IsSet=false when none.
	Explicit ValueSource[T]

	// Default is the [Registry.SetDefault] fallback, IsSet=false
	// when none.
	Default ValueSource[T]

	// Resolved is what [Get] would have returned.
	Resolved ValueSource[T]
}

// BySource returns the entry contributed by name, or a zero entry
// with IsSet=false when no source by that name has a value.
func (p PerSource[T]) BySource(name string) ValueSource[T] {
	for _, e := range p.Sources {
		if e.Source == name {
			return e
		}
	}
	return ValueSource[T]{Source: name}
}

// PerSourceFor returns the per-source view of key. Every source is
// consulted once and the result is coerced into T using the same
// rules [Get] follows.
//
// A source without the key reports IsSet=false. A source whose value
// cannot be coerced into T reports IsSet=true with Err set — caller-
// side resolve logic can then distinguish "missing" from "wrong
// shape".
//
// Returns a wrapped [ErrRegistryClosed] on a closed registry.
func PerSourceFor[T any](r *Registry, key string) (PerSource[T], error) {
	if r == nil {
		return PerSource[T]{}, fmt.Errorf("%w: PerSourceFor: nil *Registry", ErrInvalidPath)
	}
	if err := r.validateNotClosed(); err != nil {
		return PerSource[T]{}, err
	}
	fullKey := r.fullKey(key)
	path := ParsePath(fullKey)
	canon := resolveAliasChain(fullKey, r.snapshotAliases())

	out := PerSource[T]{Path: ParsePath(canon)}

	r.state.mu.Lock()
	sources := append([]Source(nil), r.state.sources...)
	explicitRaw, hasExplicit := r.state.explicits[canon]
	defaultRaw, hasDefault := r.state.defaults[canon]
	r.state.mu.Unlock()

	out.Sources = make([]ValueSource[T], 0, len(sources))
	for _, src := range sources {
		entry := ValueSource[T]{Source: src.Name()}
		raw, ok, err := src.Get(path)
		if err != nil {
			entry.IsSet = true
			entry.Err = err
			out.Sources = append(out.Sources, entry)
			continue
		}
		if !ok {
			out.Sources = append(out.Sources, entry)
			continue
		}
		entry.IsSet = true
		entry.Value, entry.Err = coerceValueTo[T](raw)
		out.Sources = append(out.Sources, entry)
	}

	if hasExplicit {
		v := NewValue(explicitRaw).withSource(srcExplicit)
		val, err := coerceValueTo[T](v)
		out.Explicit = ValueSource[T]{
			Source: srcExplicit, IsSet: true,
			Value: val, Err: err,
		}
	}
	if hasDefault {
		v := NewValue(defaultRaw).withSource(srcDefault)
		val, err := coerceValueTo[T](v)
		out.Default = ValueSource[T]{
			Source: srcDefault, IsSet: true,
			Value: val, Err: err,
		}
	}

	if val, ok, err := Get[T](r, key); err != nil {
		out.Resolved = ValueSource[T]{IsSet: ok, Value: val, Err: err}
	} else if ok {
		// Attribute the winner from the snapshot's source chain.
		if snap := r.state.snapshot.Load(); snap != nil {
			if srcs := snap.SourceFor(out.Path); len(srcs) > 0 {
				out.Resolved = ValueSource[T]{
					Source: srcs[0], IsSet: true, Value: val,
				}
			} else {
				out.Resolved = ValueSource[T]{IsSet: true, Value: val}
			}
		} else {
			out.Resolved = ValueSource[T]{IsSet: true, Value: val}
		}
	}

	return out, nil
}

// PerSourceForPath is the explicit-path twin of [PerSourceFor].
func PerSourceForPath[T any](r *Registry, p Path) (PerSource[T], error) {
	return PerSourceFor[T](r, p.String())
}

// snapshotAliases returns a copy of the alias map under lock.
func (r *Registry) snapshotAliases() map[string]string {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	out := make(map[string]string, len(r.state.aliases))
	maps.Copy(out, r.state.aliases)
	return out
}
