//go:build unix

package recon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// SaveTo keeps the group of the file it replaces.
func TestSaveTo_KeepsGroup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "c.json")
	if err := os.WriteFile(target, []byte("{}"), 0o640); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	have := info.Sys().(*syscall.Stat_t).Gid
	groups, _ := os.Getgroups()
	want := -1
	for _, g := range groups {
		if uint32(g) != have {
			want = g
			break
		}
	}
	if want < 0 {
		t.Skip("needs membership of a second group")
	}
	if err := os.Chown(target, -1, want); err != nil {
		t.Skipf("chgrp: %v", err)
	}
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("m", map[string]any{"a": 1})))
	if err := r.SaveTo(target); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Sys().(*syscall.Stat_t).Gid; int(got) != want || info.Mode().Perm() != 0o640 {
		t.Fatalf("gid %d mode %v, want gid %d mode 0640", got, info.Mode().Perm(), want)
	}
}
