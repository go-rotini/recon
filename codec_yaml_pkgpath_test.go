package recon

import (
	"reflect"
	"testing"

	"github.com/go-rotini/yaml"
)

func TestYAMLPkgPathMatchesYAMLPackage(t *testing.T) {
	if got := reflect.TypeFor[yaml.Encoder]().PkgPath(); got != yamlPkgPath {
		t.Errorf("yamlPkgPath = %q, want %q", yamlPkgPath, got)
	}
}
