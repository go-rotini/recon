package recon

import (
	"strings"
	"testing"

	"github.com/go-rotini/env"
)

func TestSecret_AliasIdentity(t *testing.T) {
	// recon.Secret[T] is a type alias for env.Secret[T]; constructing one
	// from either side must produce an interchangeable value.
	s := NewSecret("hunter2")
	if _, ok := any(s).(env.Secret[string]); !ok {
		t.Errorf("recon.Secret[string] is not assignable to env.Secret[string]")
	}
}

func TestSecret_ValueRedaction(t *testing.T) {
	s := NewSecret("hunter2")
	// The String() method MUST redact — the only safe thing to log.
	if got := s.String(); strings.Contains(got, "hunter2") {
		t.Errorf("Secret leaked value via String(): %q", got)
	}
}

func TestSecret_AcceptsEnvSecret(t *testing.T) {
	// The reverse direction: an env.Secret[T] should be usable wherever a
	// recon.Secret[T] is.
	es := env.NewSecret(42)
	var rs Secret[int] = es
	if rs.Value() != 42 {
		t.Errorf("aliased Secret lost its Value: %d", rs.Value())
	}
}
