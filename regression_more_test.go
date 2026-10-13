package recon

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/go-rotini/jsonschema"
)

// A scalar from a higher-precedence layer leaves a lower layer's keys
// below it readable; only the single-tree views pick one side.
func TestResolve_ScalarKeepsLowerSectionReadable(t *testing.T) {
	t.Setenv("APP_LOG", "1")
	t.Setenv("USER", "matt")
	file, err := NewBufferSource("file", "yaml", []byte("log:\n  level: debug\nuser:\n  name: svc\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range []*OSEnvSource{NewOSEnvSource(WithEnvPrefix("APP_")), NewOSEnvSource()} {
		r := newRegistry(t, WithoutWatch(), WithSources(env, file))
		var c struct {
			Log struct {
				Level string `recon:"level"`
			} `recon:"log"`
			User struct {
				Name string `recon:"name"`
			} `recon:"user"`
		}
		if err := r.Bind(&c); err != nil {
			t.Fatal(err)
		}
		if c.Log.Level != "debug" || c.User.Name != "svc" {
			t.Fatalf("bound level=%q user.name=%q, want debug and svc", c.Log.Level, c.User.Name)
		}
		out, err := r.SaveString(WithSaveFormat(FormatYAML))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "name: svc") {
			t.Fatalf("Save dropped the file's user section:\n%s", out)
		}
	}
	r := newRegistry(t, WithoutWatch(), WithSources(NewOSEnvSource(WithEnvPrefix("APP_")), file))
	if got := r.Snapshot().AsMap()["log"]; got != "1" {
		t.Fatalf("AsMap log = %v, want the higher-precedence scalar", got)
	}
	if v, ok := r.Snapshot().Get(ParsePath("log.level")); !ok || v.String() != "debug" {
		t.Fatalf("Get(log.level) = %v %v", v, ok)
	}
}

func yamlRegistry(t *testing.T, doc string) *Registry {
	t.Helper()
	m, err := YAML.Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return newRegistry(t, WithoutWatch(), WithSource(NewMapSource("y", m)))
}

// A secret field inside a slice or map element struct is redacted in
// Save, Snapshot.String and coercion errors.
func TestBind_SecretsInElementsRedacted(t *testing.T) {
	type tagged struct {
		Host     string `recon:"host"`
		Password string `recon:"password,secret"`
	}
	type typed struct {
		PW Secret[string] `recon:"password"`
	}
	type pin struct {
		Pin int `recon:"pin,secret"`
	}
	check := func(t *testing.T, r *Registry) {
		t.Helper()
		out, err := r.SaveString(WithSaveFormat(FormatYAML))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "hunter2") {
			t.Fatalf("Save leaks the secret:\n%s", out)
		}
		if s := r.Snapshot().String(); strings.Contains(s, "hunter2") {
			t.Fatalf("Snapshot.String leaks the secret:\n%s", s)
		}
	}
	t.Run("slice tag", func(t *testing.T) {
		r := yamlRegistry(t, "dbs:\n  - host: h\n    password: hunter2\n")
		var c struct {
			DBs []tagged `recon:"dbs"`
		}
		if err := r.Bind(&c); err != nil || c.DBs[0].Password != "hunter2" {
			t.Fatalf("%v %+v", err, c)
		}
		check(t, r)
	})
	t.Run("slice Secret type", func(t *testing.T) {
		r := yamlRegistry(t, "dbs:\n  - password: hunter2\n")
		var c struct {
			DBs []typed `recon:"dbs"`
		}
		if err := r.Bind(&c); err != nil || c.DBs[0].PW.Value() != "hunter2" {
			t.Fatalf("%v %+v", err, c)
		}
		check(t, r)
	})
	t.Run("map tag", func(t *testing.T) {
		r := yamlRegistry(t, "dbs:\n  a:\n    password: hunter2\n")
		var c struct {
			DBs map[string]tagged `recon:"dbs"`
		}
		if err := r.Bind(&c); err != nil || c.DBs["a"].Password != "hunter2" {
			t.Fatalf("%v %+v", err, c)
		}
		check(t, r)
	})
	t.Run("error", func(t *testing.T) {
		r := yamlRegistry(t, "dbs:\n  - pin: hunter2\n")
		var c struct {
			DBs []pin `recon:"dbs"`
		}
		err := r.Bind(&c)
		var ce *CoercionError
		if !errors.As(err, &ce) || !ce.Secret || strings.Contains(FormatError(r, err), "hunter2") {
			t.Fatalf("err = %v, want a redacted secret CoercionError", FormatError(r, err))
		}
	})
}

type treeNode struct {
	Val   int       `recon:"val"`
	Left  *treeNode `recon:"left"`
	Right *treeNode `recon:"right"`
}

// A self-referencing type binds as deep as the data goes.
func TestBind_RecursiveTypeFollowsData(t *testing.T) {
	r := yamlRegistry(t, "val: 1\nleft:\n  val: 2\n  left:\n    val: 3\n")
	var n treeNode
	if err := r.Bind(&n, WithDecodeStrict()); err != nil {
		t.Fatal(err)
	}
	if n.Left == nil || n.Left.Val != 2 || n.Left.Left == nil || n.Left.Left.Val != 3 || n.Left.Left.Left != nil {
		t.Fatalf("got %+v", n)
	}
	preset := treeNode{Left: &treeNode{}}
	if err := r.Bind(&preset); err != nil || preset.Left.Val != 2 {
		t.Fatalf("preset pointer: %v %+v", err, preset.Left)
	}
}

// Bind marks every secret field at once: one rebuild, and none when
// nothing new is marked.
func TestBind_SecretMarkingRebuildsOnce(t *testing.T) {
	r := yamlRegistry(t, "a: 1\nb: 2\nc: 3\n")
	var cfg struct {
		A string `recon:"a,secret"`
		B string `recon:"b,secret"`
		C string `recon:"c,secret"`
	}
	before := r.Snapshot()
	if err := r.Bind(&cfg); err != nil {
		t.Fatal(err)
	}
	first := r.Snapshot()
	if first == before {
		t.Fatal("no rebuild after marking secrets")
	}
	if err := r.Bind(&cfg); err != nil {
		t.Fatal(err)
	}
	if r.Snapshot() != first {
		t.Fatal("a second Bind rebuilt the snapshot though no new key was marked")
	}
	r.MarkSecret("a")
	if r.Snapshot() != first {
		t.Fatal("MarkSecret of a marked key rebuilt the snapshot")
	}
}

// Element structs read the WithDecodeTag tag and the cheap options.
func TestBind_ElementTagNameAndOptions(t *testing.T) {
	r := yamlRegistry(t, "base: /srv\nservers:\n  - addr: a\n    weight: 3\n    dir: ${base}/x\n    old: legacy\n    net:\n      port: 9\n")
	var c struct {
		Servers []struct {
			Host   string `cfg:"addr"`
			Weight int    `cfg:"weight"`
			Dir    string `cfg:"dir,expand"`
			Name   string `cfg:"name,aliases=old"`
			Port   int    `cfg:"net.port"`
		} `cfg:"servers"`
	}
	if err := r.Bind(&c, WithDecodeTag("cfg")); err != nil {
		t.Fatal(err)
	}
	got := c.Servers[0]
	if got.Host != "a" || got.Weight != 3 || got.Dir != "/srv/x" || got.Name != "legacy" || got.Port != 9 {
		t.Fatalf("got %+v", got)
	}
	var e struct {
		Servers []struct {
			Host string `recon:"host,notEmpty"`
		} `recon:"servers"`
	}
	r2 := yamlRegistry(t, "servers:\n  - host: \"\"\n")
	if err := r2.Bind(&e); !errors.Is(err, ErrEmptyValue) {
		t.Fatalf("notEmpty element: err = %v", err)
	}
}

// A null value does not satisfy required.
func TestBind_RequiredRejectsNull(t *testing.T) {
	r := yamlRegistry(t, "port:\nservers:\n  - port:\n")
	var c struct {
		Port int `recon:"port,required"`
	}
	if err := r.Bind(&c); !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("err = %v, want ErrMissingRequired", err)
	}
	var d struct {
		Port int `recon:"port,required,default=80"`
	}
	if err := r.Bind(&d); err != nil || d.Port != 80 {
		t.Fatalf("default: %v %d", err, d.Port)
	}
	var e struct {
		Servers []struct {
			Port int `recon:"port,required"`
		} `recon:"servers"`
	}
	if err := r.Bind(&e); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("element: err = %v, want ErrKeyNotFound", err)
	}
}

// omitempty counts only from the primary tag.
func TestBind_OmitEmptyOnlyFromPrimaryTag(t *testing.T) {
	type inner struct {
		Port int `recon:"port,default=8080"`
	}
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("m", map[string]any{"name": "x"})))
	var c struct {
		Name string `json:"name"`
		DB   *inner `json:"db,omitempty"`
	}
	if err := r.Bind(&c); err != nil || c.DB == nil || c.DB.Port != 8080 {
		t.Fatalf("json omitempty: %v %+v", err, c.DB)
	}
	var d struct {
		DB *inner `recon:"db,omitempty"`
	}
	if err := r.Bind(&d); err != nil || d.DB != nil {
		t.Fatalf("recon omitempty: %v %+v", err, d.DB)
	}
}

// UnmarshalKey into a struct that is a leaf converts the value itself.
func TestUnmarshalKey_LeafStructs(t *testing.T) {
	r := yamlRegistry(t, "at: 2024-01-02T03:04:05Z\ntok: s3cr3t\naddr: 10.0.0.1\nn: 7\n")
	var at time.Time
	if err := r.UnmarshalKey("at", &at); err != nil || at.Year() != 2024 {
		t.Fatalf("time: %v %v", err, at)
	}
	var tok Secret[string]
	if err := r.UnmarshalKey("tok", &tok); err != nil || tok.Value() != "s3cr3t" || !r.IsSecret("tok") {
		t.Fatalf("secret: %v %q", err, tok.Value())
	}
	var addr netip.Addr
	if err := r.UnmarshalKey("addr", &addr); err != nil || addr.String() != "10.0.0.1" {
		t.Fatalf("addr: %v %v", err, addr)
	}
	var n int
	dec := WithCustomDecoder(func(v Value) (int, error) { return 42, nil })
	if err := r.UnmarshalKey("n", &n, dec); err != nil || n != 42 {
		t.Fatalf("custom decoder: %v %d", err, n)
	}
}

// A value bound through a second spelling of one environment variable
// is listed once.
func TestBind_EnvUnderscoreListedOnce(t *testing.T) {
	t.Setenv("APP_MAX_CONNS", "5")
	r := newRegistry(t, WithoutWatch(), WithSource(NewOSEnvSource(WithEnvPrefix("APP_"))))
	var c struct {
		MaxConns int `recon:"max_conns"`
	}
	if err := r.Bind(&c); err != nil || c.MaxConns != 5 {
		t.Fatalf("%v %d", err, c.MaxConns)
	}
	keys := r.Snapshot().Keys()
	if len(keys) != 1 || keys[0].String() != "max_conns" {
		t.Fatalf("keys = %v, want [max_conns]", keys)
	}
	out, err := r.SaveString(WithSaveFormat(FormatYAML))
	if err != nil || strings.Contains(out, "max:") {
		t.Fatalf("Save = %q %v", out, err)
	}
	if len(r.Describe().Keys) != 1 {
		t.Fatalf("Describe = %+v", r.Describe().Keys)
	}
	if v, ok := r.Snapshot().Get(ParsePath("max.conns")); !ok || v.String() != "5" {
		t.Fatalf("Get(max.conns) = %v %v", v, ok)
	}
}

// A bound map key whose entries a source later lists is listed once.
func TestBind_BoundParentListedOnce(t *testing.T) {
	src := NewMapSource("m", map[string]any{"name": "n"})
	r := newRegistry(t, WithoutWatch(), WithSource(src))
	var c struct {
		Labels map[string]string `recon:"labels"`
	}
	if err := r.Bind(&c); err != nil {
		t.Fatal(err)
	}
	src.Replace(map[string]any{"name": "n", "labels": map[string]any{"x": "1"}})
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, k := range r.Snapshot().Keys() {
		got = append(got, k.String())
	}
	if strings.Join(got, " ") != "labels.x name" {
		t.Fatalf("keys = %v, want [labels.x name]", got)
	}
	if err := r.Bind(&c); err != nil || c.Labels["x"] != "1" {
		t.Fatalf("%v %v", err, c.Labels)
	}
}

// PerSourceFor agrees with resolution for map values given to Set and
// for values a source holds under an alias name.
func TestPerSourceFor_MapSetAndAlias(t *testing.T) {
	r := yamlRegistry(t, "old_host: legacy\n")
	if err := r.Set("db", map[string]any{"host": "h"}); err != nil {
		t.Fatal(err)
	}
	ps, err := PerSourceFor[string](r, "db.host")
	if err != nil || !ps.Explicit.IsSet || ps.Explicit.Value != "h" {
		t.Fatalf("explicit = %+v %v", ps.Explicit, err)
	}
	if err := r.RegisterAlias("old_host", "host"); err != nil {
		t.Fatal(err)
	}
	ps, err = PerSourceFor[string](r, "host")
	if e := ps.BySource("y"); err != nil || !e.IsSet || e.Value != "legacy" || ps.Resolved.Source != "y" {
		t.Fatalf("source entry = %+v, resolved = %+v, err %v", e, ps.Resolved, err)
	}
}

// Set merges a non-empty map and replaces with an empty one, as
// documented.
func TestSet_MapMergeAndEmptyMap(t *testing.T) {
	r := yamlRegistry(t, "labels:\n  x: 1\n  y: 2\n")
	if err := r.Set("labels", map[string]any{"z": 3}); err != nil {
		t.Fatal(err)
	}
	var c struct {
		Labels map[string]int `recon:"labels"`
	}
	if err := r.Bind(&c); err != nil || len(c.Labels) != 3 {
		t.Fatalf("merge: %v %v", err, c.Labels)
	}
	if err := r.Set("labels", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	c.Labels = nil
	if err := r.Bind(&c); err != nil || len(c.Labels) != 0 {
		t.Fatalf("empty: %v %v", err, c.Labels)
	}
	if m, ok := r.Snapshot().AsMap()["labels"].(map[string]any); !ok || len(m) != 0 {
		t.Fatalf("AsMap labels = %v", r.Snapshot().AsMap()["labels"])
	}
}

// With WithFileEnvKeys, Keys lists only names Get can read, also where
// environment names fold case.
func TestFileEnvKeys_ListsOnlyReadableNames(t *testing.T) {
	old := envNamesFoldCase
	envNamesFoldCase = true
	t.Cleanup(func() { envNamesFoldCase = old })
	prefix := "APP_"
	s := &MapSource{name: "f", data: map[string]any{"app_port": "1", "APP_HOST": "h", "APP_db": "x"}, envKeys: &prefix}
	keys := s.Keys()
	if len(keys) != 1 || keys[0].String() != "host" {
		t.Fatalf("keys = %v, want [host]", keys)
	}
	for _, k := range keys {
		if _, ok, _ := s.Get(k); !ok {
			t.Fatalf("Get(%s) misses a listed key", k)
		}
	}
}

type captureValidator struct{ got map[string]any }

func (c *captureValidator) Validate(m map[string]any) error {
	c.got = m
	return nil
}

// Integers above int64 stay numbers: uint64 within its range, exact
// text beyond, in every JSON-family codec and through Save and the
// validator input.
func TestJSON_BigIntegersStayNumbers(t *testing.T) {
	for _, codec := range []Codec{JSON, JSONC} {
		m, err := codec.Decode([]byte(`{"u": 18446744073709551615, "h": 9223372036854775808, "x": 123456789012345678901234567890}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := m["u"].(uint64); !ok {
			t.Fatalf("%s: u = %T, want uint64", codec.Name(), m["u"])
		}
		if m["x"] != "123456789012345678901234567890" {
			t.Fatalf("%s: x = %#v, want its text", codec.Name(), m["x"])
		}
		v := &captureValidator{}
		r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("j", m)), WithValidator(v))
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		if v.got["u"] != uint64(18446744073709551615) || v.got["h"] != uint64(9223372036854775808) {
			t.Fatalf("validator input u=%#v h=%#v", v.got["u"], v.got["h"])
		}
		var cfg struct {
			U uint64 `recon:"u"`
			S string `recon:"u"`
		}
		if err := r.Bind(&cfg); err != nil || cfg.U != 18446744073709551615 || cfg.S != "18446744073709551615" {
			t.Fatalf("bind: %v %+v", err, cfg)
		}
		for _, f := range []string{FormatJSON, FormatYAML} {
			out, err := r.SaveString(WithSaveFormat(f))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, `"18446744073709551615"`) || !strings.Contains(out, "18446744073709551615") {
				t.Fatalf("%s Save writes u quoted or not at all: %s", f, out)
			}
		}
	}
}

// A float literal beyond the float64 range is a positioned error.
func TestJSON_FloatOverflowIsPositioned(t *testing.T) {
	for _, codec := range []Codec{JSON, JSONC} {
		_, err := codec.Decode([]byte("{\n  \"a\": 1e400}"))
		pe, ok := err.(PositionError)
		if !ok {
			t.Fatalf("%s: err = %v, want a PositionError", codec.Name(), err)
		}
		if line, col := pe.Position(); line != 2 || col != 8 {
			t.Fatalf("%s: position %d:%d, want 2:8 (%v)", codec.Name(), line, col, err)
		}
	}
}

// Invalid UTF-8 is a positioned error in every bundled codec.
func TestCodecs_InvalidUTF8Rejected(t *testing.T) {
	docs := map[Codec]string{
		JSON:   "{\"a\": \"a\xffb\"}",
		JSONC:  "{\"a\": \"a\xffb\"}",
		Dotenv: "A=a\xffb\n",
	}
	for codec, doc := range docs {
		_, err := codec.Decode([]byte(doc))
		pe, ok := err.(PositionError)
		if !ok {
			t.Fatalf("%s: err = %v, want a PositionError", codec.Name(), err)
		}
		if line, _ := pe.Position(); line != 1 || !strings.Contains(err.Error(), "invalid UTF-8") {
			t.Fatalf("%s: %v", codec.Name(), err)
		}
	}
}

// The integers 0 and 1 fill a bool field.
func TestBind_NumericBool(t *testing.T) {
	r := yamlRegistry(t, "a: 1\nb: 0\nc: 2\n")
	var ok struct {
		A bool `recon:"a"`
		B bool `recon:"b"`
	}
	ok.B = true
	if err := r.Bind(&ok); err != nil || !ok.A || ok.B {
		t.Fatalf("%v %+v", err, ok)
	}
	var bad struct {
		C bool `recon:"c"`
	}
	if err := r.Bind(&bad); !errors.Is(err, ErrCoercion) {
		t.Fatalf("2 into bool: err = %v", err)
	}
}

// A TOML date-time validates as a string.
func TestJSONSchema_TimeIsString(t *testing.T) {
	m, err := TOML.Decode([]byte("d = 2024-01-02T03:04:05Z\n"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewJSONSchemaValidator([]byte(`{"properties": {"d": {"type": "string"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("t", m)), WithValidator(v))
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
}

// YAML and JSON agree on integers above the int64 range, and beyond
// the uint64 range when the yaml dependency keeps their text.
func TestYAML_BigIntegersMatchJSON(t *testing.T) {
	y, err := YAML.Decode([]byte("u: 18446744073709551615\nx: 123456789012345678901234567890\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, isFloat := y["u"].(float64); isFloat {
		t.Skip("the yaml dependency rounds integers above int64 to float64")
	}
	if _, ok := y["u"].(uint64); !ok {
		t.Fatalf("u = %T, want uint64", y["u"])
	}
	if _, isFloat := y["x"].(float64); isFloat {
		t.Skip("the yaml dependency rounds integers beyond uint64 to float64")
	}
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("y", y)))
	var c struct {
		X string `recon:"x"`
	}
	if err := r.Bind(&c); err != nil || c.X != "123456789012345678901234567890" {
		t.Fatalf("%v %q", err, c.X)
	}
}

// A uint64-max value passes a type: integer schema, once the jsonschema
// dependency treats uint64 as an integer.
func TestJSONSchema_BigIntegerIsInteger(t *testing.T) {
	probe, err := jsonschema.Compile([]byte(`{"type": "integer"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := probe.ValidateValue(uint64(5)); err != nil || !res.Valid {
		t.Skip("the jsonschema dependency does not accept uint64 values")
	}
	v, err := NewJSONSchemaValidator([]byte(`{"properties": {"n": {"type": "integer"}, "s": {"type": "string"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []Codec{JSON, JSONC, YAML} {
		doc := `{"n": 18446744073709551615, "s": "x"}`
		if c == YAML {
			doc = "n: 18446744073709551615\ns: x\n"
		}
		m, err := c.Decode([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		if _, isFloat := m["n"].(float64); isFloat {
			continue
		}
		r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("c", m)), WithValidator(v))
		if err := r.Validate(); err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
	}
}

// A TOML float out of range is a positioned error, once the toml
// dependency reports one.
func TestTOML_FloatOverflowIsPositioned(t *testing.T) {
	_, err := TOML.Decode([]byte("port = 1e400\n"))
	if err == nil {
		t.Fatal("1e400 decoded")
	}
	pe, ok := errors.AsType[PositionError](err)
	if !ok {
		t.Skipf("the toml dependency reports no position: %v", err)
	}
	if line, col := pe.Position(); line != 1 || col < 1 {
		t.Fatalf("position %d:%d (%v)", line, col, err)
	}
}
