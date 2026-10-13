//go:build unix

package recon

import (
	"io/fs"
	"os"
	"syscall"
)

// keepOwner gives tmp the owner and group of the file info describes.
// It is best effort: a caller not allowed to change them (EPERM) keeps
// its own.
func keepOwner(tmp *os.File, info fs.FileInfo) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	if err := tmp.Chown(int(st.Uid), int(st.Gid)); err != nil {
		return
	}
}

// syncDir flushes dir's entries to disk, best effort, so a rename into
// it survives a crash.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return
	}
}
