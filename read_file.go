package recon

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"syscall"
)

// MaxFileSize caps every file recon reads: a [FileSource]'s or
// [FileSourceFS]'s file, piped standard input and a `fromFile` field's
// target. A larger file fails with [ErrFileTooLarge]
// instead of being read into memory.
const MaxFileSize int64 = 100 << 20

// fileMissing reports whether path names no file: stat fails because
// it, or a directory on the way, does not exist. Symlinks are followed.
// A file that can't be reached for another reason, such as a
// directory without search permission, is not missing.
func fileMissing(path string) bool {
	_, err := os.Stat(path)
	return err != nil && isNotExist(err)
}

// isNotExist reports whether err says a file or a directory on its
// path does not exist.
func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// readFileCapped reads path, refusing anything over [MaxFileSize]. Errors
// are [*fs.PathError]s, so errors.Is sees [fs.ErrNotExist] and
// [ErrFileTooLarge] through them.
func readFileCapped(path string) ([]byte, error) {
	data, _, err := readFileStamped(path)
	return data, err
}

// readFileStamped is [readFileCapped] that also stamps the file it
// opened, as it was before the read.
func readFileStamped(path string) ([]byte, fileStamp, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fileStamp{}, err //nolint:wrapcheck // *fs.PathError already names the path.
	}
	defer func() { _ = f.Close() }()
	var stamp fileStamp
	info, statErr := f.Stat()
	if statErr == nil {
		stamp = fileStamp{exists: true, size: info.Size(), mod: info.ModTime()}
	}
	// Refuse an oversized regular file before allocating; the limited
	// read below covers pipes and files that grow mid-read.
	if statErr == nil && info.Mode().IsRegular() && info.Size() > MaxFileSize {
		return nil, stamp, &fs.PathError{Op: "read", Path: path, Err: ErrFileTooLarge}
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, stamp, &fs.PathError{Op: "read", Path: path, Err: err}
	}
	if int64(len(data)) > MaxFileSize {
		return nil, stamp, &fs.PathError{Op: "read", Path: path, Err: ErrFileTooLarge}
	}
	return data, stamp, nil
}

// readAllCapped reads r to EOF, failing with [ErrFileTooLarge] past
// [MaxFileSize].
func readAllCapped(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxFileSize+1))
	if err != nil {
		return nil, err //nolint:wrapcheck // callers wrap with the input's name.
	}
	if int64(len(data)) > MaxFileSize {
		return nil, ErrFileTooLarge
	}
	return data, nil
}

// readFSFileCapped is [readFileCapped] for a file in fsys.
func readFSFileCapped(fsys fs.FS, path string) ([]byte, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, err //nolint:wrapcheck // *fs.PathError already names the path.
	}
	defer func() { _ = f.Close() }()
	if info, statErr := f.Stat(); statErr == nil && info.Mode().IsRegular() && info.Size() > MaxFileSize {
		return nil, &fs.PathError{Op: "read", Path: path, Err: ErrFileTooLarge}
	}
	data, err := readAllCapped(f)
	if err != nil {
		return nil, &fs.PathError{Op: "read", Path: path, Err: err}
	}
	return data, nil
}
