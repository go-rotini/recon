package recon

// Sub returns a [Registry] view rooted at prefix. Reads, writes, and
// introspection on the returned registry operate on keys *relative* to
// prefix — Sub("server").Get("port") resolves the parent's "server.port",
// Sub("server").Set("port", 9000) writes the parent's "server.port", and
// Sub("server").AllKeys() lists every "server.*" key with the prefix
// stripped.
//
// Sub views share state with the parent — there is no snapshot copy, no
// source duplication, no separate mutex. A reload on the parent is visible
// to the sub immediately, and any sub-side mutation (Set, SetDefault,
// RegisterAlias) is visible to the parent. Closing the parent invalidates
// every sub view derived from it.
//
// Sub("") returns the parent unchanged. Repeated Sub calls concatenate:
// Sub("a").Sub("b") is equivalent to Sub("a.b").
func (r *Registry) Sub(prefix string) *Registry {
	parsed := ParsePath(prefix)
	if len(parsed) == 0 {
		return r
	}
	return &Registry{
		state:  r.state,
		prefix: r.prefix.Append(parsed...),
	}
}

// Prefix returns the sub-tree path this registry view is rooted at. The
// returned [Path] is empty for a root registry. Useful for diagnostic
// output and for callers that need to reconstruct fully-qualified keys
// from a sub view.
func (r *Registry) Prefix() Path { return r.prefix.Clone() }

// fullKey returns the parent-relative path string for a key supplied to a
// sub-view's Get / Set / IsSet call. For a root registry the supplied key
// is canonicalised through ParsePath; for a sub view r.prefix is prepended.
//
// Centralized so every read/write method consults the same logic and so
// adding behavior (case-folding, key normalisation) only needs one place
// to change.
func (r *Registry) fullKey(key string) string {
	parsed := ParsePath(key)
	if len(r.prefix) == 0 {
		return parsed.String()
	}
	full := r.prefix.Append(parsed...)
	return full.String()
}

// fullPath is the [Path]-typed twin of [fullKey].
func (r *Registry) fullPath(p Path) Path {
	if len(r.prefix) == 0 {
		return p
	}
	return r.prefix.Append(p...)
}
