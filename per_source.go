package recon

import (
	"fmt"
	"maps"
)

// PerSourceEntry is one source's contribution to a single key. The
// Source field carries the source's name; Value holds the typed
// projection (post-coercion); IsSet reports whether the source had a
// value for the key (mirroring [Source.Get]'s ok return); Err
// carries any coercion-time failure so callers can distinguish "the
// source didn't have the key" from "the source's value couldn't fit
// into T".
type PerSourceEntry[T any] struct {
	Source string
	Value  T
	IsSet  bool
	Err    error
}

// PerSource is the per-source view of one key across the registry's
// entire source chain. Use it when the precedence chain isn't what
// the caller wants and they need to pick a winner themselves —
// "env-only in containers", "config-first for daemons", or any other
// resolve-by-policy logic.
//
// The slice in Sources is ordered to match the registry's source
// chain (first = highest precedence). Explicit / Default model the
// reserved layers above and below the source chain; Resolved is
// the answer a normal [Get] would return.
type PerSource[T any] struct {
	// Path is the canonical key the snapshot was queried for.
	Path Path

	// Sources lists every registered source's contribution in
	// precedence order. Length matches the registry's source chain
	// at the moment of the [PerSourceFor] call.
	Sources []PerSourceEntry[T]

	// Explicit is the value supplied by [Registry.Set]. IsSet is
	// false when no explicit override exists.
	Explicit PerSourceEntry[T]

	// Default is the value supplied by [Registry.SetDefault]. IsSet
	// is false when no default exists.
	Default PerSourceEntry[T]

	// Resolved is what [Get][T] would have returned — the winner of
	// the precedence walk after explicit, pin, source chain, and
	// default layers were consulted.
	Resolved PerSourceEntry[T]
}

// BySource returns the entry contributed by name, or a zero entry
// with IsSet=false when no source by that name has a value for the
// key. Convenience accessor for callers that already know which
// source they care about.
func (p PerSource[T]) BySource(name string) PerSourceEntry[T] {
	for _, e := range p.Sources {
		if e.Source == name {
			return e
		}
	}
	return PerSourceEntry[T]{Source: name}
}

// PerSourceFor returns the per-source view of key. The registry's
// source chain is walked once; every source's Get is consulted; the
// result is coerced into T per the same rules [Get][T] follows.
//
// A source that does not have the key reports IsSet=false. A source
// whose value cannot be coerced into T reports IsSet=true plus an
// Err describing the coercion failure — the source HAD a value, but
// it wasn't usable as T. This split lets caller-side resolve logic
// distinguish "missing" from "wrong shape".
//
// Returns an error wrapping [ErrRegistryClosed] when the registry
// has been closed.
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

	out.Sources = make([]PerSourceEntry[T], 0, len(sources))
	for _, src := range sources {
		entry := PerSourceEntry[T]{Source: src.Name()}
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
		out.Explicit = PerSourceEntry[T]{
			Source: srcExplicit, IsSet: true,
			Value: val, Err: err,
		}
	}
	if hasDefault {
		v := NewValue(defaultRaw).withSource(srcDefault)
		val, err := coerceValueTo[T](v)
		out.Default = PerSourceEntry[T]{
			Source: srcDefault, IsSet: true,
			Value: val, Err: err,
		}
	}

	if val, ok, err := Get[T](r, key); err != nil {
		out.Resolved = PerSourceEntry[T]{IsSet: ok, Value: val, Err: err}
	} else if ok {
		// Source-name attribution from the snapshot's winner list.
		if snap := r.state.snapshot.Load(); snap != nil {
			if srcs := snap.SourceFor(out.Path); len(srcs) > 0 {
				out.Resolved = PerSourceEntry[T]{
					Source: srcs[0], IsSet: true, Value: val,
				}
			} else {
				out.Resolved = PerSourceEntry[T]{IsSet: true, Value: val}
			}
		} else {
			out.Resolved = PerSourceEntry[T]{IsSet: true, Value: val}
		}
	}

	return out, nil
}

// snapshotAliases returns a copy of the registry's alias map under
// lock. Used by [PerSourceFor] to follow alias chains without
// holding the mutex during the actual per-source enumeration.
func (r *Registry) snapshotAliases() map[string]string {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	out := make(map[string]string, len(r.state.aliases))
	maps.Copy(out, r.state.aliases)
	return out
}
