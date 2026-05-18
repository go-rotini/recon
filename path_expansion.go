package recon

import (
	"fmt"
	"os"
	"os/user"
	"strings"
)

// expandShellPath runs path through POSIX-shell-style expansion:
//
//   - `~/...`, `~name/...`         → home expansion.
//   - `$VAR`, `${VAR}`             → env lookup; unset → empty.
//   - `${VAR-def}`, `${VAR:-def}`  → default fallback.
//   - `${VAR:?msg}`                → error when unset/empty.
//   - `${VAR+alt}`, `${VAR:+alt}`  → conditional alt.
//
// Expansion is one-pass: a default carrying a `$VAR` reference passes
// through verbatim. Stray `$` and unclosed `${` are preserved.
func expandShellPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	expanded, err := expandTilde(path)
	if err != nil {
		return "", err
	}
	return expandVars(expanded)
}

// expandTilde handles a leading `~` or `~user`. A `~` mid-path is
// treated as a literal.
func expandTilde(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	end := strings.IndexAny(path, "/\\")
	if end < 0 {
		end = len(path)
	}
	if end == 1 {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("recon: tilde expand: %w", err)
		}
		return home + path[1:], nil
	}
	name := path[1:end]
	u, err := user.Lookup(name)
	if err != nil {
		return "", fmt.Errorf("recon: ~%s expand: %w", name, err)
	}
	return u.HomeDir + path[end:], nil
}

// expandVars walks s and substitutes `$VAR` / `${...}`.
func expandVars(s string) (string, error) {
	if !strings.ContainsRune(s, '$') {
		return s, nil
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		switch {
		case s[i] != '$':
			b.WriteByte(s[i])
			i++
		case i+1 < len(s) && s[i+1] == '{':
			out, consumed, err := expandBraced(s, i)
			if err != nil {
				return "", err
			}
			b.WriteString(out)
			i += consumed
		case i+1 < len(s) && isVarFirstByte(s[i+1]):
			end := i + 1
			for end < len(s) && isVarByte(s[end]) {
				end++
			}
			name := s[i+1 : end]
			b.WriteString(os.Getenv(name))
			i = end
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String(), nil
}

// expandBraced handles `${...}` at position i. Returns the
// substitution and the number of bytes consumed.
func expandBraced(s string, i int) (string, int, error) {
	closeIdx := strings.IndexByte(s[i:], '}')
	if closeIdx < 0 {
		return s[i:], len(s) - i, nil
	}
	expr := s[i+2 : i+closeIdx]
	consumed := closeIdx + 1
	out, err := evalBraced(expr)
	if err != nil {
		return "", 0, err
	}
	return out, consumed, nil
}

// evalBraced evaluates one `${...}` per the modifier rules.
func evalBraced(expr string) (string, error) {
	idx, op := findShellOp(expr)
	if idx < 0 {
		return os.Getenv(expr), nil
	}
	name := expr[:idx]
	rhs := expr[idx+len(op):]
	val, set := os.LookupEnv(name)
	switch op {
	case ":-":
		if !set || val == "" {
			return rhs, nil
		}
		return val, nil
	case "-":
		if !set {
			return rhs, nil
		}
		return val, nil
	case ":?":
		if !set || val == "" {
			return "", fmt.Errorf("%w: env var %q: %s",
				ErrInvalidPath, name, rhs)
		}
		return val, nil
	case ":+":
		if set && val != "" {
			return rhs, nil
		}
		return "", nil
	case "+":
		if set {
			return rhs, nil
		}
		return "", nil
	default:
		return val, nil
	}
}

// findShellOp returns the index and operator of the first POSIX
// modifier in expr. Longer operators are checked first so `:-` is
// not matched as `-`.
func findShellOp(expr string) (int, string) {
	ops := []string{":-", ":+", ":?", "-", "+"}
	for _, op := range ops {
		if idx := strings.Index(expr, op); idx >= 0 {
			return idx, op
		}
	}
	return -1, ""
}

// isVarFirstByte reports whether c can start a `$VAR` name (POSIX
// [A-Za-z_]).
func isVarFirstByte(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// isVarByte reports whether c can continue a `$VAR` name.
func isVarByte(c byte) bool {
	return isVarFirstByte(c) || (c >= '0' && c <= '9')
}
