//go:build !unix

package recon

import (
	"io/fs"
	"os"
)

// keepOwner is a no-op where files have no POSIX owner.
func keepOwner(*os.File, fs.FileInfo) {}

// syncDir is a no-op where directories cannot be synced.
func syncDir(string) {}
