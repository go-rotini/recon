package recon

import (
	"errors"
	"fmt"
	"strings"
)

// FormatError renders err into a multi-line, human-readable summary
// suitable for direct printing to a terminal. Each entry in a
// [*MultiError] becomes its own line; typed errors that carry a
// [Path] (e.g., [*MissingRequiredError], [*EmptyValueError],
// [*CoercionError], [*ValidationError], [*ImmutableChangedError])
// are formatted with their path leading and any source-provenance
// information trailing in parentheses.
//
// reg is optional. When non-nil, FormatError consults its current
// snapshot to surface the precedence chain alongside each failing
// path — handy for "the value came from this source AND was rejected
// at validation time". Pass nil when the registry is unavailable
// (e.g., when New itself failed) — the rendering still works, just
// without the extra context.
//
// FormatError returns "" when err is nil so it composes cleanly with
// log.Println.
func FormatError(reg *Registry, err error) string {
	if err == nil {
		return ""
	}
	var multi *MultiError
	if errors.As(err, &multi) && multi != nil && len(multi.Errors) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "recon: %d errors:\n", len(multi.Errors))
		for _, sub := range multi.Errors {
			b.WriteString("  • ")
			b.WriteString(formatOneError(reg, sub))
			b.WriteByte('\n')
		}
		// Trim the trailing newline so callers can append context.
		return strings.TrimRight(b.String(), "\n")
	}
	return "recon: " + formatOneError(reg, err)
}

// formatOneError formats a single error. Typed errors that carry a
// Path or Source field get extra context; everything else falls
// back to err.Error().
func formatOneError(reg *Registry, err error) string {
	var (
		mreq  *MissingRequiredError
		empty *EmptyValueError
		coer  *CoercionError
		valid *ValidationError
		immut *ImmutableChangedError
		srcE  *SourceError
		parse *ParseError
	)
	switch {
	case errors.As(err, &mreq):
		return formatPathErr(reg, mreq.Path, "missing required value", "")
	case errors.As(err, &empty):
		return formatPathErr(reg, empty.Path, "empty value", empty.Source)
	case errors.As(err, &coer):
		msg := fmt.Sprintf("coerce %s → %s", coer.WireType, coer.Target)
		if coer.Cause != nil {
			msg += ": " + coer.Cause.Error()
		}
		return formatPathErr(reg, coer.Path, msg, coer.Source)
	case errors.As(err, &valid):
		msg := valid.Msg
		if valid.Rule != "" {
			msg = "[" + valid.Rule + "] " + msg
		}
		return formatPathErr(reg, valid.Path, msg, "")
	case errors.As(err, &immut):
		return formatPathErr(reg, immut.Path,
			fmt.Sprintf("immutable key changed: %q → %q", immut.Old, immut.New),
			"")
	case errors.As(err, &srcE):
		return fmt.Sprintf("source %q %s: %v", srcE.Source, srcE.Op, srcE.Cause)
	case errors.As(err, &parse):
		loc := parse.Source
		if parse.Path != "" {
			loc = parse.Path
		}
		return fmt.Sprintf("parse %s: %v", loc, parse.Cause)
	default:
		return err.Error()
	}
}

// formatPathErr is the shared shape for typed errors that carry a
// Path. The trailing "(from source X)" annotation lands when the
// error itself names a source and / or when the registry knows of
// other sources holding the same key.
func formatPathErr(reg *Registry, path Path, msg, source string) string {
	out := path.String() + ": " + msg
	annotation := buildProvenance(reg, path, source)
	if annotation != "" {
		out += " " + annotation
	}
	return out
}

// buildProvenance returns the "(from … sources: …)" trailing
// annotation. When source is non-empty it leads; when the registry
// is available the full SourceFor chain rides along so the operator
// sees both the winning source and the shadowed ones.
func buildProvenance(reg *Registry, path Path, source string) string {
	var parts []string
	if source != "" {
		parts = append(parts, "source "+quote(source))
	}
	if reg != nil {
		if snap := reg.state.snapshot.Load(); snap != nil {
			if all := snap.SourceFor(path); len(all) > 0 {
				parts = append(parts, "chain "+formatSourceList(all))
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, "; ") + ")"
}

// formatSourceList renders a source-chain slice as "[a, b, c]"
// without the bracket noise reflect-style formatting would emit.
func formatSourceList(srcs []string) string {
	quoted := make([]string, len(srcs))
	for i, s := range srcs {
		quoted[i] = quote(s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// quote wraps s in double quotes. Used by [formatSourceList] and
// [buildProvenance] for consistent output styling.
func quote(s string) string { return `"` + s + `"` }
