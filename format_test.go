package recon

import "testing"

func TestDetectFormat(t *testing.T) {
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"config.yaml", FormatYAML, true},
		{"config.yml", FormatYAML, true},
		{"/etc/app/config.YAML", FormatYAML, true},
		{"settings.toml", FormatTOML, true},
		{"data.json", FormatJSON, true},
		{"data.jsonc", FormatJSONC, true},
		{"data.json5", FormatJSONC, true},
		{".env", FormatDotenv, true},
		{"some.unknown", "", false},
		{"no-extension", "", false},
		{"", "", false},
		{"trailing.local.yaml", FormatYAML, true}, // multi-dot still uses last extension
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			got, ok := DetectFormat(c.path)
			if ok != c.ok || got != c.want {
				t.Errorf("DetectFormat(%q) = (%q, %v), want (%q, %v)", c.path, got, ok, c.want, c.ok)
			}
		})
	}
}
