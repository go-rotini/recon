package recon

import (
	"slices"
	"strings"
)

// DefaultDelimiter is the path-segment delimiter [ParsePath] consumes
// and [Path.String] emits.
const DefaultDelimiter = "."

// Path is an ordered sequence of segments naming a value in the
// configuration hierarchy. Path{"server", "port"} represents
// "server.port" under the default delimiter.
//
// Methods that return a Path ([Path.Append], [Path.After],
// [Path.Clone]) return a fresh slice and do not mutate the receiver.
type Path []string

// MakePath constructs a Path from raw segments; no delimiter parsing
// is performed. Use [ParsePath] to parse a delimited string.
func MakePath(segments ...string) Path {
	if len(segments) == 0 {
		return Path{}
	}
	p := make(Path, len(segments))
	copy(p, segments)
	return p
}

// ParsePath parses s using [DefaultDelimiter]. A segment containing
// the delimiter or "[" is bracket-escaped: ParsePath("[my.key].sub")
// returns Path{"my.key", "sub"}. Inside brackets "]]" stands for a
// literal "]". An empty string returns an empty Path; "[]" is a Path
// holding one empty segment.
//
// ParsePath never errors. An unclosed bracket is treated as a literal
// "[" at that position; two consecutive delimiters produce an empty
// segment so [Path.String] round-trips.
func ParsePath(s string) Path {
	if s == "" {
		return Path{}
	}
	if strings.IndexByte(s, '[') < 0 {
		return Path(strings.Split(s, DefaultDelimiter))
	}
	var (
		out Path
		buf strings.Builder
		i   int
	)
	for i < len(s) {
		switch c := s[i]; c {
		case '[':
			end := closingBracket(s, i+1)
			if end < 0 {
				buf.WriteByte(c)
				i++
				continue
			}
			buf.WriteString(strings.ReplaceAll(s[i+1:end], "]]", "]"))
			i = end + 1
		case DefaultDelimiter[0]:
			out = append(out, buf.String())
			buf.Reset()
			i++
		default:
			buf.WriteByte(c)
			i++
		}
	}
	out = append(out, buf.String())
	return out
}

// closingBracket returns the index of the "]" closing a bracketed
// segment whose content starts at from, skipping "]]" escapes, or -1.
func closingBracket(s string, from int) int {
	for j := from; j < len(s); j++ {
		if s[j] != ']' {
			continue
		}
		if j+1 < len(s) && s[j+1] == ']' {
			j++
			continue
		}
		return j
	}
	return -1
}

// needsBrackets reports whether seg must be bracket-escaped to survive
// [ParsePath].
func needsBrackets(seg string) bool {
	return strings.Contains(seg, DefaultDelimiter) || strings.IndexByte(seg, '[') >= 0
}

// String returns the canonical delimited form of p, which [ParsePath]
// turns back into p. Segments containing the delimiter or "[" are
// bracket-escaped, with "]" inside brackets doubled.
func (p Path) String() string {
	switch {
	case len(p) == 0:
		return ""
	case len(p) == 1 && p[0] == "":
		return "[]"
	case len(p) == 1 && !needsBrackets(p[0]):
		return p[0]
	}
	n := len(p) - 1
	for _, seg := range p {
		n += len(seg) + 2
	}
	var b strings.Builder
	b.Grow(n)
	for i, seg := range p {
		if i > 0 {
			b.WriteString(DefaultDelimiter)
		}
		if needsBrackets(seg) {
			b.WriteByte('[')
			b.WriteString(strings.ReplaceAll(seg, "]", "]]"))
			b.WriteByte(']')
		} else {
			b.WriteString(seg)
		}
	}
	return b.String()
}

// sortPaths sorts ps by canonical string form, rendering each path
// once rather than on every comparison.
func sortPaths(ps []Path) {
	if len(ps) < 2 {
		return
	}
	type keyed struct {
		path Path
		key  string
	}
	ks := make([]keyed, len(ps))
	for i, p := range ps {
		ks[i] = keyed{path: p, key: p.String()}
	}
	slices.SortFunc(ks, func(a, b keyed) int { return strings.Compare(a.key, b.key) })
	for i, k := range ks {
		ps[i] = k.path
	}
}

// topLevelDelims calls fn with the index of each delimiter in the
// canonical path string s that separates two segments (one outside
// brackets), in order. fn returning false stops the walk.
func topLevelDelims(s string, fn func(i int) bool) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[':
			if end := closingBracket(s, i+1); end >= 0 {
				i = end
			}
		case DefaultDelimiter[0]:
			if !fn(i) {
				return
			}
		}
	}
}

// Equal reports whether p and other have identical segments.
func (p Path) Equal(other Path) bool {
	if len(p) != len(other) {
		return false
	}
	for i := range p {
		if p[i] != other[i] {
			return false
		}
	}
	return true
}

// HasPrefix reports whether prefix is a segment-wise prefix of p. An
// empty prefix matches every Path.
func (p Path) HasPrefix(prefix Path) bool {
	if len(prefix) > len(p) {
		return false
	}
	for i := range prefix {
		if p[i] != prefix[i] {
			return false
		}
	}
	return true
}

// After returns a copy of the suffix of p following prefix, or nil when
// prefix is not a prefix of p.
func (p Path) After(prefix Path) Path {
	if !p.HasPrefix(prefix) {
		return nil
	}
	if len(prefix) == 0 {
		return p.Clone()
	}
	out := make(Path, len(p)-len(prefix))
	copy(out, p[len(prefix):])
	return out
}

// Append returns a new Path with seg appended.
func (p Path) Append(seg ...string) Path {
	if len(seg) == 0 {
		return p.Clone()
	}
	out := make(Path, len(p)+len(seg))
	copy(out, p)
	copy(out[len(p):], seg)
	return out
}

// Clone returns an independent copy of p.
func (p Path) Clone() Path {
	if len(p) == 0 {
		return Path{}
	}
	out := make(Path, len(p))
	copy(out, p)
	return out
}
