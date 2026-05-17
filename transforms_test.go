package recon

import (
	"slices"
	"testing"
)

func TestSnakeUpperTransform(t *testing.T) {
	cases := []struct {
		path Path
		want string
	}{
		{nil, ""},
		{Path{}, ""},
		{MakePath("server"), "SERVER"},
		{MakePath("server", "port"), "SERVER_PORT"},
		{MakePath("a", "b", "c"), "A_B_C"},
		{MakePath("Mixed", "Case"), "MIXED_CASE"},
	}
	for _, tc := range cases {
		if got := SnakeUpperTransform(tc.path); got != tc.want {
			t.Errorf("SnakeUpperTransform(%v)=%q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestSnakeUpperPrefixTransform(t *testing.T) {
	cases := []struct {
		prefix string
		path   Path
		want   string
	}{
		{"", MakePath("server", "port"), "SERVER_PORT"},
		{"APP_", MakePath("server", "port"), "APP_SERVER_PORT"},
		{"APP_", MakePath("port"), "APP_PORT"},
		{"APP_", Path{}, "APP_"},
		{"MYSERVICE__", MakePath("k"), "MYSERVICE__K"},
	}
	for _, tc := range cases {
		got := SnakeUpperPrefixTransform(tc.prefix)(tc.path)
		if got != tc.want {
			t.Errorf("SnakeUpperPrefixTransform(%q)(%v)=%q, want %q",
				tc.prefix, tc.path, got, tc.want)
		}
	}
}

func TestKebabTransform(t *testing.T) {
	cases := []struct {
		path Path
		want string
	}{
		{nil, ""},
		{MakePath("server"), "server"},
		{MakePath("server", "port"), "server-port"},
		{MakePath("a", "b", "c"), "a-b-c"},
	}
	for _, tc := range cases {
		if got := KebabTransform(tc.path); got != tc.want {
			t.Errorf("KebabTransform(%v)=%q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestDotTransform(t *testing.T) {
	cases := []struct {
		path Path
		want string
	}{
		{nil, ""},
		{MakePath("server"), "server"},
		{MakePath("server", "port"), "server.port"},
	}
	for _, tc := range cases {
		if got := DotTransform(tc.path); got != tc.want {
			t.Errorf("DotTransform(%v)=%q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestIdentityTransform(t *testing.T) {
	// IdentityTransform is DotTransform — verify the alias holds.
	p := MakePath("a", "b", "c")
	if IdentityTransform(p) != DotTransform(p) {
		t.Fatal("IdentityTransform diverges from DotTransform")
	}
}

func TestParseSnakeUpper(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		want   []string
	}{
		{"SERVER_PORT", "", []string{"server", "port"}},
		{"APP_SERVER_PORT", "APP_", []string{"server", "port"}},
		{"NOT_PREFIXED", "APP_", nil},
		{"APP_", "APP_", []string{}}, // empty after prefix-strip
		{"SINGLE", "", []string{"single"}},
	}
	for _, tc := range cases {
		got := parseSnakeUpper(tc.name, tc.prefix)
		gotSlice := []string(got)
		if !slices.Equal(gotSlice, tc.want) {
			t.Errorf("parseSnakeUpper(%q, %q)=%v, want %v",
				tc.name, tc.prefix, gotSlice, tc.want)
		}
	}
}
