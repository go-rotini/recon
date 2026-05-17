package recon

import (
	"strings"
	"testing"
)

func TestSnapshot_StringRedactsSecrets(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("public", "hello")
	_ = r.Set("token", "hunter2")
	r.MarkSecret("token")

	out := r.Snapshot().String()
	if strings.Contains(out, "hunter2") {
		t.Fatalf("Snapshot.String leaked secret: %s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("public value missing: %s", out)
	}
	if !strings.Contains(out, "***") {
		t.Fatalf("redaction marker missing: %s", out)
	}
}

func TestSnapshot_StringUsesCustomRedactor(t *testing.T) {
	r := newRegistry(t,
		WithSecretRedactor(func(string) string { return "<HIDDEN>" }),
	)
	_ = r.Set("token", "hunter2")
	r.MarkSecret("token")

	out := r.Snapshot().String()
	if !strings.Contains(out, "<HIDDEN>") {
		t.Fatalf("custom redactor not used: %s", out)
	}
	if strings.Contains(out, "hunter2") {
		t.Fatalf("Snapshot.String leaked secret: %s", out)
	}
}

func TestSnapshot_IsSecretReportsFrozenState(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("token", "hunter2")
	r.MarkSecret("token")

	snap := r.Snapshot()
	if !snap.IsSecret(MakePath("token")) {
		t.Fatal("Snapshot.IsSecret false for marked key")
	}
	if snap.IsSecret(MakePath("other")) {
		t.Fatal("Snapshot.IsSecret true for unmarked key")
	}

	// MarkSecret AFTER the snapshot was taken should NOT affect the
	// snapshot's frozen view — the snapshot's secret set is captured
	// at build time.
	r.MarkSecret("other")
	if snap.IsSecret(MakePath("other")) {
		t.Fatal("Snapshot.IsSecret saw a later MarkSecret — snapshot should be frozen")
	}
}

func TestSnapshot_NilSnapshotIsSecretSafe(t *testing.T) {
	var s *Snapshot
	if s.IsSecret(MakePath("k")) {
		t.Fatal("nil Snapshot.IsSecret returned true")
	}
}

func TestCoercionError_RedactsCauseWhenSecret(t *testing.T) {
	type C struct {
		Token int `recon:"token,secret"`
	}
	r := newRegistry(t)
	// Coerce a non-int string under a secret tag — the typical
	// strconv error message ends with `parsing "<value>": invalid syntax`,
	// which would leak hunter2 verbatim. Secret=true suppresses it.
	_ = r.Set("token", "hunter2")

	var c C
	err := r.Bind(&c)
	if err == nil {
		t.Fatal("expected coercion error")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("CoercionError leaked secret cause: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("redaction marker missing: %s", err.Error())
	}
}

func TestValidationError_RedactsMsgWhenSecret(t *testing.T) {
	schema := []byte(`{
		"type": "object",
		"properties": {
			"token": {"type": "string", "pattern": "^[a-z]+$"}
		}
	}`)
	r, err := New(WithSchema(schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	r.MarkSecret("token")
	// Set a value that violates the pattern; expect the bound
	// ValidationError to redact the value.
	setErr := r.Set("token", "Mixed-Case")
	if setErr == nil {
		t.Fatal("expected validation error")
	}
	// The validator's message would normally quote the offending
	// value; with Secret=true on the ValidationError, Msg is
	// "[redacted]".
	if strings.Contains(setErr.Error(), "Mixed-Case") {
		t.Fatalf("ValidationError leaked secret value: %s", setErr.Error())
	}
	if !strings.Contains(setErr.Error(), "[redacted]") {
		t.Fatalf("redaction marker missing: %s", setErr.Error())
	}
}
