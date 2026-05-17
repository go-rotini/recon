package recon

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// --- helpers ---------------------------------------------------------

// bindRegistry constructs a registry seeded with the supplied flat
// key→value pairs. Kept tiny so test bodies stay focused on the bind
// assertion itself.
func bindRegistry(t *testing.T, kv map[string]any) *Registry {
	t.Helper()
	r := newRegistry(t)
	for k, v := range kv {
		if err := r.Set(k, v); err != nil {
			t.Fatalf("seed %s: %v", k, err)
		}
	}
	return r
}

// --- 1. flat scalars -------------------------------------------------

func TestBind_FlatScalars(t *testing.T) {
	type C struct {
		Name  string  `recon:"name"`
		Port  int     `recon:"port"`
		Rate  float64 `recon:"rate"`
		Debug bool    `recon:"debug"`
	}
	r := bindRegistry(t, map[string]any{
		"name":  "rotini",
		"port":  8080,
		"rate":  1.5,
		"debug": true,
	})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Name != "rotini" || c.Port != 8080 || c.Rate != 1.5 || !c.Debug {
		t.Fatalf("c=%+v", c)
	}
}

// --- 2. nested structs (dot path) -----------------------------------

func TestBind_NestedStruct(t *testing.T) {
	type Server struct {
		Host string `recon:"host"`
		Port int    `recon:"port"`
	}
	type C struct {
		Server Server `recon:"server"`
	}
	r := bindRegistry(t, map[string]any{
		"server.host": "localhost",
		"server.port": 8080,
	})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Server.Host != "localhost" || c.Server.Port != 8080 {
		t.Fatalf("nested=%+v", c)
	}
}

// --- 3. embedded structs (anonymous, no inline) ----------------------

func TestBind_EmbeddedStruct(t *testing.T) {
	type Server struct {
		Host string `recon:"host"`
		Port int    `recon:"port"`
	}
	type C struct {
		Server // embedded — uses lower-cased type name as prefix
	}
	r := bindRegistry(t, map[string]any{
		"server.host": "localhost",
		"server.port": 9090,
	})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Host != "localhost" || c.Port != 9090 {
		t.Fatalf("embedded=%+v", c)
	}
}

// --- 4. inline (flatten) --------------------------------------------

func TestBind_InlineFlatten(t *testing.T) {
	type Server struct {
		Host string `recon:"host"`
		Port int    `recon:"port"`
	}
	type C struct {
		Server `recon:",inline"`
	}
	r := bindRegistry(t, map[string]any{
		"host": "localhost",
		"port": 9090,
	})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Host != "localhost" || c.Port != 9090 {
		t.Fatalf("inline=%+v", c)
	}
}

// --- 5. pointer to struct -------------------------------------------

func TestBind_PointerToStruct(t *testing.T) {
	type Server struct {
		Port int `recon:"port"`
	}
	type C struct {
		Server *Server `recon:"server"`
	}
	r := bindRegistry(t, map[string]any{
		"server.port": 9000,
	})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Server == nil {
		t.Fatal("server is nil after Bind")
	}
	if c.Server.Port != 9000 {
		t.Fatalf("server.port=%d", c.Server.Port)
	}
}

// --- 6. pointer to scalar -------------------------------------------

func TestBind_PointerToScalar(t *testing.T) {
	type C struct {
		Port *int `recon:"port"`
	}
	r := bindRegistry(t, map[string]any{"port": 7000})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Port == nil || *c.Port != 7000 {
		t.Fatalf("port=%v", c.Port)
	}
}

// --- 7. slice from native []any -------------------------------------

func TestBind_SliceFromSlice(t *testing.T) {
	type C struct {
		Tags []string `recon:"tags"`
	}
	r := bindRegistry(t, map[string]any{
		"tags": []any{"a", "b", "c"},
	})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if !slices.Equal(c.Tags, []string{"a", "b", "c"}) {
		t.Fatalf("tags=%v", c.Tags)
	}
}

// --- 8. slice from delimited string ---------------------------------

func TestBind_SliceFromString(t *testing.T) {
	type C struct {
		Hosts []string `recon:"hosts,separator=;"`
	}
	r := bindRegistry(t, map[string]any{
		"hosts": "a;b;c",
	})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if !slices.Equal(c.Hosts, []string{"a", "b", "c"}) {
		t.Fatalf("hosts=%v", c.Hosts)
	}
}

// --- 9. int slice (typed) -------------------------------------------

func TestBind_IntSliceFromString(t *testing.T) {
	type C struct {
		Ports []int `recon:"ports"`
	}
	r := bindRegistry(t, map[string]any{"ports": "1,2,3"})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if !slices.Equal(c.Ports, []int{1, 2, 3}) {
		t.Fatalf("ports=%v", c.Ports)
	}
}

// --- 10. map[string]string ------------------------------------------

func TestBind_MapStringString(t *testing.T) {
	type C struct {
		Labels map[string]string `recon:"labels"`
	}
	r := bindRegistry(t, map[string]any{
		"labels": map[string]any{
			"env":  "prod",
			"tier": "frontend",
		},
	})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Labels["env"] != "prod" || c.Labels["tier"] != "frontend" {
		t.Fatalf("labels=%v", c.Labels)
	}
}

// --- 11. map from delimited string ----------------------------------

func TestBind_MapFromString(t *testing.T) {
	type C struct {
		Labels map[string]string `recon:"labels,separator=,,kvSeparator=:"`
	}
	r := bindRegistry(t, map[string]any{
		"labels": "env:prod,tier:frontend",
	})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Labels["env"] != "prod" || c.Labels["tier"] != "frontend" {
		t.Fatalf("labels=%v", c.Labels)
	}
}

// --- 12. time.Time --------------------------------------------------

func TestBind_TimeRFC3339(t *testing.T) {
	type C struct {
		Birthday time.Time `recon:"birthday"`
	}
	r := bindRegistry(t, map[string]any{
		"birthday": "2021-04-15T10:30:00Z",
	})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Birthday.Year() != 2021 {
		t.Fatalf("birthday=%v", c.Birthday)
	}
}

// --- 13. time.Time with custom layout -------------------------------

func TestBind_TimeCustomLayout(t *testing.T) {
	type C struct {
		At time.Time `recon:"at,layout=2006-01-02"`
	}
	r := bindRegistry(t, map[string]any{"at": "2024-12-25"})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.At.Year() != 2024 || c.At.Month() != time.December || c.At.Day() != 25 {
		t.Fatalf("at=%v", c.At)
	}
}

// --- 14. time.Duration ----------------------------------------------

func TestBind_Duration(t *testing.T) {
	type C struct {
		Timeout time.Duration `recon:"timeout"`
	}
	r := bindRegistry(t, map[string]any{"timeout": "5s"})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Timeout != 5*time.Second {
		t.Fatalf("timeout=%v", c.Timeout)
	}
}

// --- 15. []byte via base64 ------------------------------------------

func TestBind_BytesBase64(t *testing.T) {
	type C struct {
		Key []byte `recon:"key,base64"`
	}
	in := "deadbeef"
	enc := base64.StdEncoding.EncodeToString([]byte(in))
	r := bindRegistry(t, map[string]any{"key": enc})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if string(c.Key) != in {
		t.Fatalf("key=%q, want %q", c.Key, in)
	}
}

// --- 16. []byte via hex ---------------------------------------------

func TestBind_BytesHex(t *testing.T) {
	type C struct {
		Sig []byte `recon:"sig,hex"`
	}
	want := []byte{0xde, 0xad, 0xbe, 0xef}
	r := bindRegistry(t, map[string]any{"sig": hex.EncodeToString(want)})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if !slices.Equal(c.Sig, want) {
		t.Fatalf("sig=%v", c.Sig)
	}
}

// --- 17. required missing -------------------------------------------

func TestBind_RequiredMissing(t *testing.T) {
	type C struct {
		DSN string `recon:"db.dsn,required"`
	}
	r := bindRegistry(t, map[string]any{})
	var c C
	err := r.Bind(&c)
	if !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("err=%v, want wrap of ErrMissingRequired", err)
	}
}

// --- 18. required present -------------------------------------------

func TestBind_RequiredPresent(t *testing.T) {
	type C struct {
		DSN string `recon:"db.dsn,required"`
	}
	r := bindRegistry(t, map[string]any{"db.dsn": "postgres://x"})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.DSN != "postgres://x" {
		t.Fatalf("dsn=%q", c.DSN)
	}
}

// --- 19. default applied --------------------------------------------

func TestBind_DefaultApplied(t *testing.T) {
	type C struct {
		Port int    `recon:"port,default=8080"`
		Env  string `recon:"env,default=dev"`
	}
	r := bindRegistry(t, map[string]any{})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Port != 8080 || c.Env != "dev" {
		t.Fatalf("defaults=%+v", c)
	}
}

// --- 20. notEmpty rejects empty -------------------------------------

func TestBind_NotEmptyRejectsEmpty(t *testing.T) {
	type C struct {
		Name string `recon:"name,notEmpty"`
	}
	r := bindRegistry(t, map[string]any{"name": ""})
	var c C
	err := r.Bind(&c)
	if !errors.Is(err, ErrEmptyValue) {
		t.Fatalf("err=%v, want wrap of ErrEmptyValue", err)
	}
}

// --- 21. env.Secret roundtrip --------------------------------------

func TestBind_Secret(t *testing.T) {
	type C struct {
		Token Secret[string] `recon:"token"`
	}
	r := bindRegistry(t, map[string]any{"token": "hunter2"})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Token.Value() != "hunter2" {
		t.Fatalf("token.Value=%q", c.Token.Value())
	}
	// Stringer must redact.
	if s := c.Token.String(); strings.Contains(s, "hunter2") {
		t.Fatalf("token.String=%q leaks secret", s)
	}
}

// --- 22. tag fallback (env, json, yaml, toml) -----------------------

func TestBind_TagFallback(t *testing.T) {
	type C struct {
		ViaEnv  string `env:"FROM_ENV"`
		ViaJSON string `json:"from_json"`
		ViaYAML string `yaml:"from_yaml"`
		ViaTOML string `toml:"from_toml"`
	}
	r := bindRegistry(t, map[string]any{
		"FROM_ENV":  "ev",
		"from_json": "jv",
		"from_yaml": "yv",
		"from_toml": "tv",
	})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.ViaEnv != "ev" || c.ViaJSON != "jv" || c.ViaYAML != "yv" || c.ViaTOML != "tv" {
		t.Fatalf("fallback=%+v", c)
	}
}

// --- 23. transform=snake -------------------------------------------

func TestBind_TransformSnake(t *testing.T) {
	type C struct {
		MyField string `recon:",transform=snake"`
	}
	r := bindRegistry(t, map[string]any{"my_field": "v"})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.MyField != "v" {
		t.Fatalf("MyField=%q", c.MyField)
	}
}

// --- 24. path override ---------------------------------------------

func TestBind_PathOverride(t *testing.T) {
	type C struct {
		Port int `recon:"ignored,path=server.port"`
	}
	r := bindRegistry(t, map[string]any{"server.port": 8080})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Port != 8080 {
		t.Fatalf("port=%d", c.Port)
	}
}

// --- 25. aliases ----------------------------------------------------

func TestBind_Aliases(t *testing.T) {
	type C struct {
		Port int `recon:"port,aliases=server.port;PORT"`
	}
	r := bindRegistry(t, map[string]any{"PORT": 9999})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Port != 9999 {
		t.Fatalf("port via alias=%d", c.Port)
	}
}

// --- 26. skip tag --------------------------------------------------

func TestBind_SkipTag(t *testing.T) {
	type C struct {
		Skipped string `recon:"-"`
		Used    string `recon:"used"`
	}
	r := bindRegistry(t, map[string]any{
		"skipped": "should-not-be-read",
		"used":    "ok",
	})
	c := C{Skipped: "untouched"}
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Skipped != "untouched" {
		t.Fatalf("Skipped overwritten: %q", c.Skipped)
	}
	if c.Used != "ok" {
		t.Fatalf("Used=%q", c.Used)
	}
}

// --- 27. unexported skipped -----------------------------------------

func TestBind_UnexportedSkipped(t *testing.T) {
	type C struct {
		Public  string `recon:"public"`
		private string
	}
	r := bindRegistry(t, map[string]any{"public": "x", "private": "should-not-bind"})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Public != "x" {
		t.Fatalf("Public=%q", c.Public)
	}
	if c.private != "" {
		t.Fatalf("private bound: %q", c.private)
	}
}

// --- 28. invalid target -------------------------------------------

func TestBind_NonPointerTarget(t *testing.T) {
	type C struct{ X int }
	r := newRegistry(t)
	err := r.Bind(C{})
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err=%v, want ErrInvalidPath", err)
	}
}

func TestBind_NilPointerTarget(t *testing.T) {
	r := newRegistry(t)
	var p *struct{ X int }
	err := r.Bind(p)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err=%v, want ErrInvalidPath", err)
	}
}

// --- 29. validator hook --------------------------------------------

type validatedConfig struct {
	Port int `recon:"port"`
}

func (c *validatedConfig) Validate() error {
	if c.Port == 0 {
		return errors.New("port required")
	}
	return nil
}

func TestBind_ValidatorHook(t *testing.T) {
	r := bindRegistry(t, map[string]any{})
	var c validatedConfig
	err := r.Bind(&c)
	if err == nil {
		t.Fatal("Validate hook not invoked")
	}
	if !strings.Contains(err.Error(), "port required") {
		t.Fatalf("err=%v", err)
	}
}

// --- 30. validator-context hook ------------------------------------

type validatedCtxConfig struct {
	Port int `recon:"port"`
}

func (c *validatedCtxConfig) Validate(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil ctx threaded through")
	}
	if c.Port < 1 {
		return errors.New("port must be > 0")
	}
	return nil
}

func TestBind_ValidatorContextHook(t *testing.T) {
	r := bindRegistry(t, map[string]any{"port": 0})
	var c validatedCtxConfig
	err := r.BindContext(t.Context(), &c)
	if err == nil {
		t.Fatal("ValidatorContext hook not invoked")
	}
	if !strings.Contains(err.Error(), "port must be > 0") {
		t.Fatalf("err=%v", err)
	}
}

// --- 31. custom decoder --------------------------------------------

type myInt int

func TestBind_CustomDecoder(t *testing.T) {
	type C struct {
		N myInt `recon:"n"`
	}
	r := bindRegistry(t, map[string]any{"n": "forty-two"})
	var c C
	dec := WithCustomDecoder(func(v Value) (myInt, error) {
		s, _ := v.AsString()
		if s == "forty-two" {
			return 42, nil
		}
		return 0, errors.New("only forty-two supported")
	})
	if err := r.Bind(&c, dec); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.N != 42 {
		t.Fatalf("N=%d", c.N)
	}
}

// --- 32. FailFast vs FailCollect ------------------------------------

func TestBind_FailCollectAccumulates(t *testing.T) {
	type C struct {
		A string `recon:"a,required"`
		B string `recon:"b,required"`
	}
	r := bindRegistry(t, map[string]any{})
	var c C
	err := r.Bind(&c)
	var multi *MultiError
	if !errors.As(err, &multi) {
		t.Fatalf("err=%T, want *MultiError", err)
	}
	if len(multi.Errors) != 2 {
		t.Fatalf("got %d errors, want 2", len(multi.Errors))
	}
}

func TestBind_FailFastStopsAtFirst(t *testing.T) {
	type C struct {
		A string `recon:"a,required"`
		B string `recon:"b,required"`
	}
	r := bindRegistry(t, map[string]any{})
	var c C
	err := r.Bind(&c, WithDecodeErrorBehavior(FailFast))
	if err == nil {
		t.Fatal("FailFast did not produce an error")
	}
	// Under FailFast we expect exactly one error (the first one).
	var multi *MultiError
	if errors.As(err, &multi) && len(multi.Errors) > 1 {
		t.Fatalf("FailFast returned %d errors", len(multi.Errors))
	}
}

// --- 33. closed registry rejects bind -------------------------------

func TestBind_ClosedRegistryRejected(t *testing.T) {
	r, _ := New()
	_ = r.Close()
	var c struct{ X int }
	err := r.Bind(&c)
	if !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("err=%v, want ErrRegistryClosed", err)
	}
}

// --- 34. type-mismatch produces CoercionError -----------------------

func TestBind_CoercionError(t *testing.T) {
	type C struct {
		Port int `recon:"port"`
	}
	// A map-valued source cannot coerce into int.
	r := bindRegistry(t, map[string]any{
		"port": map[string]any{"nested": 1},
	})
	var c C
	err := r.Bind(&c)
	if err == nil {
		t.Fatal("expected coercion error, got nil")
	}
	if !errors.Is(err, ErrTypeMismatch) && !errors.Is(err, ErrCoercion) {
		t.Fatalf("err=%v, want wrap of ErrCoercion or ErrTypeMismatch", err)
	}
}

// --- 35. unmarshaler hook ------------------------------------------

type customDecodeField struct{ Got string }

func (c *customDecodeField) UnmarshalRecon(v Value) error {
	s, _ := v.AsString()
	c.Got = "recon:" + s
	return nil
}

func TestBind_UnmarshalerHook(t *testing.T) {
	type C struct {
		Field customDecodeField `recon:"field"`
	}
	r := bindRegistry(t, map[string]any{"field": "x"})
	var c C
	if err := r.Bind(&c); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if c.Field.Got != "recon:x" {
		t.Fatalf("Got=%q, want recon:x", c.Field.Got)
	}
}
