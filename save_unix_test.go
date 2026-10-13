//go:build unix

package recon

import (
	"io/fs"
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

type ownerInfo struct {
	fs.FileInfo
	st *syscall.Stat_t
}

func (o ownerInfo) Sys() any { return o.st }

// When the owner can't be kept, the group still is.
func TestKeepOwner_GroupWhenOwnerDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may change the owner")
	}
	tmp, err := os.CreateTemp(t.TempDir(), "c")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tmp.Close() }()
	info, err := tmp.Stat()
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
	owner := uint32(os.Geteuid() + 1) // an owner this process may not give
	keepOwner(tmp, ownerInfo{info, &syscall.Stat_t{Uid: owner, Gid: uint32(want)}})
	info, err = tmp.Stat()
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if int(st.Gid) != want || int(st.Uid) != os.Geteuid() {
		t.Fatalf("uid %d gid %d, want uid %d gid %d", st.Uid, st.Gid, os.Geteuid(), want)
	}
}
