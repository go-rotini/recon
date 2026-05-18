package recon

import (
	"context"
	"errors"
	"testing"
)

func TestWithDecodeLenient_OverridesRegistryStrict(t *testing.T) {
	r := newRegistry(t, WithStrict())
	if err := r.Set("known.key", "hi"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := r.Set("unknown.key", "ignored"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Strict at the registry level would surface unknown.key; the
	// per-call WithDecodeLenient flips it off.
	var c struct {
		Known string `recon:"known.key"`
	}
	if err := r.Bind(&c, WithDecodeLenient()); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Known != "hi" {
		t.Fatalf("got %q, want hi", c.Known)
	}
}

func TestWithDecodeStrict_OverridesRegistryLenient(t *testing.T) {
	r := newRegistry(t) // default lenient
	_ = r.Set("known.key", "hi")
	_ = r.Set("unknown.key", "x")
	var c struct {
		Known string `recon:"known.key"`
	}
	err := r.Bind(&c, WithDecodeStrict())
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("err = %v, want wrapping ErrUnknownKey", err)
	}
}

func TestWithDecodeTag_CustomTagName(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("port", 8080)

	type Config struct {
		Port int `mytag:"port"`
	}
	var c Config
	if err := r.Bind(&c, WithDecodeTag("mytag")); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Port != 8080 {
		t.Fatalf("got %d, want 8080", c.Port)
	}
}

// validatorCtxStruct is a Bind target whose ValidatorContext hook
// captures the context recon threads in.
type validatorCtxStruct struct {
	N int `recon:"n"`

	gotCtx context.Context
}

func (v *validatorCtxStruct) Validate(ctx context.Context) error {
	v.gotCtx = ctx
	return nil
}

func TestWithDecodeContext_ThreadsThrough(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("n", 7)

	type ctxKey struct{}
	parent := context.WithValue(context.Background(), ctxKey{}, "marker")

	var c validatorCtxStruct
	if err := r.Bind(&c, WithDecodeContext(parent)); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.gotCtx == nil {
		t.Fatal("Validate did not receive a context")
	}
	if c.gotCtx.Value(ctxKey{}) != "marker" {
		t.Fatal("Validate received a context without the parent's value")
	}
}
