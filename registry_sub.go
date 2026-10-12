package recon

// Sub returns a [Registry] view rooted at prefix. Reads, writes, binds,
// [Registry.AllKeys] and [Registry.DescribeKey] on the returned registry
// take keys relative to prefix. [Registry.Describe], [Registry.Snapshot],
// the Save methods and source management still cover the whole
// registry.
//
// Sub views share state with the parent: no snapshot copy, no source
// duplication. A reload on the parent is visible to the sub
// immediately and vice versa. Closing the parent invalidates every
// sub view.
//
// Sub("") returns the parent unchanged. Sub("a").Sub("b") is
// equivalent to Sub("a.b").
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

// Prefix returns the sub-tree path this view is rooted at, or an
// empty Path for a root registry.
func (r *Registry) Prefix() Path { return r.prefix.Clone() }

// fullKey resolves a sub-view key to its parent-relative canonical
// path string.
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
