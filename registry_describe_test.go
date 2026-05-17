package recon

import (
	"slices"
	"strings"
	"testing"
)

func TestDescribe_EmptyRegistry(t *testing.T) {
	r := newRegistry(t)
	d := r.Describe()
	if len(d.Keys) != 0 {
		t.Fatalf("Describe on empty registry returned %d keys", len(d.Keys))
	}
}

func TestDescribe_ListsKeysSortedByPath(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("zebra", "z")
	_ = r.Set("alpha.beta", 1)
	_ = r.Set("middle", true)

	d := r.Describe()
	got := make([]string, len(d.Keys))
	for i, k := range d.Keys {
		got[i] = k.Path.String()
	}
	want := []string{"alpha.beta", "middle", "zebra"}
	if !slices.Equal(got, want) {
		t.Fatalf("Keys=%v, want %v", got, want)
	}
}

func TestDescribe_PopulatesSourceProvenance(t *testing.T) {
	high := NewMapSource("high", map[string]any{"k": "from-high"})
	low := NewMapSource("low", map[string]any{"k": "from-low"})
	r := newRegistry(t, WithSources(high, low))

	d := r.Describe()
	if len(d.Keys) != 1 {
		t.Fatalf("Keys=%d, want 1", len(d.Keys))
	}
	k := d.Keys[0]
	if k.Source != "high" {
		t.Fatalf("winner Source=%q, want high", k.Source)
	}
	if !slices.Equal(k.Sources, []string{"high", "low"}) {
		t.Fatalf("Sources=%v, want [high low]", k.Sources)
	}
	if k.Value != "from-high" {
		t.Fatalf("Value=%q", k.Value)
	}
}

func TestDescribe_RedactsSecretValues(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("token", "hunter2")
	r.MarkSecret("token")

	d := r.Describe()
	if len(d.Keys) != 1 {
		t.Fatalf("Keys=%d", len(d.Keys))
	}
	k := d.Keys[0]
	if !k.Secret {
		t.Fatal("Secret=false for marked key")
	}
	if strings.Contains(k.Value, "hunter2") {
		t.Fatalf("Value=%q leaks secret", k.Value)
	}
}

func TestDescribe_CustomSecretRedactor(t *testing.T) {
	r := newRegistry(t,
		WithSecretRedactor(func(string) string { return "<HIDDEN>" }),
	)
	_ = r.Set("token", "hunter2")
	r.MarkSecret("token")

	d := r.Describe()
	if d.Keys[0].Value != "<HIDDEN>" {
		t.Fatalf("Value=%q, want <HIDDEN>", d.Keys[0].Value)
	}
}

func TestDescribe_AliasesAttachedToCanonical(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("server.port", 8080)
	if err := r.RegisterAlias("port", "server.port"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterAlias("p", "server.port"); err != nil {
		t.Fatal(err)
	}

	d := r.Describe()
	// Exactly one canonical row for server.port; the aliases appear
	// in its Aliases field (no duplicate row per alias).
	var canon *KeyDescription
	for i := range d.Keys {
		if d.Keys[i].Path.String() == "server.port" {
			canon = &d.Keys[i]
		}
	}
	if canon == nil {
		t.Fatalf("server.port row missing; got keys=%v", keyPaths(d))
	}
	aliasStrs := make([]string, len(canon.Aliases))
	for i, a := range canon.Aliases {
		aliasStrs[i] = a.String()
	}
	slices.Sort(aliasStrs)
	if !slices.Equal(aliasStrs, []string{"p", "port"}) {
		t.Fatalf("Aliases=%v, want [p port]", aliasStrs)
	}
}

func TestDescribeKey_LooksUpCanonical(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("server.port", 8080)

	k, ok := r.DescribeKey("server.port")
	if !ok {
		t.Fatal("DescribeKey(server.port) not found")
	}
	if k.Path.String() != "server.port" {
		t.Fatalf("Path=%s", k.Path)
	}
}

func TestDescribeKey_ResolvesAlias(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("server.port", 8080)
	if err := r.RegisterAlias("port", "server.port"); err != nil {
		t.Fatal(err)
	}

	k, ok := r.DescribeKey("port")
	if !ok {
		t.Fatal("DescribeKey via alias not found")
	}
	if k.Path.String() != "server.port" {
		t.Fatalf("Path=%s, want canonical server.port", k.Path)
	}
}

func TestDescribeKey_NotFound(t *testing.T) {
	r := newRegistry(t)
	if _, ok := r.DescribeKey("missing"); ok {
		t.Fatal("DescribeKey(missing) ok=true")
	}
}

func TestMarkSecret_SetsAndIsSecretReports(t *testing.T) {
	r := newRegistry(t)
	r.MarkSecret("token")
	if !r.IsSecret("token") {
		t.Fatal("IsSecret(token)=false after MarkSecret")
	}
	if r.IsSecret("other") {
		t.Fatal("IsSecret(other)=true without MarkSecret")
	}
}

func TestMarkSecret_EmptyKeyIgnored(t *testing.T) {
	r := newRegistry(t)
	r.MarkSecret("") // must not poison the secret set
	r.MarkSecret("k")
	d := r.Describe()
	for _, k := range d.Keys {
		// No key should be marked secret by virtue of the empty
		// call — the only legitimate marker is "k".
		if k.Secret && k.Path.String() != "k" {
			t.Fatalf("spurious secret=%v", k.Path)
		}
	}
}

func TestBind_SecretTagMarksKeyForDescribe(t *testing.T) {
	type C struct {
		Token string `recon:"token,secret"`
	}
	r := newRegistry(t)
	_ = r.Set("token", "hunter2")
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if !r.IsSecret("token") {
		t.Fatal("secret tag did not propagate to registry")
	}
	k, _ := r.DescribeKey("token")
	if strings.Contains(k.Value, "hunter2") {
		t.Fatalf("Describe leaked secret: %q", k.Value)
	}
}

func TestDescribe_OnClosedRegistry(t *testing.T) {
	r, _ := New()
	_ = r.Close()
	d := r.Describe()
	if len(d.Keys) != 0 {
		t.Fatalf("closed registry returned %d keys", len(d.Keys))
	}
	if _, ok := r.DescribeKey("anything"); ok {
		t.Fatal("DescribeKey on closed registry returned ok=true")
	}
}

func TestDescribe_SubRegistryPrefixIsAppliedByMarkSecret(t *testing.T) {
	// A sub-view's MarkSecret should mark the prefixed canonical key.
	r := newRegistry(t)
	_ = r.Set("server.token", "x")
	r.Sub("server").MarkSecret("token")
	if !r.IsSecret("server.token") {
		t.Fatal("sub-view MarkSecret did not prefix the path")
	}
}

// keyPaths is a tiny helper for failure diagnostics.
func keyPaths(d Description) []string {
	out := make([]string, len(d.Keys))
	for i, k := range d.Keys {
		out[i] = k.Path.String()
	}
	return out
}
