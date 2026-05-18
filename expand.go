package recon

import (
	"fmt"
	"strings"
)

// expandValueRefs substitutes ${other.key} placeholders in s with the
// matching value from r's current snapshot.
//
// Recognized forms:
//
//   - ${key.path} — required; an unresolved reference returns s
//     unchanged plus a non-nil error.
//   - ${key.path:-default} — substitute default when unset or empty.
//   - ${key.path:?msg} — error with msg when unset.
//
// A literal `$` not followed by `{` is preserved. Substitution is
// one-pass; referenced values are not recursively expanded.
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
		i += next + 2 // past "${"
		closeIdx := strings.IndexByte(s[i:], '}')
		if closeIdx < 0 {
			// Unclosed brace — preserve the rest verbatim.
			b.WriteString("${")
			b.WriteString(s[i:])
			break
		}
		expr := s[i : i+closeIdx]
		i += closeIdx + 1 // past "}"

		resolved, err := resolveExpandExpr(expr, r)
		if err != nil {
			return s, err
		}
		b.WriteString(resolved)
	}
	return b.String(), nil
}

// resolveExpandExpr handles one expression between `${` and `}`.
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
		// Unknown modifier: treat the whole expr as the key.
		return expandRequired(expr, r)
	}
}

// expandRequired implements `${key}`.
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

// expandWithDefault implements `${key:-default}`. The default text is
// taken verbatim; nested expand syntax inside it is not re-evaluated.
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

// expandOrError implements `${key:?msg}`.
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

// expandMissingError reports an unresolved `${key}` reference. Wraps
// [ErrKeyNotFound].
type expandMissingError struct{ key string }

func (e *expandMissingError) Error() string {
	return fmt.Sprintf("recon: expand: key not found: %q", e.key)
}
func (e *expandMissingError) Is(target error) bool { return target == ErrKeyNotFound }

// expandRequiredError reports a `${key:?msg}` failure with msg
// preserved verbatim.
type expandRequiredError struct{ key, msg string }

func (e *expandRequiredError) Error() string {
	return fmt.Sprintf("recon: expand %q: %s", e.key, e.msg)
}
