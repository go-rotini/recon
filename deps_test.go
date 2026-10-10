package recon

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestRootPackageDependencies keeps network, archive, template and
// Go-toolchain packages, and go-rotini/fs, out of the root package, so a
// program that only reads configuration doesn't link them. The
// notification-based watcher lives in the fswatch subpackage. Each entry
// also forbids the packages below it.
func TestRootPackageDependencies(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	forbidden := []string{
		"net/http", "crypto/tls", "crypto/x509",
		"go", "archive", "compress/gzip", "text/template",
		"github.com/go-rotini/fs",
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		cmd := exec.Command("go", "list", "-deps", ".")
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=amd64", "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list -deps (GOOS=%s): %v", goos, err)
		}
		for _, dep := range strings.Fields(string(out)) {
			for _, pkg := range forbidden {
				if dep == pkg || strings.HasPrefix(dep, pkg+"/") {
					t.Errorf("GOOS=%s: the root package depends on %s", goos, dep)
				}
			}
		}
	}
}
