package recon

import "testing"

func TestSub_BindSecretAndUnsetUseViewKeys(t *testing.T) {
	r := newRegistry(t, WithSources(NewMapSource("m", map[string]any{
		"db": map[string]any{"pass": "p"},
	})))
	if err := r.Set("db.tok", "t"); err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Pass string `recon:"pass,secret"`
		Tok  string `recon:"tok,unset"`
	}
	if err := r.Sub("db").Bind(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Pass != "p" || cfg.Tok != "t" {
		t.Fatalf("bind = %+v", cfg)
	}
	if !r.IsSecret("db.pass") {
		t.Error("db.pass not marked secret")
	}
	if r.IsSecret("db.db.pass") {
		t.Error("db.db.pass marked secret")
	}
	if r.IsSet("db.tok") {
		t.Error("db.tok still set after unset")
	}
}
