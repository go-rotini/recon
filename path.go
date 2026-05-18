package recon

import "strings"

// DefaultDelimiter is the path-segment delimiter [ParsePath] consumes
// and [Path.String] emits.
const DefaultDelimiter = "."

// Path is an ordered sequence of segments naming a value in the
// configuration hierarchy. Path{"server", "port"} represents
// "server.port" under the default delimiter.
//
// Path is a value type. Methods that return a new Path ([Append],
// [After], [Clone]) do not mutate the receiver.
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

// ParsePath parses s using [DefaultDelimiter]. Segments containing the
// delimiter are bracket-escaped on input: ParsePath("[my.key].sub")
// returns Path{"my.key", "sub"}. An empty string returns an empty Path.
//
// ParsePath never errors. An unclosed bracket is treated as a literal
// "[" at that position; two consecutive delimiters produce an empty
// segment so [Path.String] round-trips.
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
			j := strings.IndexByte(s[i+1:], ']')
			if j < 0 {
				buf.WriteByte(c)
				i++
				continue
			}
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

// String returns the canonical delimited form of p. Segments
// containing the delimiter are bracket-escaped.
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

// After returns the suffix of p following prefix, or nil when prefix
// is not a prefix of p. After(Path{}) returns a fresh copy of p.
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
