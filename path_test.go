package recon

import (
	"strings"
	"testing"
)

func TestMakePath(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want Path
	}{
		{"empty", nil, Path{}},
		{"single", []string{"a"}, Path{"a"}},
		{"multi", []string{"server", "port"}, Path{"server", "port"}},
		{"with-empty-seg", []string{"a", "", "b"}, Path{"a", "", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MakePath(c.in...)
			if !got.Equal(c.want) {
				t.Errorf("MakePath(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
	// Mutating the input slice must NOT alter the returned Path.
	in := []string{"a", "b"}
	p := MakePath(in...)
	in[0] = "z"
	if !p.Equal(Path{"a", "b"}) {
		t.Errorf("MakePath aliases its input: got %v", p)
	}
}

func TestParsePath(t *testing.T) {
	cases := []struct {
		in   string
		want Path
	}{
		{"", Path{}},
		{"a", Path{"a"}},
		{"server.port", Path{"server", "port"}},
		{"a.b.c", Path{"a", "b", "c"}},
		{"[my.key].sub", Path{"my.key", "sub"}},
		{"a.[b.c].d", Path{"a", "b.c", "d"}},
		{"[only.bracketed]", Path{"only.bracketed"}},
		{"trailing.", Path{"trailing", ""}},
		{".leading", Path{"", "leading"}},
		{"a..b", Path{"a", "", "b"}},
		// Unclosed bracket → treated as literal "[".
		{"[oops", Path{"[oops"}},
		// Bracket without delimiter content still works.
		{"[a]", Path{"a"}},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := ParsePath(c.in)
			if !got.Equal(c.want) {
				t.Errorf("ParsePath(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestPath_String(t *testing.T) {
	cases := []struct {
		in   Path
		want string
	}{
		{nil, ""},
		{Path{}, ""},
		{Path{"a"}, "a"},
		{Path{"server", "port"}, "server.port"},
		{Path{"my.key", "sub"}, "[my.key].sub"},
		{Path{"a", "b.c", "d"}, "a.[b.c].d"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.in, "/"), func(t *testing.T) {
			if got := c.in.String(); got != c.want {
				t.Errorf("%v.String() = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestPath_RoundTrip(t *testing.T) {
	// Every path that round-trips through String → ParsePath should equal
	// the original (modulo nil-vs-empty Path).
	cases := []Path{
		{"a"},
		{"server", "port"},
		{"my.key", "sub"},
		{"a", "b.c", "d"},
		{"only.bracketed"},
	}
	for _, p := range cases {
		t.Run(p.String(), func(t *testing.T) {
			round := ParsePath(p.String())
			if !round.Equal(p) {
				t.Errorf("round-trip: ParsePath(%q) = %v, want %v", p.String(), round, p)
			}
		})
	}
}

func TestPath_Equal(t *testing.T) {
	if !(Path{"a", "b"}).Equal(Path{"a", "b"}) {
		t.Error("equal paths not equal")
	}
	if (Path{"a"}).Equal(Path{"a", "b"}) {
		t.Error("different-length paths are equal")
	}
	if (Path{"a", "b"}).Equal(Path{"a", "c"}) {
		t.Error("different segments compare equal")
	}
	if !(Path{}).Equal(Path{}) {
		t.Error("empty paths not equal")
	}
}

func TestPath_HasPrefix(t *testing.T) {
	p := Path{"a", "b", "c"}
	if !p.HasPrefix(Path{}) {
		t.Error("empty prefix should match every Path")
	}
	if !p.HasPrefix(Path{"a"}) {
		t.Error("expected HasPrefix(a)")
	}
	if !p.HasPrefix(Path{"a", "b"}) {
		t.Error("expected HasPrefix(a,b)")
	}
	if !p.HasPrefix(p) {
		t.Error("path should be a prefix of itself")
	}
	if p.HasPrefix(Path{"x"}) {
		t.Error("unexpected HasPrefix(x)")
	}
	if p.HasPrefix(Path{"a", "b", "c", "d"}) {
		t.Error("longer prefix should not match")
	}
}

func TestPath_After(t *testing.T) {
	p := Path{"a", "b", "c"}
	if got := p.After(Path{}); !got.Equal(p) {
		t.Errorf("After(empty) = %v, want %v", got, p)
	}
	if got := p.After(Path{"a"}); !got.Equal(Path{"b", "c"}) {
		t.Errorf("After(a) = %v, want [b c]", got)
	}
	if got := p.After(Path{"a", "b"}); !got.Equal(Path{"c"}) {
		t.Errorf("After(a,b) = %v, want [c]", got)
	}
	if got := p.After(Path{"a", "b", "c"}); !got.Equal(Path{}) {
		t.Errorf("After(self) = %v, want []", got)
	}
	if got := p.After(Path{"x"}); got != nil {
		t.Errorf("After(non-prefix) = %v, want nil", got)
	}
	// After must return a fresh slice — mutating must not affect the source.
	p2 := Path{"a", "b", "c"}
	suffix := p2.After(Path{"a"})
	suffix[0] = "X"
	if p2[1] != "b" {
		t.Errorf("After returned aliased slice: p2[1]=%q after mutation", p2[1])
	}
}

func TestPath_Append(t *testing.T) {
	p := Path{"a", "b"}
	got := p.Append("c", "d")
	want := Path{"a", "b", "c", "d"}
	if !got.Equal(want) {
		t.Errorf("Append = %v, want %v", got, want)
	}
	// Receiver must not be mutated.
	if !p.Equal(Path{"a", "b"}) {
		t.Errorf("Append mutated receiver: %v", p)
	}
	// Append() with no args returns a clone.
	clone := p.Append()
	if !clone.Equal(p) {
		t.Errorf("Append() = %v, want %v", clone, p)
	}
}

func TestPath_Clone(t *testing.T) {
	p := Path{"a", "b"}
	c := p.Clone()
	if !c.Equal(p) {
		t.Errorf("Clone = %v, want %v", c, p)
	}
	c[0] = "X"
	if p[0] != "a" {
		t.Errorf("Clone aliases source: p[0]=%q after mutation", p[0])
	}
	// Empty Path clones to empty.
	if !(Path{}).Clone().Equal(Path{}) {
		t.Error("empty Path didn't clone to empty")
	}
}
