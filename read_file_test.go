package recon

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileCapped(t *testing.T) {
	dir := t.TempDir()

	small := filepath.Join(dir, "small.yaml")
	if err := writeFile(small, []byte("k: v\n")); err != nil {
		t.Fatal(err)
	}
	data, err := readFileCapped(small)
	if err != nil || string(data) != "k: v\n" {
		t.Fatalf("readFileCapped = %q, %v", data, err)
	}

	if _, err := readFileCapped(filepath.Join(dir, "missing.yaml")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file err = %v, want fs.ErrNotExist", err)
	}

	// A sparse file one byte over the cap: refused before it is read.
	big := filepath.Join(dir, "big.yaml")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxFileSize + 1); err != nil {
		_ = f.Close()
		t.Skipf("cannot create a sparse file: %v", err)
	}
	_ = f.Close()
	_, err = readFileCapped(big)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("oversized file err = %v, want ErrFileTooLarge", err)
	}
	var pe *fs.PathError
	if !errors.As(err, &pe) || pe.Path != big {
		t.Errorf("oversized file err = %v, want a *fs.PathError naming the file", err)
	}
	if _, err := NewFileSource(big); !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("NewFileSource err = %v, want ErrFileTooLarge", err)
	}
}
