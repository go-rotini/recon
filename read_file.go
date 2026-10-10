package recon

import (
	"io"
	"io/fs"
	"os"
)

// MaxFileSize caps every file recon reads: a [FileSource]'s file and a
// `fromFile` field's target. A larger file fails with [ErrFileTooLarge]
// instead of being read into memory.
const MaxFileSize int64 = 100 << 20

// fileExists reports whether path exists. Symlinks are followed.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readFileCapped reads path, refusing anything over [MaxFileSize]. Errors
// are [*fs.PathError]s, so errors.Is sees [fs.ErrNotExist] and
// [ErrFileTooLarge] through them.
func readFileCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err //nolint:wrapcheck // *fs.PathError already names the path.
	}
	defer func() { _ = f.Close() }()
	// Refuse an oversized regular file before allocating; the limited
	// read below covers pipes and files that grow mid-read.
	if info, statErr := f.Stat(); statErr == nil && info.Mode().IsRegular() && info.Size() > MaxFileSize {
		return nil, &fs.PathError{Op: "read", Path: path, Err: ErrFileTooLarge}
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, &fs.PathError{Op: "read", Path: path, Err: err}
	}
	if int64(len(data)) > MaxFileSize {
		return nil, &fs.PathError{Op: "read", Path: path, Err: ErrFileTooLarge}
	}
	return data, nil
}
