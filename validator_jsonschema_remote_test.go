package recon

import (
	"errors"
	"testing"

	"github.com/go-rotini/jsonschema"
)

const remoteRefSchema = `{"$ref":"https://example.com/port.json"}`

func TestJSONSchemaValidator_RemoteRefRejectedByDefault(t *testing.T) {
	_, err := NewJSONSchemaValidator([]byte(remoteRefSchema))
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("err = %v, want ErrSchemaInvalid", err)
	}
	if !errors.Is(err, jsonschema.ErrLoaderRejected) {
		t.Fatalf("err = %v, want jsonschema.ErrLoaderRejected", err)
	}
}

func TestJSONSchemaValidator_RemoteRefViaCallerLoader(t *testing.T) {
	loader := jsonschema.ChainLoader{
		jsonschema.MetaLoader(),
		jsonschema.MapLoader{
			"https://example.com/port.json": []byte(`{"type":"object","required":["port"]}`),
		},
	}
	s, err := jsonschema.Compile([]byte(remoteRefSchema), jsonschema.WithLoader(loader))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v := NewJSONSchemaValidatorFromSchema(s)
	if err := v.Validate(map[string]any{"port": 8080}); err != nil {
		t.Fatalf("valid instance: %v", err)
	}
	if err := v.Validate(map[string]any{}); err == nil {
		t.Fatal("required keyword from the remote schema not enforced")
	}
}
