package recon

// resolveKey runs the per-key precedence walk and reports the winner
// plus the full per-source provenance list. The order:
//
//  1. explicit override ([Registry.Set])
//  2. pinned source ([Registry.PinSource]) — pinned keys do not fall
//     back through the rest of the chain or to default
//  3. each registered Source in precedence order (first = highest)
//  4. default ([Registry.SetDefault])
//
// Under [MergeShadow] (the default) step 3 is "first-set wins". Under
// [MergeAppend] slice and map contributions across the chain (plus
// default) are concatenated / deep-merged; type-mismatched pairs
// fall back to "higher precedence wins".
func resolveKey(p Path, is snapshotInputs) (val Value, srcs []string, found bool) {
	pathStr := p.String()

	// 1. Explicit override wins unconditionally; never merged.
	if raw, ok := is.explicits[pathStr]; ok {
		v := NewValue(raw).withSource(srcExplicit)
		return v, []string{srcExplicit}, true
	}

	// 2. Pinned source: only this source can supply the key.
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
		// Pinned source not currently registered.
		return Value{}, nil, false
	}

	// 3. Source chain (+ default layer for MergeAppend).
	if is.merge == MergeAppend {
		return resolveAppend(p, pathStr, is)
	}
	return resolveShadow(p, pathStr, is)
}

// resolveShadow is the default first-set-wins walk. Returns the
// winner plus the full provenance list in precedence-descending
// order.
func resolveShadow(p Path, pathStr string, is snapshotInputs) (Value, []string, bool) {
	var winner Value
	var winnerName string
	var srcs []string
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
	if raw, ok := is.defaults[pathStr]; ok {
		v := NewValue(raw).withSource(srcDefault)
		return v, []string{srcDefault}, true
	}
	return Value{}, nil, false
}

// resolveAppend is the [MergeAppend] walk: every contributor is
// collected then folded lowest → highest via [mergeValues]. The
// merged value carries the highest-precedence contributor's name.
func resolveAppend(p Path, pathStr string, is snapshotInputs) (Value, []string, bool) {
	type contribution struct {
		val    Value
		source string
	}

	// Collect in precedence-descending order; fold from the tail.
	var descending []contribution
	for _, s := range is.sources {
		rawVal, ok, err := s.Get(p)
		if err != nil || !ok {
			continue
		}
		descending = append(descending, contribution{val: rawVal, source: s.Name()})
	}
	if raw, ok := is.defaults[pathStr]; ok {
		descending = append(descending, contribution{
			val: NewValue(raw), source: srcDefault,
		})
	}
	if len(descending) == 0 {
		return Value{}, nil, false
	}

	merged := descending[len(descending)-1].val
	for i := len(descending) - 2; i >= 0; i-- {
		merged = mergeValues(merged, descending[i].val)
	}
	winnerName := descending[0].source
	merged = merged.withSource(winnerName)

	srcs := make([]string, len(descending))
	for i, c := range descending {
		srcs[i] = c.source
	}
	return merged, srcs, true
}

// mergeValues combines lo and hi under [MergeAppend] semantics. hi
// has higher precedence.
//
//   - Slice + Slice: concat with lo's elements first.
//   - Map + Map: per-key deep merge.
//   - Anything else: hi wins (scalars shadow; type mismatch shadows).
func mergeValues(lo, hi Value) Value {
	switch {
	case lo.Kind() == SliceKind && hi.Kind() == SliceKind:
		ls, lErr := lo.AsSlice()
		hs, hErr := hi.AsSlice()
		if lErr != nil || hErr != nil {
			return hi
		}
		merged := make([]any, 0, len(ls)+len(hs))
		for _, v := range ls {
			merged = append(merged, v.Any())
		}
		for _, v := range hs {
			merged = append(merged, v.Any())
		}
		return NewValue(merged)
	case lo.Kind() == MapKind && hi.Kind() == MapKind:
		lm, lErr := lo.AsMap()
		hm, hErr := hi.AsMap()
		if lErr != nil || hErr != nil {
			return hi
		}
		merged := make(map[string]any, len(lm)+len(hm))
		for k, v := range lm {
			merged[k] = v.Any()
		}
		for k, hv := range hm {
			if lv, exists := lm[k]; exists {
				merged[k] = mergeValues(lv, hv).Any()
				continue
			}
			merged[k] = hv.Any()
		}
		return NewValue(merged)
	default:
		return hi
	}
}
