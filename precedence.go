package recon

// resolveKey runs the per-key precedence walk and reports the winner plus
// the full per-source provenance list. The order is fixed:
//
//  1. explicit override (Registry.Set)
//  2. pinned source (Registry.PinSource) — if pinned, NO fallback through
//     the rest of the chain or to default; the pinned source is the only
//     authority for that key.
//  3. each registered Source in precedence order (first → highest)
//  4. default (Registry.SetDefault)
//
// found=false when no layer supplied the key. The returned []string lists
// every source name that had a value for the key in precedence order — the
// first entry is the winner; subsequent entries are shadowed sources that a
// "config explain" report can show as overridden.
func resolveKey(p Path, is snapshotInputs) (val Value, srcs []string, found bool) {
	pathStr := p.String()

	// 1. Explicit override wins unconditionally.
	if raw, ok := is.explicits[pathStr]; ok {
		v := NewValue(raw)
		v = v.withSource(srcExplicit)
		return v, []string{srcExplicit}, true
	}

	// 2. Pinned source: only this source can supply the key. No fallback.
	if pinName, pinned := is.pins[pathStr]; pinned {
		for _, s := range is.sources {
			if s.Name() != pinName {
				continue
			}
			rawVal, ok, err := s.Get(p)
			if err != nil || !ok {
				return Value{}, nil, false
			}
			rawVal = rawVal.withSource(s.Name())
			return rawVal, []string{s.Name()}, true
		}
		// Pinned source isn't registered (anymore) — treat as unset.
		return Value{}, nil, false
	}

	// 3. Source chain: first-set wins. Record every source that had a value
	//    for provenance reporting.
	var winner Value
	var winnerName string
	for _, s := range is.sources {
		rawVal, ok, err := s.Get(p)
		if err != nil || !ok {
			continue
		}
		if winnerName == "" {
			winner = rawVal.withSource(s.Name())
			winnerName = s.Name()
		}
		srcs = append(srcs, s.Name())
	}
	if winnerName != "" {
		return winner, srcs, true
	}

	// 4. Default — lowest precedence, only consulted on a full miss.
	if raw, ok := is.defaults[pathStr]; ok {
		v := NewValue(raw).withSource(srcDefault)
		return v, []string{srcDefault}, true
	}

	return Value{}, nil, false
}
