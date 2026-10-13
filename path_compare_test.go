package recon

import (
	"slices"
	"strings"
	"testing"
)

// pathFrom splits s into a Path on '/', so a fuzz input can spell any
// segments, empty ones and ones holding the delimiter or brackets too.
// "=" stands for the path of one empty segment.
func pathFrom(s string) Path {
	switch s {
	case "":
		return Path{}
	case "=":
		return Path{""}
	}
	return Path(strings.Split(s, "/"))
}

func FuzzComparePaths(f *testing.F) {
	for _, s := range [][2]string{
		{"a/b", "ab"}, {"a/b", "a-c"}, {"a", "a/b"}, {"", "x"}, {"", ""},
		{"a.b", "a/b"}, {"[x", "a"}, {"a/", "a"}, {"a//b", "a/"}, {"server/port", "server/tls/key"},
		{"", "/"}, {"=", ""}, {"=", "a"}, {"=", "/"}, {"a/b/c", "a/b"}, {"x]", "x/]"}, {"a~", "a/b"}, {"a!", "a/b"},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, x, y string) {
		a, b := pathFrom(x), pathFrom(y)
		want := strings.Compare(a.String(), b.String())
		if got := comparePaths(a, b); got != want {
			t.Fatalf("comparePaths(%q, %q) = %d, want %d (%q vs %q)", a, b, got, want, a.String(), b.String())
		}
	})
}

// keysBefore is MapSource.Keys as it was: every leaf path, each built
// by Path.Append, sorted by rendered string.
func keysBefore(m map[string]any) []Path {
	var out []Path
	var walk func(m map[string]any, prefix Path)
	walk = func(m map[string]any, prefix Path) {
		for k, v := range m {
			p := prefix.Append(k)
			if sub, ok := v.(map[string]any); ok {
				walk(sub, p)
				continue
			}
			out = append(out, p)
		}
	}
	walk(m, nil)
	slices.SortFunc(out, func(a, b Path) int { return strings.Compare(a.String(), b.String()) })
	return out
}

// mapFrom builds a nested map from s: each line is a '/'-separated
// path whose last segment holds a leaf, or, ending in '/', an empty
// map. A line that would turn a leaf into a map, or back, is skipped.
func mapFrom(s string) map[string]any {
	root := map[string]any{}
	for line := range strings.SplitSeq(s, "\n") {
		segs := strings.Split(line, "/")
		cur := root
		ok := true
		for _, seg := range segs[:len(segs)-1] {
			next, exists := cur[seg]
			if !exists {
				m := map[string]any{}
				cur[seg] = m
				cur = m
				continue
			}
			m, isMap := next.(map[string]any)
			if !isMap {
				ok = false
				break
			}
			cur = m
		}
		if !ok {
			continue
		}
		last := segs[len(segs)-1]
		if _, exists := cur[last]; !exists {
			cur[last] = line
		}
	}
	return root
}

func FuzzMapSourceKeys(f *testing.F) {
	for _, s := range []string{
		"", "a", "a/b\na/c\nd", "server/tls/key\nserver/port\nservers\nserver-x", "a.b/c\n[x]/y\n/\n//",
		"a/\nb/c/", "x/y/z/w\nx/y/q\nx/r",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		m := mapFrom(s)
		want := keysBefore(m)
		got := NewMapSource("m", m).Keys()
		if !slices.EqualFunc(got, want, Path.Equal) {
			t.Fatalf("Keys() = %q, want %q", got, want)
		}
		for i := range got {
			if cap(got[i]) != len(got[i]) {
				t.Fatalf("path %q shares spare capacity", got[i])
			}
		}
		if len(got) > 1 {
			got[0][len(got[0])-1] = "changed"
			_ = append(got[0], "extra")
			if !got[1].Equal(want[1]) {
				t.Fatalf("changing one path changed another: %q", got[1])
			}
		}
	})
}

func FuzzAppendCanonical(f *testing.F) {
	for _, s := range []string{"", "=", "a", "a/b", "a.b/c", "[x]]/y", "/", "a//b", "]"} {
		f.Add(s, "pre")
	}
	f.Fuzz(func(t *testing.T, s, pre string) {
		p := pathFrom(s)
		if got, want := string(p.AppendCanonical([]byte(pre))), pre+p.String(); got != want {
			t.Fatalf("AppendCanonical(%q) = %q, want %q", p, got, want)
		}
	})
}
