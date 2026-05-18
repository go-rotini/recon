package recon

import (
	"errors"
	"fmt"
	"strings"
)

// FormatError renders err into a multi-line, human-readable summary.
// Entries in a [*MultiError] become separate lines; typed errors that
// carry a [Path] are formatted with their path leading and
// source-provenance trailing in parentheses.
//
// reg is optional. When non-nil, FormatError consults its current
// snapshot to surface the precedence chain alongside each failing
// path. Returns "" when err is nil so it composes cleanly with
// log.Println.
//
// Pass color=true to opt into ANSI colorization. Callers that always
// want color can use [FormatErrorColor].
func FormatError(reg *Registry, err error, color ...bool) string {
	if err == nil {
		return ""
	}
	wantColor := len(color) > 0 && color[0]
	var multi *MultiError
	if errors.As(err, &multi) && multi != nil && len(multi.Errors) > 0 {
		var b strings.Builder
		header := fmt.Sprintf("recon: %d errors:", len(multi.Errors))
		b.WriteString(applyColor(header, wantColor, ansiBold))
		b.WriteByte('\n')
		for _, sub := range multi.Errors {
			b.WriteString("  ")
			b.WriteString(applyColor("•", wantColor, ansiRed))
			b.WriteByte(' ')
			b.WriteString(formatOneError(reg, sub))
			b.WriteByte('\n')
		}
		return strings.TrimRight(b.String(), "\n")
	}
	return applyColor("recon:", wantColor, ansiRed) + " " + formatOneError(reg, err)
}

// FormatErrorColor is [FormatError] with ANSI colorization always on.
func FormatErrorColor(reg *Registry, err error) string {
	return FormatError(reg, err, true)
}

func applyColor(s string, enabled bool, code string) string {
	if !enabled {
		return s
	}
	return code + s + ansiReset
}

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiRed   = "\x1b[31m"
)

// formatOneError formats a single error. Typed errors that carry a
// Path or Source field get extra context; others fall back to
// err.Error().
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

// formatPathErr formats a typed error with its path leading and any
// source-provenance trailing.
func formatPathErr(reg *Registry, path Path, msg, source string) string {
	out := path.String() + ": " + msg
	annotation := buildProvenance(reg, path, source)
	if annotation != "" {
		out += " " + annotation
	}
	return out
}

// buildProvenance returns the trailing "(from … sources: …)"
// annotation. The error's own Source leads; when reg is available the
// full SourceFor chain rides along.
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

func formatSourceList(srcs []string) string {
	quoted := make([]string, len(srcs))
	for i, s := range srcs {
		quoted[i] = quote(s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func quote(s string) string { return `"` + s + `"` }
