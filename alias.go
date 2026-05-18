package recon

// maxAliasDepth caps alias-chain traversal so a bug-installed cycle
// cannot wedge resolution. The authoritative cycle check runs at
// install time in [validateNoAliasCycle].
const maxAliasDepth = 32

// resolveAliasChain follows aliases until reaching a non-alias path or
// exhausting the depth budget. Budget exhaustion returns the last
// visited path so resolution can still pick up a value if one happens
// to be there.
func resolveAliasChain(start string, aliases map[string]string) string {
	cur := start
	for range maxAliasDepth {
		next, ok := aliases[cur]
		if !ok {
			return cur
		}
		cur = next
	}
	return cur
}

// validateNoAliasCycle reports whether adding alias → canonical to the
// existing alias graph would create a cycle. Returns the offending
// chain in walk order, or nil when the alias is safe to install.
func validateNoAliasCycle(alias, canonical string, existing map[string]string) []Path {
	if alias == canonical {
		return []Path{ParsePath(alias)}
	}
	visited := map[string]struct{}{alias: {}}
	chain := []Path{ParsePath(alias), ParsePath(canonical)}

	cur := canonical
	for range maxAliasDepth {
		next, ok := existing[cur]
		if !ok {
			return nil
		}
		if next == alias {
			chain = append(chain, ParsePath(next))
			return chain
		}
		if _, seen := visited[next]; seen {
			// Pre-existing cycle reachable from canonical; surface it
			// so the error message points at the real problem.
			chain = append(chain, ParsePath(next))
			return chain
		}
		visited[next] = struct{}{}
		chain = append(chain, ParsePath(next))
		cur = next
	}
	return chain
}
