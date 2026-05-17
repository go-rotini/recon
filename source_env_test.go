package recon

import (
	"slices"
	"testing"
)

func TestOSEnvSource_NameAndClose(t *testing.T) {
	s := NewOSEnvSource()
	if s.Name() != "osenv" {
		t.Fatalf("Name()=%q, want osenv", s.Name())
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestOSEnvSource_PathProjectsToEnvVar verifies the canonical
// 12-factor mapping: a recon path of `server.port` reads the
// `SERVER_PORT` env var via [SnakeUpperTransform].
func TestOSEnvSource_PathProjectsToEnvVar(t *testing.T) {
	t.Setenv("SERVER_PORT", "8080")
	s := NewOSEnvSource()

	v, ok, err := s.Get(MakePath("server", "port"))
	if err != nil || !ok {
		t.Fatalf("Get(server.port) ok=%v err=%v", ok, err)
	}
	if got, _ := v.AsString(); got != "8080" {
		t.Fatalf("server.port=%q", got)
	}
}

// TestOSEnvSource_PrefixedPathProjects verifies WithEnvPrefix
// composes into the transform — Path{"port"} reads APP_PORT.
func TestOSEnvSource_PrefixedPathProjects(t *testing.T) {
	t.Setenv("APP_PORT", "8080")
	t.Setenv("APP_SERVER_HOST", "localhost")
	s := NewOSEnvSource(WithEnvPrefix("APP_"))

	v, ok, _ := s.Get(MakePath("port"))
	if !ok {
		t.Fatal("port not resolved through APP_PORT")
	}
	if got, _ := v.AsString(); got != "8080" {
		t.Fatalf("port=%q", got)
	}

	v, ok, _ = s.Get(MakePath("server", "host"))
	if !ok {
		t.Fatal("server.host not resolved through APP_SERVER_HOST")
	}
	if got, _ := v.AsString(); got != "localhost" {
		t.Fatalf("server.host=%q", got)
	}
}

// TestOSEnvSource_PrefixedKeysOnly verifies unprefixed env vars
// do NOT surface when a prefix is configured.
func TestOSEnvSource_PrefixedKeysOnly(t *testing.T) {
	t.Setenv("APP_PORT", "8080")
	t.Setenv("UNRELATED_THING", "whatever")
	s := NewOSEnvSource(WithEnvPrefix("APP_"))

	if _, ok, _ := s.Get(MakePath("unrelated", "thing")); ok {
		t.Fatal("UNRELATED_THING visible despite prefix filter")
	}
}

// TestOSEnvSource_KeysEnumeratesEnv verifies Keys() returns paths
// projected from the matching env vars (snake-upper inverse).
func TestOSEnvSource_KeysEnumeratesEnv(t *testing.T) {
	t.Setenv("OSENV_TEST_A", "1")
	t.Setenv("OSENV_TEST_B", "2")
	s := NewOSEnvSource(WithEnvPrefix("OSENV_TEST_"))

	keys := s.Keys()
	got := make([]string, len(keys))
	for i, k := range keys {
		got[i] = k.String()
	}
	if !slices.IsSorted(got) {
		t.Fatalf("Keys not sorted: %v", got)
	}
	// Path{"a"} and Path{"b"} should both surface (lowercased).
	want := map[string]bool{"a": false, "b": false}
	for _, g := range got {
		if _, exists := want[g]; exists {
			want[g] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("missing %q in Keys()=%v", k, got)
		}
	}
}

// TestOSEnvSource_CustomTransformAndParser verifies a caller can
// override the default snake-upper convention for a non-standard
// env-var-naming scheme.
func TestOSEnvSource_CustomTransformAndParser(t *testing.T) {
	// Treat the env var as a verbatim, case-preserving lookup.
	t.Setenv("server.port", "8080")
	s := NewOSEnvSource(
		WithEnvTransform(IdentityTransform),
		WithEnvKeyParser(ParsePath),
	)

	v, ok, _ := s.Get(MakePath("server", "port"))
	if !ok {
		t.Fatal("identity-transform path not resolved")
	}
	if got, _ := v.AsString(); got != "8080" {
		t.Fatalf("server.port=%q", got)
	}
}

func TestOSEnvSource_RefreshPicksUpNewEnv(t *testing.T) {
	s := NewOSEnvSource(WithEnvPrefix("OSENV_REFRESH_"))
	// Establish the cache empty.
	if keys := s.Keys(); len(keys) > 0 {
		// Other matching env vars may exist; we only assert our key
		// is initially absent.
		for _, k := range keys {
			if k.String() == "new" {
				t.Fatal("OSENV_REFRESH_NEW set before test")
			}
		}
	}

	t.Setenv("OSENV_REFRESH_NEW", "v")
	// Cached, so still empty (or stale).
	cached := s.Keys()
	for _, k := range cached {
		if k.String() == "new" {
			t.Fatal("cache reflected env mutation without Refresh")
		}
	}

	_ = s.Refresh()
	keys := s.Keys()
	saw := false
	for _, k := range keys {
		if k.String() == "new" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("Refresh did not pick up OSENV_REFRESH_NEW; keys=%v", keys)
	}
}

func TestOSEnvSource_IntegrationWithRegistry(t *testing.T) {
	t.Setenv("APP_SERVER_PORT", "8080")
	s := NewOSEnvSource(WithEnvPrefix("APP_"))
	r := newRegistry(t, WithSource(s))

	if v, _, _ := r.GetString("server.port"); v != "8080" {
		t.Fatalf("server.port via registry=%q", v)
	}
	// Env-var values are always StringKind; numeric coercion happens
	// at the Bind / Get[int] call site.
	v, ok, _ := r.Get("server.port")
	if !ok || v.Kind() != StringKind {
		t.Fatalf("server.port kind=%v ok=%v, want StringKind", v.Kind(), ok)
	}
}
