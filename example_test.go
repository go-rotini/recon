package recon_test

import (
	"fmt"
	"sort"
	"time"

	"github.com/go-rotini/recon"
)

// The examples below double as documentation: each demonstrates one
// canonical entry point so godoc readers see the API shape end-to-end.

// ExampleNew shows the minimum useful registry construction:
// a single in-memory source, register, read, close.
func ExampleNew() {
	src := recon.NewMapSource("config", map[string]any{
		"server": map[string]any{
			"host": "localhost",
			"port": 8080,
		},
	})

	r, err := recon.New(recon.WithSource(src))
	if err != nil {
		panic(err)
	}
	defer func() { _ = r.Close() }()

	host, _, _ := r.GetString("server.host")
	port, _, _ := r.GetInt("server.port")
	fmt.Printf("%s:%d\n", host, port)
	// Output: localhost:8080
}

// ExampleRegistry_Bind populates a struct from the registry via the
// recon: tag. Defaults, required fields, and nested paths all work
// the same way they do in struct-driven config loaders.
func ExampleRegistry_Bind() {
	src := recon.NewMapSource("config", map[string]any{
		"server": map[string]any{
			"host": "localhost",
			"port": 8080,
		},
		"debug": true,
	})
	r, _ := recon.New(recon.WithSource(src))
	defer func() { _ = r.Close() }()

	type Config struct {
		Host    string        `recon:"server.host,required"`
		Port    int           `recon:"server.port,default=80"`
		Debug   bool          `recon:"debug"`
		Timeout time.Duration `recon:"timeout,default=30s"`
	}
	var cfg Config
	if err := r.Bind(&cfg); err != nil {
		panic(err)
	}
	fmt.Printf("host=%s port=%d debug=%v timeout=%s\n",
		cfg.Host, cfg.Port, cfg.Debug, cfg.Timeout)
	// Output: host=localhost port=8080 debug=true timeout=30s
}

// ExampleNewLive demonstrates the typed live-config pattern. The
// initial bind runs synchronously inside NewLive; subsequent
// reloads rebind atomically. Live.Get is a single atomic load —
// safe for hot paths.
func ExampleNewLive() {
	src := recon.NewMapSource("config", map[string]any{
		"port": 8080,
		"name": "rotini",
	})
	r, _ := recon.New(recon.WithSource(src))
	defer func() { _ = r.Close() }()

	type Config struct {
		Port int    `recon:"port"`
		Name string `recon:"name"`
	}
	live, err := recon.NewLive[Config](r)
	if err != nil {
		panic(err)
	}
	defer func() { _ = live.Close() }()

	cfg := live.Get() // *Config — atomic load
	fmt.Printf("%s on :%d\n", cfg.Name, cfg.Port)
	// Output: rotini on :8080
}

// ExampleRegistry_Get_provenance shows how to inspect WHICH source
// supplied a value — the foundation for "myapp config explain"
// tooling and for debugging precedence surprises.
func ExampleRegistry_Get_provenance() {
	flag := recon.NewMapSource("flags", map[string]any{"port": 9000})
	env := recon.NewMapSource("env", map[string]any{"port": 8080})
	r, _ := recon.New(recon.WithSources(flag, env))
	defer func() { _ = r.Close() }()

	v, _, _ := r.Get("port")
	fmt.Println("value:", v.String())
	fmt.Println("source:", v.Source())
	fmt.Println("shadowed:", r.Snapshot().SourceFor(recon.MakePath("port")))
	// Output:
	// value: 9000
	// source: flags
	// shadowed: [flags env]
}

// ExampleRegistry_RegisterAlias maps an alternate key onto a
// canonical one. Both Get("port") and Get("server.port") return
// the same value after this call.
func ExampleRegistry_RegisterAlias() {
	r, _ := recon.New()
	defer func() { _ = r.Close() }()

	_ = r.Set("server.port", 8080)
	_ = r.RegisterAlias("port", "server.port")

	via := func(key string) any {
		v, _, _ := r.Get(key)
		i, _ := v.AsInt64()
		return i
	}
	fmt.Println("canonical:", via("server.port"))
	fmt.Println("alias:   ", via("port"))
	// Output:
	// canonical: 8080
	// alias:    8080
}

// ExampleRegistry_Sub returns a registry view rooted at a sub-tree.
// Reads, writes, and AllKeys operate relative to the prefix.
func ExampleRegistry_Sub() {
	src := recon.NewMapSource("config", map[string]any{
		"server": map[string]any{
			"host": "localhost",
			"port": 8080,
		},
		"db": map[string]any{
			"dsn": "postgres://x",
		},
	})
	r, _ := recon.New(recon.WithSource(src))
	defer func() { _ = r.Close() }()

	server := r.Sub("server")
	host, _, _ := server.GetString("host")
	port, _, _ := server.GetInt("port")
	fmt.Printf("server view: host=%s port=%d\n", host, port)
	// Output: server view: host=localhost port=8080
}

// ExampleRegistry_Describe surfaces per-key provenance + redacted
// values for "myapp config show" output. The Sources slice lists
// every source in precedence order.
func ExampleRegistry_Describe() {
	high := recon.NewMapSource("flags", map[string]any{"port": 9000})
	low := recon.NewMapSource("env", map[string]any{"port": 8080})
	r, _ := recon.New(recon.WithSources(high, low))
	defer func() { _ = r.Close() }()
	_ = r.Set("token", "hunter2")
	r.MarkSecret("token")

	d := r.Describe()
	for _, k := range d.Keys {
		fmt.Printf("%s = %s (source=%s, secret=%v)\n",
			k.Path, k.Value, k.Source, k.Secret)
	}
	// Output:
	// port = 9000 (source=flags, secret=false)
	// token = *** (source=explicit, secret=true)
}

// ExampleRegistry_Save serializes the snapshot through a bundled
// codec. Pass WithSaveFormat with the registry-wide [Save] (or use
// SaveTo with a path whose extension implies the format).
func ExampleRegistry_Save() {
	r, _ := recon.New()
	defer func() { _ = r.Close() }()
	_ = r.Set("server.host", "localhost")
	_ = r.Set("server.port", 8080)

	out, err := r.SaveString(recon.WithSaveFormat(recon.FormatJSON))
	if err != nil {
		panic(err)
	}
	// Re-decode for deterministic output (JSON key ordering is
	// implementation-defined in encoding/json).
	decoded, _ := recon.JSON.Decode([]byte(out))
	server := decoded["server"].(map[string]any)
	fmt.Printf("host=%v port=%v\n", server["host"], server["port"])
	// Output: host=localhost port=8080
}

// ExampleOSEnvSource reads environment variables through the
// canonical OS-env source. Combine with WithEnvPrefix to scope
// to a single namespace.
func ExampleOSEnvSource() {
	// In a real program you'd pass NewOSEnvSource() directly to
	// recon.New; this example uses a Map source to keep the output
	// deterministic across test runs.
	src := recon.NewMapSource("env", map[string]any{
		"APP_PORT": "8080",
		"APP_NAME": "rotini",
	})
	r, _ := recon.New(recon.WithSource(src))
	defer func() { _ = r.Close() }()

	keys := r.AllKeys()
	sort.Strings(keys)
	for _, k := range keys {
		v, _, _ := r.GetString(k)
		fmt.Printf("%s=%s\n", k, v)
	}
	// Output:
	// APP_NAME=rotini
	// APP_PORT=8080
}

// ExampleMemoryBackend demonstrates the in-memory RemoteBackend
// reference impl. Real adapters (etcd, consul, vault, …) live in
// separate sub-modules; the in-memory backend is for tests and for
// prototyping the remote-source plumbing.
func ExampleMemoryBackend() {
	backend := recon.NewInMemoryBackend()
	backend.Put("app/port", "8080")
	backend.Put("app/host", "localhost")

	src, err := recon.NewRemoteSource("remote", backend,
		recon.WithRemotePrefix("app/"),
		recon.WithRemoteTrimPrefix(true),
	)
	if err != nil {
		panic(err)
	}
	r, _ := recon.New(recon.WithSource(src))
	defer func() { _ = r.Close() }()

	host, _, _ := r.GetString("host")
	port, _, _ := r.GetString("port")
	fmt.Printf("%s:%s\n", host, port)
	// Output: localhost:8080
}

// ExampleJSONSchemaValidator wires a JSON Schema into the registry
// so every reload is validated against it. Validation failures
// retain the previous snapshot; the registry keeps running.
func ExampleJSONSchemaValidator() {
	schema := []byte(`{
		"type": "object",
		"properties": {
			"port": {"type": "integer", "minimum": 1, "maximum": 65535}
		},
		"required": ["port"]
	}`)
	validator, err := recon.NewJSONSchemaValidator(schema)
	if err != nil {
		panic(err)
	}

	r, _ := recon.New(recon.WithValidator(validator))
	defer func() { _ = r.Close() }()

	_ = r.Set("port", 8080)
	if err := r.Reload(); err != nil {
		fmt.Println("invalid:", err)
	} else {
		fmt.Println("valid")
	}
	// Output: valid
}

// ExampleFlagAdapter demonstrates implementing the FlagAdapter
// interface against a tiny argv-parser shim. Real callers wrap
// their library of choice — stdlib flag, pflag, kong, rotini —
// in this same shape.
func ExampleFlagAdapter() {
	parsed := exampleFlags{set: map[string]any{"port": 9000}}
	flags, err := recon.NewFlagSource(parsed)
	if err != nil {
		panic(err)
	}
	r, _ := recon.New(recon.WithSource(flags))
	defer func() { _ = r.Close() }()

	v, _, _ := r.GetInt("port")
	fmt.Println("port:", v)
	// Output: port: 9000
}

// exampleFlags is the tiny FlagAdapter shim ExampleFlagAdapter
// drives. A real adapter would query its parser library for the
// "was this flag set?" signal.
type exampleFlags struct{ set map[string]any }

func (e exampleFlags) Names() []string {
	out := make([]string, 0, len(e.set))
	for k := range e.set {
		out = append(out, k)
	}
	return out
}

func (e exampleFlags) Lookup(name string) (any, bool) {
	v, ok := e.set[name]
	return v, ok
}
