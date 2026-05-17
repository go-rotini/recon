package recon

import (
	"fmt"
	"os"
	"os/user"
	"strings"
)

// expandShellPath runs path through POSIX-shell-style expansion
// suitable for [FileSource] paths. The supported forms:
//
//   - `~/...`           → user home + remainder.
//   - `~name/...`       → name's home + remainder (POSIX only).
//   - `$VAR`            → os.Getenv("VAR"); unset → empty.
//   - `${VAR}`          → same.
//   - `${VAR-def}`      → VAR if set (even when empty), else def.
//   - `${VAR:-def}`     → VAR if set AND non-empty, else def.
//   - `${VAR:?msg}`     → VAR if set AND non-empty, else error
//     wrapping the user-supplied msg.
//   - `${VAR:+alt}`     → alt if VAR is set AND non-empty, else
//     empty.
//   - `${VAR+alt}`      → alt if VAR is set (even when empty), else
//     empty.
//
// Recursive expansion is NOT performed: a default value carrying a
// `$VAR` reference passes through verbatim. This matches POSIX
// shell behavior and prevents reference cycles in the default
// expression.
//
// A literal `$` not followed by `{` or a valid identifier char is
// preserved verbatim. Unclosed `${` is preserved verbatim.
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

// expandTilde handles the leading-`~` portion of a path. Forms:
//
//   - `~`           → current user's home.
//   - `~/`          → same + the slash.
//   - `~user/...`   → user's home + remainder.
//
// A `~` that isn't at the start of the path is treated as literal.
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
	// ~user/...
	name := path[1:end]
	u, err := user.Lookup(name)
	if err != nil {
		return "", fmt.Errorf("recon: ~%s expand: %w", name, err)
	}
	return u.HomeDir + path[end:], nil
}

// expandVars walks s and substitutes `$VAR` / `${...}` per the
// rules documented on [expandShellPath].
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
			// Stray `$` — keep verbatim.
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String(), nil
}

// expandBraced handles `${...}` at position i in s. Returns the
// substitution and the number of bytes consumed (including the
// `${` and the closing `}`).
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

// evalBraced evaluates the contents of `${...}` per the modifiers
// documented on [expandShellPath].
func evalBraced(expr string) (string, error) {
	// Find the operator: one of ":-", ":+", ":?", "-", "+".
	idx, op := findShellOp(expr)
	if idx < 0 {
		// Bare ${VAR}.
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

// findShellOp returns the index of the first POSIX-style modifier
// in expr and the matched operator string. Operators are checked in
// length-descending order so `:-` doesn't get matched as `-`.
func findShellOp(expr string) (int, string) {
	ops := []string{":-", ":+", ":?", "-", "+"}
	for _, op := range ops {
		if idx := strings.Index(expr, op); idx >= 0 {
			return idx, op
		}
	}
	return -1, ""
}

// isVarFirstByte reports whether c can start a `$VAR` name. POSIX
// shells accept [A-Za-z_]; subsequent chars also allow digits.
func isVarFirstByte(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// isVarByte reports whether c can continue a `$VAR` name.
func isVarByte(c byte) bool {
	return isVarFirstByte(c) || (c >= '0' && c <= '9')
}
