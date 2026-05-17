package recon

// maxAliasDepth caps alias-chain traversal during resolution to prevent
// runaway loops on a snapshot built before RegisterAlias's cycle check had
// a chance to detect a problem (or a snapshot installed by a future bug).
// 32 is generous — well beyond any reasonable alias graph.
const maxAliasDepth = 32

// resolveAliasChain follows aliases until reaching a non-alias path or
// exhausting the depth budget (which signals a cycle that slipped past
// [validateNoAliasCycle] — i.e. a bug in alias-graph maintenance).
//
// The function is intentionally lenient at the snapshot-resolution layer:
// on a budget-exhausted cycle it returns the LAST visited path so
// resolution can still pick up a value from a registered source if one
// happens to be there. The authoritative cycle check is
// [validateNoAliasCycle], called from [Registry.RegisterAlias] before the
// alias is installed.
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
// existing alias graph would create a cycle. Returns the cycle chain when
// one would be created, or nil when the alias is safe to install.
//
// Cycles are detected by walking from canonical: if we ever reach alias,
// adding the new edge would close a loop. The returned chain is alias →
// (existing edges back to alias), in walk order.
func validateNoAliasCycle(alias, canonical string, existing map[string]string) []Path {
	if alias == canonical {
		// Self-alias is a degenerate one-node cycle.
		return []Path{ParsePath(alias)}
	}
	visited := map[string]struct{}{alias: {}}
	chain := []Path{ParsePath(alias), ParsePath(canonical)}

	cur := canonical
	for range maxAliasDepth {
		next, ok := existing[cur]
		if !ok {
			return nil // hit a canonical key — no cycle
		}
		if next == alias {
			chain = append(chain, ParsePath(next))
			return chain
		}
		if _, seen := visited[next]; seen {
			// Pre-existing cycle reachable from canonical — surface it as
			// part of the offending chain so the error message points at
			// the real problem.
			chain = append(chain, ParsePath(next))
			return chain
		}
		visited[next] = struct{}{}
		chain = append(chain, ParsePath(next))
		cur = next
	}
	// Depth budget exhausted — treat as a cycle so the caller surfaces
	// rather than silently accepting an alias graph we can't traverse.
	return chain
}
