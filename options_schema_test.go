package recon

import (
	"errors"
	"testing"
)

func TestWithSchema_AcceptsValidJSON(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"port":{"type":"integer"}}}`)
	r, err := New(WithSchema(schema))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if r.state.opts.validator == nil {
		t.Fatal("WithSchema did not install a validator")
	}
}

func TestWithSchema_PropagatesCompileError(t *testing.T) {
	_, err := New(WithSchema([]byte(`{"type": 123}`)))
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("err=%v, want wrap of ErrSchemaInvalid", err)
	}
}

func TestWithSchema_EnforcesAtReload(t *testing.T) {
	schema := []byte(`{
		"type":"object",
		"required":["port"],
		"properties":{"port":{"type":"integer","minimum":1}}
	}`)
	r, err := New(WithSchema(schema))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	rerr := r.Reload()
	if !errors.Is(rerr, ErrValidation) {
		t.Fatalf("Reload err=%v, want wrap of ErrValidation", rerr)
	}

	_ = r.Set("port", 8080)
	if rerr := r.Reload(); rerr != nil {
		t.Fatalf("Reload after fix: %v", rerr)
	}
}
