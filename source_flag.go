package recon

import (
	"fmt"
)

// FlagAdapter is the seam between recon and a command-line-flag
// parser. recon does not pick a parser — the stdlib `flag` package
// or any third-party library can satisfy the interface from the
// caller's side.
//
// An adapter must distinguish flags the user explicitly set from
// those still holding compile-time defaults. A flag source usually
// sits first in the precedence order, so reporting an unset flag would
// shadow every lower-precedence source.
type FlagAdapter interface {
	// Names returns the names of flags the user explicitly set, in
	// any order. The returned slice may alias the adapter's storage;
	// [FlagSource] never mutates it.
	Names() []string

	// Lookup returns the value associated with the named flag. set is
	// false for flags whose value came from a compile-time default.
	Lookup(name string) (value any, set bool)
}

// FlagSource is a [Source] backed by a [FlagAdapter]. By default a
// flag name, minus any leading "-" or "--", is parsed as a dotted path,
// so "server.port" maps to Path{"server", "port"} and "server-port" to
// a single segment. Pair with [WithFlagPathTransform] to map names like
// "server-port" onto "server.port".
type FlagSource struct {
	adapter   FlagAdapter
	name      string
	transform func(flagName string) Path
}

// NewFlagSource constructs a [FlagSource] backed by adapter. Default
// name is "flags"; override via [WithFlagName] when composing multiple
// adapters into one registry. Returns wrapped [ErrInvalidPath] when
// adapter is nil.
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

// Name returns the source identifier.
func (s *FlagSource) Name() string { return s.name }

// Get matches path against the post-transform Path of each flag the
// adapter reports as set; the first such flag the adapter reports as
// set by the user supplies the value. Never returns an error.
func (s *FlagSource) Get(path Path) (Value, bool, error) {
	for _, name := range s.adapter.Names() {
		if !s.transform(name).Equal(path) {
			continue
		}
		raw, set := s.adapter.Lookup(name)
		if !set {
			continue
		}
		return NewValue(raw), true, nil
	}
	return Value{}, false, nil
}

// Keys returns the explicitly-set flags projected to recon paths,
// sorted by canonical string.
func (s *FlagSource) Keys() []Path {
	names := s.adapter.Names()
	out := make([]Path, 0, len(names))
	for _, name := range names {
		out = append(out, s.transform(name))
	}
	sortPaths(out)
	return out
}

// Close is a no-op.
func (s *FlagSource) Close() error { return nil }

// defaultFlagPathTransform strips one leading "--" or "-" and parses
// the remainder as a [Path]. "server.port" becomes Path{"server",
// "port"}; "server-port" becomes a single segment. Pass a custom
// transform via [WithFlagPathTransform] for dash-to-dot rewriting.
func defaultFlagPathTransform(name string) Path {
	switch {
	case len(name) >= 2 && name[:2] == "--":
		name = name[2:]
	case len(name) >= 1 && name[0] == '-':
		name = name[1:]
	}
	return ParsePath(name)
}
