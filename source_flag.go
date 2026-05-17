package recon

import (
	"fmt"
	"slices"
)

// FlagAdapter is the parser-agnostic seam between recon and whatever
// command-line-flag library a caller uses. recon does NOT pick a flag
// parser — stdlib `flag`, `spf13/pflag`, `alecthomas/kong`, rotini's
// own argv parser, or anything else can satisfy the interface from
// the caller's side.
//
// An adapter reports two things:
//
//  1. The flat list of flag names the parser observed as explicitly
//     set by the user (so a flag still holding its default value
//     does NOT shadow a lower-precedence source).
//  2. The value associated with each such flag, looked up by name.
//
// Implementations MUST treat both methods as cheap and side-effect-
// free: [FlagSource] calls Names + Lookup on every snapshot rebuild.
//
// The "explicitly set" distinction is critical to the precedence
// chain — see §1.5 "CLI → Env → Config → Default" in the
// requirements doc. A flag that holds its compile-time default is
// indistinguishable from "the user did not pass --foo", and the
// adapter must NOT report it.
type FlagAdapter interface {
	// Names returns the list of flags the user explicitly set, in
	// any order. The returned slice may alias the adapter's
	// storage; [FlagSource] never mutates it.
	Names() []string

	// Lookup returns the value associated with the named flag. The
	// set bool MUST be false for flags whose values came from a
	// compile-time default — `Names` already filters them, but
	// Lookup's contract is independently testable so consumers can
	// share an adapter across multiple [FlagSource] instances.
	Lookup(name string) (value any, set bool)
}

// FlagSource is the [Source] backed by a [FlagAdapter]. Construct via
// [NewFlagSource]; pair with [WithFlagPathTransform] to map flag
// names like "--server-port" onto recon paths like "server.port".
//
// FlagSource is read-only — the parser owns the canonical flag state
// and recon defers to it for every lookup. There is no Replace
// equivalent of [MapSource.Replace] because re-reading from the same
// adapter on the next snapshot rebuild already picks up parser-side
// mutations.
type FlagSource struct {
	adapter   FlagAdapter
	name      string
	transform func(flagName string) Path
}

// NewFlagSource constructs a [FlagSource] backed by adapter. The
// source's default name is "flags"; override with [WithFlagName] when
// composing more than one flag adapter into a single registry (e.g.,
// global flags + subcommand flags).
//
// Returns an error wrapping [ErrInvalidPath] when adapter is nil.
func NewFlagSource(adapter FlagAdapter, opts ...FlagOption) (*FlagSource, error) {
	if adapter == nil {
		return nil, fmt.Errorf("%w: NewFlagSource: nil FlagAdapter", ErrInvalidPath)
	}
	cfg := flagOptions{
		name:      "flags",
		transform: defaultFlagPathTransform,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &FlagSource{
		adapter:   adapter,
		name:      cfg.name,
		transform: cfg.transform,
	}, nil
}

// Name returns the source's identifier. Defaults to "flags"; can be
// overridden via [WithFlagName].
func (s *FlagSource) Name() string { return s.name }

// Get looks up the path via the adapter. The match is on the flag's
// canonical name post-transform: [FlagSource] computes every flag's
// path via the configured transform, then compares against the
// requested path.
//
// FlagSource never returns an error — the adapter's Lookup is the
// only fallible operation it depends on, and the interface promises
// not to surface its own errors.
func (s *FlagSource) Get(path Path) (Value, bool, error) {
	target := path.String()
	for _, name := range s.adapter.Names() {
		if s.transform(name).String() != target {
			continue
		}
		raw, set := s.adapter.Lookup(name)
		if !set {
			return Value{}, false, nil
		}
		return NewValue(raw), true, nil
	}
	return Value{}, false, nil
}

// Keys enumerates the paths the adapter currently knows about (the
// flags that were explicitly set). The result is sorted by canonical
// path string for deterministic snapshot output.
func (s *FlagSource) Keys() []Path {
	names := s.adapter.Names()
	out := make([]Path, 0, len(names))
	for _, name := range names {
		out = append(out, s.transform(name))
	}
	slices.SortFunc(out, func(a, b Path) int {
		switch {
		case a.String() < b.String():
			return -1
		case a.String() > b.String():
			return 1
		default:
			return 0
		}
	})
	return out
}

// Close is a no-op — [FlagSource] holds no resources. The adapter is
// the caller's, and its lifecycle is the caller's responsibility.
func (s *FlagSource) Close() error { return nil }

// defaultFlagPathTransform converts a flag name into a recon [Path]
// using the conservative rule: strip a single leading "--" or "-",
// then ParsePath the remainder. A flag named "server.port" maps to
// Path{"server","port"}; "server-port" maps to a single-segment
// Path{"server-port"}. Callers wanting dash-to-dot rewriting should
// pass their own transform via [WithFlagPathTransform].
func defaultFlagPathTransform(name string) Path {
	switch {
	case len(name) >= 2 && name[:2] == "--":
		name = name[2:]
	case len(name) >= 1 && name[0] == '-':
		name = name[1:]
	}
	return ParsePath(name)
}
