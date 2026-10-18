package recon

import (
	"strconv"
	"unicode/utf8"
)

// scanInts reports whether fmt.Sscanf(input, format, out...) would
// return a nil error, storing the integers it reads in out. format may
// hold only literal ASCII text, single spaces and %d verbs. It follows
// Sscanf rule for rule, so the codecs need not link fmt's scanner:
//
//   - a space in format matches one or more spaces in input, or the end
//     of input, but not a newline;
//   - %d skips spaces (a newline is an error), then reads an optional
//     sign and at least one decimal digit, and fails on overflow;
//   - a literal must match the next rune; input after format is ignored.
//
// "Space" is fmt's set of space runes. TestScanIntsMatchesSscanf and
// FuzzScanInts compare it with fmt.Sscanf.
func scanInts(input, format string, out ...*int) bool {
	in, n := input, 0
	for i := 0; i < len(format); {
		var ok bool
		switch c := format[i]; {
		case c == ' ':
			i++
			if in, ok = matchScanSpace(in); !ok {
				return false
			}
		case c == '%' && i+1 < len(format) && format[i+1] == 'd':
			i += 2
			if n >= len(out) {
				return false
			}
			var v int
			if in, v, ok = scanDecimal(in); !ok {
				return false
			}
			*out[n] = v
			n++
		case c == '%':
			return false // only %d is supported
		default:
			r, w := utf8.DecodeRuneInString(in)
			if in == "" || r != rune(c) {
				return false
			}
			in = in[w:]
			i++
		}
	}
	return n == len(out)
}

// matchScanSpace matches a space in the format: one or more space runes
// other than a newline, or the end of input.
func matchScanSpace(in string) (string, bool) {
	if in == "" {
		return in, true
	}
	r, w := utf8.DecodeRuneInString(in)
	if !isScanSpace(r) || r == '\n' {
		return in, false
	}
	in = in[w:]
	for in != "" {
		r, w = utf8.DecodeRuneInString(in)
		if !isScanSpace(r) || r == '\n' {
			break
		}
		in = in[w:]
	}
	return in, true
}

// scanDecimal reads a %d: it skips spaces, then reads an optional sign
// and at least one decimal digit that fit in an int, returning the rest.
func scanDecimal(in string) (string, int, bool) {
	in, ok := skipScanSpace(in)
	if !ok || in == "" {
		return in, 0, false
	}
	end := 0
	if in[0] == '+' || in[0] == '-' {
		end = 1
	}
	digits := end
	for end < len(in) && in[end] >= '0' && in[end] <= '9' {
		end++
	}
	if end == digits {
		return in, 0, false
	}
	v, err := strconv.ParseInt(in[:end], 10, strconv.IntSize)
	if err != nil {
		return in, 0, false
	}
	return in[end:], int(v), true
}

// skipScanSpace is fmt's SkipSpace for Sscanf: it drops leading space
// runes and fails at a newline (a "\r\n" counts as one).
func skipScanSpace(in string) (string, bool) {
	for in != "" {
		r, w := utf8.DecodeRuneInString(in)
		if r == '\n' {
			return in, false
		}
		if !isScanSpace(r) {
			return in, true
		}
		in = in[w:]
	}
	return in, true
}

// isScanSpace reports whether fmt's scanner treats r as a space.
func isScanSpace(r rune) bool {
	switch {
	case r >= 0x0009 && r <= 0x000d, r == 0x0020, r == 0x0085, r == 0x00a0, r == 0x1680,
		r >= 0x2000 && r <= 0x200a, r == 0x2028, r == 0x2029, r == 0x202f, r == 0x205f, r == 0x3000:
		return true
	}
	return false
}
