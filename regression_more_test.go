package recon

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
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

// Integers above int64 stay numbers: uint64 within its range, an exact
// json.Number beyond, in every JSON-family codec and through Save and
// the validator input.
func TestJSON_BigIntegersStayNumbers(t *testing.T) {
	for _, codec := range []Codec{JSON, JSONC} {
		m, err := codec.Decode([]byte(`{"u": 18446744073709551615, "h": 9223372036854775808, "x": 123456789012345678901234567890}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := m["u"].(uint64); !ok {
			t.Fatalf("%s: u = %T, want uint64", codec.Name(), m["u"])
		}
		if m["x"] != json.Number("123456789012345678901234567890") {
			t.Fatalf("%s: x = %#v, want a json.Number", codec.Name(), m["x"])
		}
		v := &captureValidator{}
		r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("j", m)), WithValidator(v))
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		if v.got["u"] != uint64(18446744073709551615) || v.got["h"] != uint64(9223372036854775808) {
			t.Fatalf("validator input u=%#v h=%#v", v.got["u"], v.got["h"])
		}
		if v.got["x"] != json.Number("123456789012345678901234567890") {
			t.Fatalf("validator input x=%#v", v.got["x"])
		}
		if x, _, _ := r.Get("x"); x.String() != "123456789012345678901234567890" {
			t.Fatalf("Get(x) = %v", x)
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
		if msg := err.Error(); !strings.Contains(msg, "value 1e400 overflows float64") || strings.Contains(msg, "strconv") || !errors.Is(err, strconv.ErrRange) {
			t.Fatalf("%s: %v", codec.Name(), err)
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

// YAML and JSON agree on integers above the int64 range and beyond
// the uint64 range.
func TestYAML_BigIntegersMatchJSON(t *testing.T) {
	y, err := YAML.Decode([]byte("u: 18446744073709551615\nx: 123456789012345678901234567890\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := y["u"].(uint64); !ok {
		t.Fatalf("u = %T, want uint64", y["u"])
	}
	r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("y", y)))
	var c struct {
		X string `recon:"x"`
	}
	if err := r.Bind(&c); err != nil || c.X != "123456789012345678901234567890" {
		t.Fatalf("%v %q", err, c.X)
	}
}

// A uint64-max value passes a type: integer schema.
func TestJSONSchema_BigIntegerIsInteger(t *testing.T) {
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
		r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("c", m)), WithValidator(v))
		if err := r.Validate(); err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
	}
}

// A TOML float out of range is a positioned error.
func TestTOML_FloatOverflowIsPositioned(t *testing.T) {
	_, err := TOML.Decode([]byte("port = 1e400\n"))
	if err == nil {
		t.Fatal("1e400 decoded")
	}
	pe, ok := errors.AsType[PositionError](err)
	if !ok {
		t.Fatalf("err = %v, want a PositionError", err)
	}
	if line, col := pe.Position(); line != 1 || col < 1 {
		t.Fatalf("position %d:%d (%v)", line, col, err)
	}
}

type (
	selfMap   map[string]selfMap
	selfSlice []selfSlice
	selfPtr   *selfPtr
	mutualA   []mutualB
	mutualB   map[string]mutualA
)

// Self-referencing container types bind and unmarshal without hanging.
func TestBind_SelfReferencingContainers(t *testing.T) {
	for name, fn := range map[string]func(r *Registry) error{
		"map field": func(r *Registry) error {
			var c struct{ T selfMap }
			return r.Bind(&c)
		},
		"slice field": func(r *Registry) error {
			var c struct{ L selfSlice }
			return r.Bind(&c)
		},
		"pointer field": func(r *Registry) error {
			var c struct{ P selfPtr }
			return r.Bind(&c)
		},
		"mutual field": func(r *Registry) error {
			var c struct{ M mutualA }
			return r.Bind(&c)
		},
		"UnmarshalKey map": func(r *Registry) error {
			var tr selfMap
			return r.UnmarshalKey("t", &tr)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := New(WithoutWatch(), WithSource(NewMapSource("m", map[string]any{"t": map[string]any{"a": map[string]any{}}})))
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- fn(r) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("did not return")
			}
		})
	}
	for _, typ := range []reflect.Type{
		reflect.TypeFor[selfMap](), reflect.TypeFor[selfSlice](),
		reflect.TypeFor[selfPtr](), reflect.TypeFor[mutualA](),
	} {
		if isSecretType(typ) || scanSecrets(typ, "recon", nil) {
			t.Errorf("%v reported as secret", typ)
		}
	}
}

// A null or scalar a lower layer leaves at a key does not hide the
// higher-precedence keys below it from Get, map fields and
// GetStringMap.
func TestResolve_LowerParentYieldsToKeysBelow(t *testing.T) {
	t.Setenv("APP_LABELS_TEAM", "core")
	t.Setenv("APP_LOG_LEVEL", "debug")
	file, err := NewBufferSource("file", "yaml", []byte("labels:\nlog: plain\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(WithoutWatch(), WithSource(NewOSEnvSource(WithEnvPrefix("APP_"))), WithSource(file))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Labels map[string]string
		Log    map[string]string
	}
	if err := r.Bind(&c); err != nil {
		t.Fatal(err)
	}
	if c.Labels["team"] != "core" || len(c.Labels) != 1 {
		t.Errorf("Labels = %v, want map[team:core]", c.Labels)
	}
	if c.Log["level"] != "debug" || len(c.Log) != 1 {
		t.Errorf("Log = %v, want map[level:debug]", c.Log)
	}
	m, ok, err := r.GetStringMap("labels")
	if err != nil || !ok || m["team"] != "core" {
		t.Errorf("GetStringMap(labels) = %v, %v, %v", m, ok, err)
	}

	// A parent from a higher layer still wins over lower keys below.
	if err := r.Set("log", "quiet"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := r.Get("log"); v.String() != "quiet" {
		t.Errorf("Get(log) after Set = %v, want quiet", v)
	}
}

// Binding a field whose spelling hides another key's listing does not
// change what lookups of that key's parent find.
func TestResolve_BindKeepsParentReadable(t *testing.T) {
	t.Setenv("APP_MAX_CONNS", "5")
	r, err := New(WithoutWatch(), WithSource(NewOSEnvSource(WithEnvPrefix("APP_"))))
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsSet("max") {
		t.Fatal("max not set before bind")
	}
	var c struct{ MaxConns int }
	if err := r.Bind(&c); err != nil {
		t.Fatal(err)
	}
	if !r.IsSet("max") {
		t.Error("max not set after bind")
	}
	m, ok, err := r.GetStringMap("max")
	if err != nil || !ok || fmt.Sprint(m["conns"]) != "5" {
		t.Errorf("GetStringMap(max) = %v, %v, %v", m, ok, err)
	}
	if got := r.Snapshot().Keys(); len(got) != 1 || got[0].String() != "max_conns" {
		t.Errorf("Keys = %v, want [max_conns]", got)
	}
}

// A scalar set at a grandparent leaves an entry inside a map value at
// the parent readable.
func TestResolve_ScalarGrandparentKeepsMapEntry(t *testing.T) {
	ms := NewMapSource("m", map[string]any{"a": map[string]any{"b": map[string]string{"c": "1"}}})
	r, err := New(WithoutWatch(), WithSource(ms))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Set("a", "x"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := r.Get("a.b.c"); !ok || v.String() != "1" {
		t.Errorf("Get(a.b.c) = %v, %v; want 1", v, ok)
	}
	var c struct {
		A struct{ B struct{ C string } }
	}
	if err := r.Bind(&c); err != nil {
		t.Fatal(err)
	}
	if c.A.B.C != "1" {
		t.Errorf("bound a.b.c = %q, want 1", c.A.B.C)
	}
}

// Save to TOML writes an unsigned integer above the int64 range as its
// decimal text, which binds back unchanged.
func TestTOML_SaveBigUint(t *testing.T) {
	r, err := New(WithoutWatch())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Set("n", uint64(math.MaxUint64)); err != nil {
		t.Fatal(err)
	}
	if err := r.Set("list", []any{uint64(math.MaxUint64), 1}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetDefault("d", uint64(math.MaxUint64)); err != nil {
		t.Fatal(err)
	}
	out, err := r.SaveString(WithSaveFormat(FormatTOML))
	if err != nil {
		t.Fatal(err)
	}
	src, err := NewBufferSource("back", FormatTOML, []byte(out))
	if err != nil {
		t.Fatal(err)
	}
	back, err := New(WithoutWatch(), WithSource(src))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		N    uint64
		List []uint64
	}
	if err := back.Bind(&c); err != nil {
		t.Fatalf("bind %q: %v", out, err)
	}
	if c.N != math.MaxUint64 || len(c.List) != 2 || c.List[0] != math.MaxUint64 || c.List[1] != 1 {
		t.Errorf("read back %+v from %q", c, out)
	}
	if _, err := r.GenerateTemplate(FormatTOML); err != nil {
		t.Errorf("GenerateTemplate: %v", err)
	}
}

// A null value takes the field's `default=`, with or without
// `required`, at top level and in struct elements.
func TestBind_NullTakesDefault(t *testing.T) {
	file, err := NewBufferSource("file", "yaml", []byte("port: ~\nservers:\n  - port: ~\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(WithoutWatch(), WithSource(file))
	if err != nil {
		t.Fatal(err)
	}
	type server struct {
		Port    int `recon:"port,default=8080"`
		PortReq int `recon:"port,default=9090,required"`
	}
	var c struct {
		Port    int      `recon:"port,default=8080"`
		PortReq int      `recon:"port,default=9090,required"`
		Servers []server `recon:"servers"`
	}
	if err := r.Bind(&c); err != nil {
		t.Fatal(err)
	}
	if c.Port != 8080 || c.PortReq != 9090 {
		t.Errorf("top level: port=%d portReq=%d, want 8080 9090", c.Port, c.PortReq)
	}
	if len(c.Servers) != 1 || c.Servers[0].Port != 8080 || c.Servers[0].PortReq != 9090 {
		t.Errorf("servers = %+v, want [{8080 9090}]", c.Servers)
	}
}

// An empty `fromFile` path in a struct element is rejected as at top
// level.
func TestBind_ElementFromFileEmptyPath(t *testing.T) {
	r, err := New(WithoutWatch(), WithSource(NewMapSource("m", map[string]any{
		"items": []any{map[string]any{"key": ""}},
	})))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Items []struct {
			Key string `recon:"key,fromFile"`
		} `recon:"items"`
	}
	if err := r.Bind(&c); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err = %v, want ErrInvalidPath", err)
	}
}

// A time of day fills a time.Time field only when the field sets no
// `layout=`; date and date-time forms are accepted either way.
func TestBind_TimeOfDayOnlyWithoutLayout(t *testing.T) {
	r, err := New(WithoutWatch(), WithSource(NewMapSource("m", map[string]any{
		"d": "12:30", "e": "2024-01-02T03:04:05Z", "f": "12:30",
	})))
	if err != nil {
		t.Fatal(err)
	}
	var bad struct {
		D time.Time `recon:"d,layout=2006-01-02"`
	}
	if err := r.Bind(&bad); !errors.Is(err, ErrCoercion) {
		t.Errorf("layout=2006-01-02 with 12:30: err = %v, want ErrCoercion", err)
	}
	var good struct {
		E time.Time `recon:"e,layout=2006-01-02"`
		F time.Time `recon:"f"`
	}
	if err := r.Bind(&good); err != nil {
		t.Fatal(err)
	}
	if good.E.Year() != 2024 || good.F.Hour() != 12 || good.F.Minute() != 30 {
		t.Errorf("e=%v f=%v", good.E, good.F)
	}
}

// A dotenv file loads with bytes that are not UTF-8 in its comments;
// only keys and values must be UTF-8.
func TestDotenv_NonUTF8CommentLoads(t *testing.T) {
	m, err := Dotenv.Decode([]byte("# caf\xe9\nA=1 # caf\xe9\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m["A"] != "1" {
		t.Errorf("A = %v, want 1", m["A"])
	}
	_, err = Dotenv.Decode([]byte("# ok\nA=1\nB=caf\xe9\n"))
	pe, ok := err.(PositionError)
	if !ok {
		t.Fatalf("value: err = %v, want a PositionError", err)
	}
	if line, _ := pe.Position(); line != 3 {
		t.Errorf("value: line %d, want 3 (%v)", line, err)
	}
}

// A secret marked while a source holds changes that would fail a
// rebuild is still redacted by the current snapshot.
func TestMarkSecret_RedactsWhenRebuildWouldFail(t *testing.T) {
	ms := NewMapSource("m", map[string]any{"port": 1, "pw": "hunter2"})
	r, err := New(WithoutWatch(), WithSource(ms),
		WithSchema([]byte(`{"properties":{"port":{"type":"integer"}}}`)),
		WithSecretRedactor(func(string) string { return "***" }))
	if err != nil {
		t.Fatal(err)
	}
	ms.Replace(map[string]any{"port": "bad", "pw": "hunter2"})
	var c struct {
		Pw string `recon:"pw,secret"`
	}
	_ = r.Bind(&c)
	snap := r.Snapshot()
	if !snap.IsSecret(ParsePath("pw")) {
		t.Error("snapshot does not report pw as secret")
	}
	if s := snap.String(); strings.Contains(s, "hunter2") {
		t.Errorf("String leaks the secret:\n%s", s)
	}
}

// Get[uint64] and Get[uint] read unsigned integers, including those
// above the int64 range, and reject negative ones and strings.
func TestGet_Unsigned(t *testing.T) {
	r, err := New(WithoutWatch())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetValues(map[string]any{"big": uint64(math.MaxUint64), "n": 7, "neg": -1, "s": "7"}); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := Get[uint64](r, "big"); err != nil || !ok || got != math.MaxUint64 {
		t.Errorf("Get[uint64](big) = %d, %v, %v", got, ok, err)
	}
	if got, ok, err := Get[uint](r, "n"); err != nil || !ok || got != 7 {
		t.Errorf("Get[uint](n) = %d, %v, %v", got, ok, err)
	}
	if _, _, err := Get[uint64](r, "neg"); !errors.Is(err, ErrCoercion) {
		t.Errorf("Get[uint64](neg) err = %v, want ErrCoercion", err)
	}
	if _, _, err := Get[uint64](r, "s"); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("Get[uint64](s) err = %v, want ErrTypeMismatch", err)
	}
}

// UnmarshalKey finds an underscore key in the environment as a bound
// field does.
func TestUnmarshalKey_UnderscoreEnvName(t *testing.T) {
	t.Setenv("APP_MAX_CONNS", "5")
	r, err := New(WithoutWatch(), WithSource(NewOSEnvSource(WithEnvPrefix("APP_"))))
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := r.UnmarshalKey("max_conns", &n); err != nil || n != 5 {
		t.Fatalf("UnmarshalKey(max_conns) = %d, %v; want 5", n, err)
	}
}

// An integer literal beyond uint64 reaches a validator as a number:
// it passes `type: number` and fails `type: string`.
func TestJSONSchema_BigIntegerIsNumber(t *testing.T) {
	num, err := NewJSONSchemaValidator([]byte(`{"properties": {"f": {"type": "number"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	str, err := NewJSONSchemaValidator([]byte(`{"properties": {"f": {"type": "string"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []Codec{JSON, JSONC, YAML} {
		doc := `{"f": 100000000000000000000, "g": -100000000000000000000}`
		if c == YAML {
			doc = "f: 100000000000000000000\ng: 0x1ffffffffffffffff\n"
		}
		m, err := c.Decode([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := m["f"].(json.Number); !ok {
			t.Fatalf("%s: f = %#v, want a json.Number", c.Name(), m["f"])
		}
		if c == YAML && m["g"] != json.Number("36893488147419103231") {
			t.Fatalf("yaml: g = %#v, want json.Number in decimal", m["g"])
		}
		r := newRegistry(t, WithoutWatch(), WithSource(NewMapSource("c", m)), WithValidator(num))
		if err := r.Validate(); err != nil {
			t.Fatalf("%s type number: %v", c.Name(), err)
		}
		r = newRegistry(t, WithoutWatch(), WithSource(NewMapSource("c", m)))
		if err := str.Validate(r.Snapshot().AsMap()); err == nil {
			t.Fatalf("%s: type string accepted an unquoted integer", c.Name())
		}
		var cfg struct {
			F float64 `recon:"f"`
			S string  `recon:"f"`
		}
		if err := r.Bind(&cfg); err != nil || cfg.F != 1e20 || cfg.S != "100000000000000000000" {
			t.Fatalf("%s bind: %v %+v", c.Name(), err, cfg)
		}
		out, err := r.SaveString(WithSaveFormat(FormatJSON))
		if err != nil || !strings.Contains(out, `"f":100000000000000000000`) {
			t.Fatalf("%s Save json: %v %s", c.Name(), err, out)
		}
		if _, err := r.SaveString(WithSaveFormat(FormatTOML)); err != nil {
			t.Fatalf("%s Save toml: %v", c.Name(), err)
		}
	}
}

// A map or `any` field takes the values Value.Any gives: a TOML
// date-time as a time.Time, an integer above int64 as a uint64 and one
// beyond uint64 as a json.Number.
func TestBind_AnyMatchesValueAny(t *testing.T) {
	file, err := NewBufferSource("t", FormatTOML, []byte("[m]\nwhen = 2024-01-02T03:04:05Z\nday = 2024-01-02\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(WithoutWatch(), WithSource(file))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Set("m.big", uint64(math.MaxUint64)); err != nil {
		t.Fatal(err)
	}
	if err := r.Set("m.huge", json.Number("123456789012345678901234567890")); err != nil {
		t.Fatal(err)
	}
	var c struct {
		M    map[string]any
		When any `recon:"m.when"`
	}
	if err := r.Bind(&c); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if tm, ok := c.M["when"].(time.Time); !ok || !tm.Equal(when) || c.M["day"] != "2024-01-02" {
		t.Errorf("times: %#v", c.M)
	}
	if tm, ok := c.When.(time.Time); !ok || !tm.Equal(when) {
		t.Errorf("When = %#v, want time.Time", c.When)
	}
	if c.M["big"] != uint64(math.MaxUint64) || c.M["huge"] != json.Number("123456789012345678901234567890") {
		t.Errorf("big, huge = %#v, %#v", c.M["big"], c.M["huge"])
	}
	for _, k := range []string{"when", "big", "huge"} {
		v, _, _ := r.Get("m." + k)
		if got := v.Any(); !reflect.DeepEqual(got, c.M[k]) {
			t.Errorf("Get(m.%s).Any() = %#v, Bind gave %#v", k, got, c.M[k])
		}
	}
}

// A JSON or JSONC document holding only null loads as empty, as YAML's
// ~ does.
func TestJSON_NullDocumentIsEmpty(t *testing.T) {
	for _, c := range []Codec{JSON, JSONC, YAML} {
		m, err := c.Decode([]byte(" null\n"))
		if err != nil || len(m) != 0 {
			t.Errorf("%s: %v, %v", c.Name(), m, err)
		}
	}
}

// A scalar read of a key gets the value set there, even when keys below
// it are set by the same or a higher layer; only a map read assembles
// the keys below.
func TestResolve_ScalarReadKeepsOwnValue(t *testing.T) {
	t.Setenv("APP_PORT", "8080")
	t.Setenv("APP_PORT_RANGE", "10")
	t.Setenv("APP_TIMEOUT", "5s")
	t.Setenv("APP_TIMEOUT_RETRY", "2")
	t.Setenv("APP_LOG", "info")
	t.Setenv("APP_LOG_FORMAT", "json")
	t.Setenv("APP_DB_POOL", "5")
	file, err := NewBufferSource("file", "yaml", []byte("db: postgres://h/x\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(WithoutWatch(), WithSource(NewOSEnvSource(WithEnvPrefix("APP_"))), WithSource(file))
	if err != nil {
		t.Fatal(err)
	}
	if s, _, err := r.GetString("log"); err != nil || s != "info" {
		t.Errorf("GetString(log) = %q, %v", s, err)
	}
	if s, _, err := r.GetString("db"); err != nil || s != "postgres://h/x" {
		t.Errorf("GetString(db) = %q, %v", s, err)
	}
	if v, _, _ := r.Get("log"); v.String() != "info" {
		t.Errorf("Get(log) = %v", v)
	}
	// Before a bind lists log.format as log_format, a map read of log
	// assembles the keys below it.
	var m map[string]string
	if err := r.UnmarshalKey("log", &m); err != nil || m["format"] != "json" || len(m) != 1 {
		t.Errorf("UnmarshalKey(log) into map = %v, %v", m, err)
	}
	if m, _, err := r.GetStringMap("log"); err != nil || m["format"] != "json" {
		t.Errorf("GetStringMap(log) = %v, %v", m, err)
	}
	var c struct {
		Port         int
		PortRange    int
		Timeout      time.Duration
		TimeoutRetry int
		Log          string
		LogFormat    string
		DB           string `recon:"db"`
		DBPool       int    `recon:"db_pool"`
	}
	if err := r.Bind(&c); err != nil {
		t.Fatal(err)
	}
	if c.Port != 8080 || c.PortRange != 10 || c.Timeout != 5*time.Second || c.TimeoutRetry != 2 {
		t.Errorf("numbers: %+v", c)
	}
	if c.Log != "info" || c.LogFormat != "json" || c.DB != "postgres://h/x" || c.DBPool != 5 {
		t.Errorf("strings: %+v", c)
	}
	var d time.Duration
	if err := r.UnmarshalKey("timeout", &d); err != nil || d != 5*time.Second {
		t.Errorf("UnmarshalKey(timeout) = %v, %v", d, err)
	}
}

// A section assembled for a map read lists a value read through two
// spellings once.
func TestResolve_SubtreeListsOneSpelling(t *testing.T) {
	t.Setenv("APP_DB_MAX_CONNS", "5")
	r, err := New(WithoutWatch(), WithSource(NewOSEnvSource(WithEnvPrefix("APP_"))))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		DB struct{ MaxConns int } `recon:"db"`
	}
	if err := r.Bind(&c); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"max_conns": "5"}
	if v, _, _ := r.Get("db"); !reflect.DeepEqual(unwrapValueDeep(v), want) {
		t.Errorf("Get(db) = %v, want %v", v, want)
	}
	var m map[string]any
	if err := r.UnmarshalKey("db", &m); err != nil || !reflect.DeepEqual(m, want) {
		t.Errorf("UnmarshalKey(db) = %v, %v", m, err)
	}
	var f struct {
		DB map[string]any `recon:"db"`
	}
	if err := r.Bind(&f); err != nil || !reflect.DeepEqual(f.DB, want) {
		t.Errorf("map field = %v, %v", f.DB, err)
	}
}

// An alias reads as its key does when keys below the key win over it.
func TestResolve_AliasOfOverriddenParent(t *testing.T) {
	t.Setenv("APP_LABELS_TEAM", "core")
	file, err := NewBufferSource("file", "yaml", []byte("labels:\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(WithoutWatch(), WithSource(NewOSEnvSource(WithEnvPrefix("APP_"))), WithSource(file))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterAlias("lbl", "labels"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := r.Get("lbl"); v.Kind() != MapKind {
		t.Errorf("Get(lbl) = %v (%s), want the map", v, v.Kind())
	}
	var c struct {
		L map[string]string `recon:"lbl"`
	}
	if err := r.Bind(&c); err != nil || c.L["team"] != "core" {
		t.Errorf("bind via alias = %v, %v", c.L, err)
	}
}

// An entry inside a map that a higher layer set at a parent wins over a
// lower layer's value for the same key.
func TestResolve_HigherParentMapEntryWins(t *testing.T) {
	hi := NewMapSource("hi", map[string]any{"db": map[string]string{"host": "hihost"}})
	file, err := NewBufferSource("file", "yaml", []byte("db:\n  host: filehost\n  port: 5\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(WithoutWatch(), WithSource(hi), WithSource(file))
	if err != nil {
		t.Fatal(err)
	}
	if v, _, _ := r.Get("db.host"); v.String() != "hihost" || v.Source() != "hi" {
		t.Errorf("Get(db.host) = %v from %s, want hihost from hi", v, v.Source())
	}
	var c struct {
		DB struct {
			Host string
			Port int
		} `recon:"db"`
	}
	if err := r.Bind(&c); err != nil || c.DB.Host != "hihost" || c.DB.Port != 5 {
		t.Errorf("bind = %+v, %v", c.DB, err)
	}
}

// A YAML float beyond the float64 range is a positioned error, as in
// JSON.
func TestYAML_FloatOverflowIsPositioned(t *testing.T) {
	_, err := YAML.Decode([]byte("a: 1\nb: [2, -1e400]\n"))
	pe, ok := errors.AsType[PositionError](err)
	if !ok {
		t.Fatalf("err = %v, want a PositionError", err)
	}
	if line, col := pe.Position(); line != 2 || col != 8 {
		t.Fatalf("position %d:%d, want 2:8 (%v)", line, col, err)
	}
	if msg := pe.Message(); msg != "value -1e400 overflows float64" || !errors.Is(err, strconv.ErrRange) {
		t.Fatalf("message %q (%v)", msg, err)
	}
	if m, err := YAML.Decode([]byte("a: '1e400'\n")); err != nil || m["a"] != "1e400" {
		t.Fatalf("quoted: %v, %v", m, err)
	}
}

type failingSchema struct{ err *error }

func (f failingSchema) Validate(map[string]any) error { return *f.err }

// A validator's error is reported under one "recon: " prefix.
func TestValidate_PrefixOnce(t *testing.T) {
	cause := fmt.Errorf("%w: jsonschema: max evaluations exceeded", ErrValidation)
	want := "recon: schema validation: validation failed: jsonschema: max evaluations exceeded"
	src := func() Option { return WithSource(NewMapSource("m", map[string]any{"a": 1})) }
	_, err := New(WithoutWatch(), src(), WithValidator(failingSchema{&cause}))
	if err == nil || err.Error() != want || !errors.Is(err, ErrValidation) {
		t.Fatalf("New: %v, want %q", err, want)
	}
	var fail error
	r, err := New(WithoutWatch(), src(), WithValidator(failingSchema{&fail}))
	if err != nil {
		t.Fatal(err)
	}
	fail = cause
	if err := r.Validate(); err == nil || err.Error() != want {
		t.Fatalf("Validate: %v, want %q", err, want)
	}
}
