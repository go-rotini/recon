package recon

import (
	"slices"
	"testing"
)

func TestParseTag_Empty(t *testing.T) {
	ft := ParseTag("")
	if ft.Name != "" || ft.Skip {
		t.Errorf("empty tag should produce zero FieldTag, got %+v", ft)
	}
}

func TestParseTag_SkipDash(t *testing.T) {
	if !ParseTag("-").Skip {
		t.Error("tag \"-\" should set Skip")
	}
	if !ParseTag("-,required").Skip {
		t.Error("tag \"-,required\" should set Skip")
	}
}

func TestParseTag_NameOnly(t *testing.T) {
	ft := ParseTag("server.port")
	if ft.Name != "server.port" {
		t.Errorf("Name = %q, want server.port", ft.Name)
	}
}

func TestParseTag_BareOptions(t *testing.T) {
	ft := ParseTag("port,required,notEmpty,secret,immutable,expand,fromFile,unset,inline,base64,hex,deprecated")
	if ft.Name != "port" {
		t.Errorf("Name = %q, want port", ft.Name)
	}
	checks := map[string]bool{
		"Required":   ft.Required,
		"NotEmpty":   ft.NotEmpty,
		"Secret":     ft.Secret,
		"Immutable":  ft.Immutable,
		"Expand":     ft.Expand,
		"FromFile":   ft.FromFile,
		"Unset":      ft.Unset,
		"Inline":     ft.Inline,
		"Base64":     ft.Base64,
		"Hex":        ft.Hex,
		"Deprecated": ft.Deprecated,
	}
	for name, set := range checks {
		if !set {
			t.Errorf("expected %s = true", name)
		}
	}
}

func TestParseTag_ValueOptions(t *testing.T) {
	ft := ParseTag(`port,default=8080,path=server.port,source=env,format=json,transform=snake,layout=2006-01-02,validate=expr,separator=;,kvSeparator==,deprecated=use --port`)
	cases := map[string]string{
		"DefaultValue":       ft.DefaultValue,
		"Path":               ft.Path,
		"Source":             ft.Source,
		"Format":             ft.Format,
		"Transform":          ft.Transform,
		"Layout":             ft.Layout,
		"Validate":           ft.Validate,
		"Separator":          ft.Separator,
		"KVSeparator":        ft.KVSeparator,
		"DeprecationMessage": ft.DeprecationMessage,
	}
	wants := map[string]string{
		"DefaultValue":       "8080",
		"Path":               "server.port",
		"Source":             "env",
		"Format":             "json",
		"Transform":          "snake",
		"Layout":             "2006-01-02",
		"Validate":           "expr",
		"Separator":          ";",
		"KVSeparator":        "=",
		"DeprecationMessage": "use --port",
	}
	for k, got := range cases {
		if got != wants[k] {
			t.Errorf("%s = %q, want %q", k, got, wants[k])
		}
	}
	if !ft.HasDefault {
		t.Error("HasDefault should be true when default= is present")
	}
	if !ft.Deprecated {
		t.Error("Deprecated should be true even with a message")
	}
}

func TestParseTag_Aliases(t *testing.T) {
	ft := ParseTag("port,aliases=p;PORT;APP_PORT")
	want := []string{"p", "PORT", "APP_PORT"}
	if !slices.Equal(ft.Aliases, want) {
		t.Errorf("Aliases = %v, want %v", ft.Aliases, want)
	}
}

func TestParseTag_AliasesEmpty(t *testing.T) {
	ft := ParseTag("port,aliases=")
	if len(ft.Aliases) != 0 {
		t.Errorf("Aliases = %v, want empty", ft.Aliases)
	}
}

func TestParseTag_AliasesWithEmptyEntries(t *testing.T) {
	ft := ParseTag("port,aliases=a;;b;;")
	want := []string{"a", "b"}
	if !slices.Equal(ft.Aliases, want) {
		t.Errorf("Aliases = %v, want %v", ft.Aliases, want)
	}
}

func TestParseTag_DefaultWithoutValue(t *testing.T) {
	// `default` (no = sign) still sets HasDefault, with empty DefaultValue.
	ft := ParseTag("port,default")
	if !ft.HasDefault {
		t.Error("HasDefault should be true for bare `default`")
	}
	if ft.DefaultValue != "" {
		t.Errorf("DefaultValue = %q, want empty", ft.DefaultValue)
	}
}

func TestParseTag_UnknownOptionIgnored(t *testing.T) {
	// Unknown options must not panic and must not affect known fields.
	ft := ParseTag("port,required,frobnicate=quux")
	if !ft.Required {
		t.Error("known option should still be honored alongside unknown ones")
	}
}

func TestParseTag_WhitespaceTolerated(t *testing.T) {
	ft := ParseTag("  port  ,  required  ,  default = 9000  ")
	if ft.Name != "port" {
		t.Errorf("Name = %q, want port (trimmed)", ft.Name)
	}
	if !ft.Required {
		t.Error("Required not detected with surrounding whitespace")
	}
	if ft.DefaultValue != "9000" {
		t.Errorf("DefaultValue = %q, want 9000 (trimmed)", ft.DefaultValue)
	}
}

func TestParseTag_EmptySegmentsIgnored(t *testing.T) {
	// Consecutive commas don't produce phantom options.
	ft := ParseTag("port,,required,")
	if ft.Name != "port" || !ft.Required {
		t.Errorf("ParseTag mishandles consecutive commas: %+v", ft)
	}
}
