package recon

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestGenerateTemplate_EmitsDefaultsByDefault(t *testing.T) {
	r := newRegistry(t)
	_ = r.SetDefault("server.port", 8080)
	_ = r.SetDefault("server.host", "localhost")

	b, err := r.GenerateTemplate(FormatJSON)
	if err != nil {
		t.Fatalf("GenerateTemplate: %v", err)
	}
	decoded, _ := JSON.Decode(b)
	server, ok := decoded["server"].(map[string]any)
	if !ok {
		t.Fatalf("server missing from template: %v", decoded)
	}
	if _, ok := server["port"]; !ok {
		t.Fatal("port missing from template")
	}
	if _, ok := server["host"]; !ok {
		t.Fatal("host missing from template")
	}
}

func TestGenerateTemplate_FormatRequired(t *testing.T) {
	r := newRegistry(t)
	_, err := r.GenerateTemplate("")
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err=%v, want ErrUnsupportedFormat", err)
	}
}

func TestGenerateTemplate_UnknownFormatRejected(t *testing.T) {
	r := newRegistry(t)
	_, err := r.GenerateTemplate("xml")
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err=%v, want ErrUnsupportedFormat", err)
	}
}

func TestGenerateTemplate_OnlyPrefixFilters(t *testing.T) {
	r := newRegistry(t)
	_ = r.SetDefault("server.port", 8080)
	_ = r.SetDefault("db.dsn", "postgres://x")

	b, err := r.GenerateTemplate(FormatJSON, WithSaveOnly("server"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "db") || strings.Contains(string(b), "dsn") {
		t.Fatalf("non-server keys leaked: %s", b)
	}
}

func TestGenerateTemplate_RedactsSecretsByDefault(t *testing.T) {
	r := newRegistry(t)
	_ = r.SetDefault("token", "hunter2")
	r.MarkSecret("token")

	b, err := r.GenerateTemplate(FormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "hunter2") {
		t.Fatalf("template leaked secret: %s", b)
	}
}

func TestGenerateTemplate_YAMLOutput(t *testing.T) {
	r := newRegistry(t)
	_ = r.SetDefault("k", "v")
	b, err := r.GenerateTemplate(FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "k") || !strings.Contains(string(b), "v") {
		t.Fatalf("YAML template missing payload: %q", b)
	}
}

func TestTemplateKeys_ReturnsSortedPaths(t *testing.T) {
	r := newRegistry(t)
	_ = r.SetDefault("zebra", 1)
	_ = r.SetDefault("alpha", 2)
	_ = r.SetDefault("middle", 3)

	keys := r.TemplateKeys()
	got := make([]string, len(keys))
	for i, p := range keys {
		got[i] = p.String()
	}
	want := []string{"alpha", "middle", "zebra"}
	if !slices.Equal(got, want) {
		t.Fatalf("TemplateKeys=%v, want %v", got, want)
	}
}

func TestGenerateTemplate_ClosedRegistry(t *testing.T) {
	r, _ := New()
	_ = r.Close()
	_, err := r.GenerateTemplate(FormatJSON)
	if !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("err=%v, want ErrRegistryClosed", err)
	}
}
