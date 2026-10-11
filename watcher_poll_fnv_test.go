package recon

import (
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFNV64a_MatchesStdlib(t *testing.T) {
	for _, in := range []string{"", "a", "recon", strings.Repeat("config ", 1000)} {
		h := fnv.New64a()
		_, _ = h.Write([]byte(in))
		if got, want := fnv64a([]byte(in)), h.Sum64(); got != want {
			t.Errorf("fnv64a(%q) = %x, want %x", in, got, want)
		}
	}
}

func TestPollSnap_DetectsSameSizeSameMtimeRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(path, []byte("port: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	before, err := pollSnap(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("port: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	after, err := pollSnap(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.size != before.size || !after.mtime.Equal(before.mtime) {
		t.Fatalf("setup: size or mtime changed (%d→%d, %v→%v)", before.size, after.size, before.mtime, after.mtime)
	}
	if after.equal(before) {
		t.Fatal("a same-size, same-mtime content change was not detected")
	}
	again, err := pollSnap(path)
	if err != nil {
		t.Fatal(err)
	}
	if !again.equal(after) {
		t.Fatal("an unchanged file reported a change")
	}
}
