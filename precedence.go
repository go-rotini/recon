package recon

// resolveKey runs the per-key precedence walk and reports the
// winner plus the full per-source provenance list. The order is
// fixed:
//
//  1. explicit override (Registry.Set)
//  2. pinned source (Registry.PinSource) — if pinned, NO fallback
//     through the rest of the chain or to default; the pinned source
//     is the only authority for that key.
//  3. each registered Source in precedence order (first → highest)
//  4. default (Registry.SetDefault)
//
// Under [MergeShadow] (the default), the source chain is "first-set
// wins"; the returned []string lists every source that had a value
// for the key, winner first.
//
// Under [MergeAppend], slice-valued and map-valued contributions
// across the source chain (plus the default layer when present) are
// concatenated / deep-merged; the returned []string lists every
// contributor in precedence order. Scalar-valued contributions, or
// any type-mismatched pair, fall back to "higher precedence wins"
// — there's no useful merge of an int and a string.
//
// found=false when no layer supplied the key.
func resolveKey(p Path, is snapshotInputs) (val Value, srcs []string, found bool) {
	pathStr := p.String()

	// 1. Explicit override wins unconditionally — never merged.
	if raw, ok := is.explicits[pathStr]; ok {
		v := NewValue(raw)
		v = v.withSource(srcExplicit)
		return v, []string{srcExplicit}, true
	}

	// 2. Pinned source: only this source can supply the key. No
	//    fallback, no merge.
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

	// 3. Source chain (+ default layer for MergeAppend).
	if is.merge == MergeAppend {
		return resolveAppend(p, pathStr, is)
	}
	return resolveShadow(p, pathStr, is)
}

// resolveShadow is the default first-set-wins source-chain walk.
// Returns the highest-precedence source's value plus the full
// provenance list (winner first, shadowed contributors after, in
// precedence-descending order).
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

// resolveAppend is the [MergeAppend] source-chain walk: every
// contributor is collected, then folded from lowest precedence to
// highest via [mergeValues]. The default layer joins as the lowest
// element when present; explicit + pin paths already returned in
// [resolveKey] and don't reach this function.
//
// The result's Value.Source() is the highest-precedence
// contributor's name — the source that "would have won" under
// [MergeShadow]. Provenance lists every contributor in
// precedence-descending order (winner first).
func resolveAppend(p Path, pathStr string, is snapshotInputs) (Value, []string, bool) {
	type contribution struct {
		val    Value
		source string
	}

	// Collect contributions in precedence-descending order, then
	// reverse so we can fold lowest → highest.
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

	// Fold lowest → highest.
	merged := descending[len(descending)-1].val
	for i := len(descending) - 2; i >= 0; i-- {
		merged = mergeValues(merged, descending[i].val)
	}
	// Tag the merged value with the highest-precedence contributor's
	// source name.
	winnerName := descending[0].source
	merged = merged.withSource(winnerName)

	srcs := make([]string, len(descending))
	for i, c := range descending {
		srcs[i] = c.source
	}
	return merged, srcs, true
}

// mergeValues combines lo and hi under [MergeAppend] semantics. hi
// has higher precedence; lo has lower. The merge rules:
//
//   - SliceKind + SliceKind: concat (lo's elements first, hi's
//     appended). This matches the standard config-overlay
//     expectation where higher-precedence values land at the tail.
//   - MapKind + MapKind: per-key deep-merge — every hi key replaces
//     the lo key's value via the same mergeValues recursion; lo-only
//     keys survive untouched.
//   - Any other case: hi wins outright (scalar shadowing, or type
//     mismatch where there's no sensible merge).
func mergeValues(lo, hi Value) Value {
	switch {
	case lo.Kind() == SliceKind && hi.Kind() == SliceKind:
		ls, lErr := lo.AsSlice()
		hs, hErr := hi.AsSlice()
		if lErr != nil || hErr != nil {
			// Kind check should have made these infallible; treat
			// an unexpected error as a type mismatch and shadow.
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
