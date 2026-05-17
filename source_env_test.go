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

func TestOSEnvSource_GetReadsEnvVar(t *testing.T) {
	t.Setenv("RECON_TEST_KEY", "value-1")
	s := NewOSEnvSource()
	v, ok, err := s.Get(MakePath("RECON_TEST_KEY"))
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got, _ := v.AsString(); got != "value-1" {
		t.Fatalf("RECON_TEST_KEY=%q", got)
	}
}

func TestOSEnvSource_GetMissing(t *testing.T) {
	s := NewOSEnvSource()
	if _, ok, _ := s.Get(MakePath("RECON_DEFINITELY_UNSET_XYZZY")); ok {
		t.Fatal("unset var ok=true")
	}
}

func TestOSEnvSource_GetMultiSegmentPathReturnsMiss(t *testing.T) {
	t.Setenv("RECON_TEST_KEY", "v")
	s := NewOSEnvSource()
	if _, ok, _ := s.Get(MakePath("RECON", "TEST", "KEY")); ok {
		t.Fatal("multi-segment path resolved; env keys are flat")
	}
}

func TestOSEnvSource_PrefixFilter(t *testing.T) {
	t.Setenv("RECON_TEST_FILTER_A", "1")
	t.Setenv("RECON_TEST_FILTER_B", "2")
	t.Setenv("UNRELATED_VAR", "x")

	s := NewOSEnvSource(WithEnvPrefix("RECON_TEST_FILTER_"))
	if _, ok, _ := s.Get(MakePath("UNRELATED_VAR")); ok {
		t.Fatal("UNRELATED_VAR visible through prefix filter")
	}
	if _, ok, _ := s.Get(MakePath("RECON_TEST_FILTER_A")); !ok {
		t.Fatal("prefixed key not visible")
	}

	keys := s.Keys()
	for _, k := range keys {
		ks := k.String()
		if ks == "UNRELATED_VAR" {
			t.Fatal("UNRELATED_VAR in Keys() with prefix filter")
		}
	}
}

func TestOSEnvSource_KeysCachedUntilRefresh(t *testing.T) {
	t.Setenv("RECON_KEYS_CACHE", "1")
	s := NewOSEnvSource(WithEnvPrefix("RECON_KEYS_"))
	keys1 := s.Keys()

	t.Setenv("RECON_KEYS_NEW", "2")
	// Cached — new var not yet visible.
	keys2 := s.Keys()
	if len(keys1) != len(keys2) {
		t.Fatalf("Keys() changed without Refresh: %d → %d", len(keys1), len(keys2))
	}

	_ = s.Refresh()
	keys3 := s.Keys()
	if len(keys3) <= len(keys1) {
		t.Fatalf("Refresh didn't pick up new var: %d → %d", len(keys1), len(keys3))
	}
}

func TestOSEnvSource_KeysSortedAndFiltered(t *testing.T) {
	t.Setenv("RECON_SORT_CCC", "3")
	t.Setenv("RECON_SORT_AAA", "1")
	t.Setenv("RECON_SORT_BBB", "2")
	s := NewOSEnvSource(WithEnvPrefix("RECON_SORT_"))
	keys := s.Keys()
	got := make([]string, len(keys))
	for i, k := range keys {
		got[i] = k.String()
	}
	if !slices.IsSorted(got) {
		t.Fatalf("Keys()=%v, want sorted", got)
	}
	for _, k := range got {
		if len(k) < len("RECON_SORT_") || k[:len("RECON_SORT_")] != "RECON_SORT_" {
			t.Fatalf("Keys contains unfiltered %q", k)
		}
	}
}

func TestOSEnvSource_IntegrationWithRegistry(t *testing.T) {
	t.Setenv("RECON_REG_INT_PORT", "8080")
	s := NewOSEnvSource(WithEnvPrefix("RECON_REG_INT_"))
	r := newRegistry(t, WithSource(s))
	if v, _, _ := r.GetString("RECON_REG_INT_PORT"); v != "8080" {
		t.Fatalf("PORT via registry=%q", v)
	}
	// Env-var values are always strings on the wire; numeric coercion
	// from a string Value lands with the Bind / Phase-6 struct decoder.
	// Confirm here that the value's kind is StringKind so a Phase-6
	// reader knows what to coerce from.
	v, ok, _ := r.Get("RECON_REG_INT_PORT")
	if !ok || v.Kind() != StringKind {
		t.Fatalf("PORT kind=%v ok=%v, want StringKind", v.Kind(), ok)
	}
}
