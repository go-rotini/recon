package recon

import (
	"fmt"
	"strings"
)

// expandValueRefs substitutes `${other.key}` placeholders in s with
// the matching value from r's current snapshot. Used by the [Bind]
// walker when a field carries the `expand` tag option.
//
// Recognized forms:
//
//   - `${key.path}` — required substitution; an unresolved reference
//     returns the input s unchanged plus a non-nil error.
//   - `${key.path:-default}` — fall back to default when the
//     referenced key is unset.
//   - `${key.path:?msg}` — error with msg when the referenced key
//     is unset.
//
// A literal `$` that is not followed by `{` is preserved verbatim.
// Substitution is one-pass — referenced values are NOT recursively
// expanded — to prevent reference cycles and to match POSIX shell
// behavior.
func expandValueRefs(s string, r *Registry) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		next := strings.Index(s[i:], "${")
		if next < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+next])
		i += next + 2 // skip past "${"
		closeIdx := strings.IndexByte(s[i:], '}')
		if closeIdx < 0 {
			// Unclosed brace — preserve the rest verbatim.
			b.WriteString("${")
			b.WriteString(s[i:])
			break
		}
		expr := s[i : i+closeIdx]
		i += closeIdx + 1 // skip past "}"

		resolved, err := resolveExpandExpr(expr, r)
		if err != nil {
			return s, err
		}
		b.WriteString(resolved)
	}
	return b.String(), nil
}

// resolveExpandExpr handles one expand expression — the contents
// between the `${` and `}`. Supports the three forms documented on
// [expandValueRefs].
func resolveExpandExpr(expr string, r *Registry) (string, error) {
	key, modifier, hasMod := strings.Cut(expr, ":")
	if !hasMod {
		return expandRequired(key, r)
	}
	switch {
	case strings.HasPrefix(modifier, "-"):
		return expandWithDefault(key, modifier[1:], r), nil
	case strings.HasPrefix(modifier, "?"):
		return expandOrError(key, modifier[1:], r)
	default:
		// Unknown modifier — fall back to the plain lookup, treating
		// the entire expr as the key. (No mid-stream syntax errors.)
		return expandRequired(expr, r)
	}
}

// expandRequired implements `${key}` — the lookup must succeed.
func expandRequired(key string, r *Registry) (string, error) {
	v, ok, err := r.Get(key)
	if err != nil {
		return "", fmt.Errorf("recon: expand %q: %w", key, err)
	}
	if !ok {
		return "", &expandMissingError{key: key}
	}
	s, asErr := valueAsString(v)
	if asErr != nil {
		return "", fmt.Errorf("recon: expand %q: %w", key, asErr)
	}
	return s, nil
}

// expandWithDefault implements `${key:-default}` — the default is
// substituted when the key is unset OR the resolved value is empty.
// The default text is taken verbatim; nested expand syntax inside
// the default is NOT re-evaluated (one-pass expansion).
func expandWithDefault(key, def string, r *Registry) string {
	v, ok, err := r.Get(key)
	if err != nil || !ok {
		return def
	}
	s, asErr := valueAsString(v)
	if asErr != nil || s == "" {
		return def
	}
	return s
}

// expandOrError implements `${key:?msg}` — fails with msg when the
// key is unset; otherwise returns the resolved value.
func expandOrError(key, msg string, r *Registry) (string, error) {
	v, ok, err := r.Get(key)
	if err != nil {
		return "", fmt.Errorf("recon: expand %q: %w", key, err)
	}
	if !ok {
		return "", &expandRequiredError{key: key, msg: msg}
	}
	return valueAsString(v)
}

// expandMissingError reports a `${key}` reference that resolved to
// nothing. Wraps [ErrKeyNotFound] for errors.Is.
type expandMissingError struct{ key string }

func (e *expandMissingError) Error() string {
	return fmt.Sprintf("recon: expand: key not found: %q", e.key)
}
func (e *expandMissingError) Is(target error) bool { return target == ErrKeyNotFound }

// expandRequiredError reports a `${key:?msg}` failure. Carries the
// caller's msg verbatim so the surfaced error explains why the key
// matters.
type expandRequiredError struct{ key, msg string }

func (e *expandRequiredError) Error() string {
	return fmt.Sprintf("recon: expand %q: %s", e.key, e.msg)
}
