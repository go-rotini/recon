package recon

import "strings"

// DefaultDelimiter is the path-segment delimiter ParsePath uses (and Path.String
// emits) when no per-registry delimiter has been configured. The registry-level
// delimiter is set via WithKeyDelimiter; this constant is the package-wide
// default for the standalone helpers ParsePath and Path.String.
const DefaultDelimiter = "."

// Path is an ordered sequence of segments naming a value in the configuration
// hierarchy. Path{"server", "port"} represents the canonical key "server.port"
// (when the delimiter is the default ".").
//
// A Path is a value type. Cloning is explicit via Clone; methods that return a
// new Path (Append, After) never mutate the receiver.
type Path []string

// MakePath constructs a Path from a list of segments. Each segment is taken
// verbatim — no parsing of the delimiter is performed. To parse a delimited
// string use ParsePath.
func MakePath(segments ...string) Path {
	if len(segments) == 0 {
		return Path{}
	}
	p := make(Path, len(segments))
	copy(p, segments)
	return p
}

// ParsePath parses s using DefaultDelimiter as the segment separator. Segments
// containing the delimiter are bracket-escaped on input: ParsePath("[my.key].sub")
// returns Path{"my.key", "sub"}. An empty string returns an empty Path.
//
// ParsePath never returns an error — malformed input is parsed best-effort. An
// unclosed bracket is treated as a literal "[" at that position. Two consecutive
// delimiters produce an empty segment between them (preserves the rule that
// Path round-trips through String).
func ParsePath(s string) Path {
	if s == "" {
		return Path{}
	}
	var (
		out Path
		buf strings.Builder
		i   int
	)
	for i < len(s) {
		switch c := s[i]; c {
		case '[':
			// Find the closing ']'. If absent, treat the '[' as literal.
			j := strings.IndexByte(s[i+1:], ']')
			if j < 0 {
				buf.WriteByte(c)
				i++
				continue
			}
			// Flush any pending plain segment text into the result before
			// starting a bracketed segment. (Bracketed segments must stand
			// alone; "abc[def].ghi" is rare and treated as buf="abc" plus
			// bracketed "def" — same as "abc.def.ghi" semantically.)
			buf.WriteString(s[i+1 : i+1+j])
			i += j + 2
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

// String returns the canonical delimited form of p. Segments containing the
// delimiter are bracket-escaped: Path{"my.key", "sub"}.String() == "[my.key].sub".
// An empty Path returns the empty string.
func (p Path) String() string {
	if len(p) == 0 {
		return ""
	}
	var b strings.Builder
	for i, seg := range p {
		if i > 0 {
			b.WriteString(DefaultDelimiter)
		}
		if strings.Contains(seg, DefaultDelimiter) {
			b.WriteByte('[')
			b.WriteString(seg)
			b.WriteByte(']')
		} else {
			b.WriteString(seg)
		}
	}
	return b.String()
}

// Equal reports whether p and other have identical length and identical
// segments at every index.
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

// HasPrefix reports whether prefix is a prefix of p (segment-wise). An empty
// prefix is a prefix of every Path.
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

// After returns the suffix of p that follows prefix. If prefix is not actually
// a prefix of p, After returns nil. After(Path{}) returns p itself (a fresh
// slice — not aliased).
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

// Append returns a new Path with seg appended. The receiver is not mutated.
func (p Path) Append(seg ...string) Path {
	if len(seg) == 0 {
		return p.Clone()
	}
	out := make(Path, len(p)+len(seg))
	copy(out, p)
	copy(out[len(p):], seg)
	return out
}

// Clone returns an independent copy of p. Useful when a caller wants to retain
// a Path that the registry may otherwise reuse internally.
func (p Path) Clone() Path {
	if len(p) == 0 {
		return Path{}
	}
	out := make(Path, len(p))
	copy(out, p)
	return out
}
