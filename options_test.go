package recon

import (
	"bytes"
	"log/slog"
	"testing"
)

func TestWithLenient_OverridesStrict(t *testing.T) {
	r := newRegistry(t, WithStrict(), WithLenient())
	if r.state.opts.strict {
		t.Fatal("strict still set after WithLenient")
	}
}

func TestWithErrorBehavior(t *testing.T) {
	r := newRegistry(t, WithErrorBehavior(FailFast))
	if r.state.opts.errorBehavior != FailFast {
		t.Fatalf("errorBehavior = %v, want FailFast", r.state.opts.errorBehavior)
	}
}

func TestWithCodec_RegistersAndUsable(t *testing.T) {
	// Use a fresh codec set so the test isn't sensitive to bundled
	// defaults. Re-registering JSON under its canonical name verifies
	// the codec is actually wired in.
	r := newRegistry(t, WithCodec(JSON))
	c, ok := r.state.opts.codecs.ByName(FormatJSON)
	if !ok {
		t.Fatal("JSON codec not registered after WithCodec")
	}
	if c.Name() != FormatJSON {
		t.Fatalf("codec name = %q, want %q", c.Name(), FormatJSON)
	}
}

func TestWithCodec_Nil(t *testing.T) {
	// nil codec is a documented no-op; registry must still construct.
	r := newRegistry(t, WithCodec(nil))
	if r.state.opts.codecs == nil {
		t.Fatal("codecs set should still exist after nil WithCodec")
	}
}

func TestWithoutCodec_RemovesByName(t *testing.T) {
	r := newRegistry(t, WithoutCodec(FormatYAML))
	if _, ok := r.state.opts.codecs.ByName(FormatYAML); ok {
		t.Fatal("YAML codec should be unregistered")
	}
	// Other defaults remain.
	if _, ok := r.state.opts.codecs.ByName(FormatJSON); !ok {
		t.Fatal("JSON codec should still be registered")
	}
}

func TestWithCodecs_ReplacesSet(t *testing.T) {
	// A codec set with only JSON; YAML / TOML must be absent.
	custom := NewCodecs(JSON)
	r := newRegistry(t, WithCodecs(custom))
	if _, ok := r.state.opts.codecs.ByName(FormatJSON); !ok {
		t.Fatal("JSON should be present")
	}
	if _, ok := r.state.opts.codecs.ByName(FormatYAML); ok {
		t.Fatal("YAML should NOT be present after WithCodecs replacement")
	}
}

func TestWithLogger_Installs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	r := newRegistry(t, WithLogger(logger))
	if r.state.logger != logger {
		t.Fatal("logger not installed")
	}
}
