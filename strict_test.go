package recon

import (
	"errors"
	"testing"
)

func TestStrict_UnknownKeysReportedPerKey(t *testing.T) {
	type C struct {
		Port int `recon:"port"`
	}
	r := newRegistry(t)
	_ = r.Set("port", 8080)
	_ = r.Set("extra", "hello") // not bound by C

	var c C
	err := r.Bind(&c, WithDecodeStrict())
	if err == nil {
		t.Fatal("expected error under strict mode")
	}
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("err=%v, want wrap of ErrUnknownKey", err)
	}

	var ue *UnknownKeyError
	if !errors.As(err, &ue) {
		t.Fatalf("err=%v, want *UnknownKeyError", err)
	}
	if ue.Path.String() != "extra" {
		t.Fatalf("UnknownKey path=%s, want extra", ue.Path)
	}
}

func TestStrict_AllKnownKeysAccepted(t *testing.T) {
	type C struct {
		Port int    `recon:"port"`
		Host string `recon:"host"`
	}
	r := newRegistry(t)
	_ = r.Set("port", 8080)
	_ = r.Set("host", "localhost")

	var c C
	if err := r.Bind(&c, WithDecodeStrict()); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Port != 8080 || c.Host != "localhost" {
		t.Fatalf("c=%+v", c)
	}
}

func TestStrict_NestedStructAccountsForNestedKeys(t *testing.T) {
	type Server struct {
		Host string `recon:"host"`
		Port int    `recon:"port"`
	}
	type C struct {
		Server Server `recon:"server"`
	}
	r := newRegistry(t)
	_ = r.Set("server.host", "localhost")
	_ = r.Set("server.port", 8080)

	var c C
	if err := r.Bind(&c, WithDecodeStrict()); err != nil {
		t.Fatalf("Bind: %v", err)
	}
}

func TestStrict_AliasCountsAsConsulted(t *testing.T) {
	type C struct {
		Port int `recon:"port,aliases=server.port"`
	}
	r := newRegistry(t)
	_ = r.Set("server.port", 8080) // alias is what the source provides

	var c C
	if err := r.Bind(&c, WithDecodeStrict()); err != nil {
		t.Fatalf("Bind: %v", err)
	}
}

func TestStrict_OptionPropagatesFromRegistry(t *testing.T) {
	type C struct {
		Port int `recon:"port"`
	}
	r := newRegistry(t, WithStrict())
	_ = r.Set("port", 8080)
	_ = r.Set("extra", "x")

	var c C
	err := r.Bind(&c)
	if err == nil {
		t.Fatal("expected error under registry-level WithStrict")
	}
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("err=%v, want wrap of ErrUnknownKey", err)
	}
}

func TestStrict_DisabledByDefault(t *testing.T) {
	type C struct {
		Port int `recon:"port"`
	}
	r := newRegistry(t)
	_ = r.Set("port", 8080)
	_ = r.Set("extra", "ignored")

	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
}

func TestStrict_SubViewScopedToPrefix(t *testing.T) {
	type C struct {
		Port int `recon:"port"`
	}
	r := newRegistry(t)
	_ = r.Set("server.port", 8080)
	_ = r.Set("db.dsn", "postgres") // outside server.*; should NOT trigger

	var c C
	if err := r.Sub("server").Bind(&c, WithDecodeStrict()); err != nil {
		t.Fatalf("Sub.Bind: %v", err)
	}
}
