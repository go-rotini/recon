package recon_test

import (
	"fmt"
	"strings"

	"github.com/go-rotini/recon"
)

// ExampleWithMerge demonstrates [recon.MergeAppend] semantics: a
// lower-precedence source's slice contributes its elements first;
// the higher-precedence source's elements are appended. Scalar
// values still shadow under MergeAppend.
func ExampleWithMerge() {
	high := recon.NewMapSource("flags", map[string]any{
		"tags": []any{"hi-1", "hi-2"},
	})
	low := recon.NewMapSource("config", map[string]any{
		"tags": []any{"lo-1", "lo-2"},
	})
	r, _ := recon.New(
		recon.WithSources(high, low),
		recon.WithMerge(recon.MergeAppend),
	)
	defer func() { _ = r.Close() }()

	tags, _, _ := r.GetStringSlice("tags")
	fmt.Println(tags)
	// Output: [lo-1 lo-2 hi-1 hi-2]
}

// ExampleWithSchema validates the registry's snapshot against a
// JSON Schema on every reload. Construction returns the compile
// error directly when the schema is malformed.
func ExampleWithSchema() {
	schema := []byte(`{
		"type": "object",
		"properties": {
			"port": {"type": "integer", "minimum": 1, "maximum": 65535}
		},
		"required": ["port"]
	}`)
	r, err := recon.New(recon.WithSchema(schema))
	if err != nil {
		panic(err)
	}
	defer func() { _ = r.Close() }()

	if err := r.Set("port", 8080); err != nil {
		fmt.Println("invalid:", err)
	} else {
		fmt.Println("valid")
	}
	// Output: valid
}

// ExampleFormatError renders a multi-error from Bind into a single
// human-readable summary suitable for direct printing.
func ExampleFormatError() {
	type Config struct {
		Port int    `recon:"port,required"`
		Name string `recon:"name,required"`
	}
	r, _ := recon.New()
	defer func() { _ = r.Close() }()

	var c Config
	err := r.Bind(&c)
	// FormatError prints one bullet per error in the order Bind
	// surfaced them — Bind walks fields in declaration order, which
	// is stable but uninteresting for the example. Use the unordered
	// directive so the comparison sorts lines first.
	fmt.Println(recon.FormatError(r, err))
	// Unordered output:
	// recon: 2 errors:
	//   • name: missing required value
	//   • port: missing required value
}

// ExampleRegistry_GenerateTemplate emits a stub document populated
// with the registry's known defaults — useful as the `myapp config
// init` entry point.
func ExampleRegistry_GenerateTemplate() {
	r, _ := recon.New()
	defer func() { _ = r.Close() }()
	_ = r.SetDefault("server.port", 8080)
	_ = r.SetDefault("server.host", "localhost")

	out, err := r.GenerateTemplate(recon.FormatJSON)
	if err != nil {
		panic(err)
	}
	// Re-decode for deterministic output (JSON key ordering is
	// not guaranteed by the stdlib encoder).
	decoded, _ := recon.JSON.Decode(out)
	server := decoded["server"].(map[string]any)
	fmt.Printf("host=%v port=%v\n", server["host"], server["port"])
	// Output: host=localhost port=8080
}

// ExamplePerSourceFor shows how to inspect every source's
// contribution to one key independently — the foundation for
// per-key "config explain" tooling and for resolve-by-policy hooks
// that want to deviate from the registry's default precedence.
func ExamplePerSourceFor() {
	flags := recon.NewMapSource("flags", map[string]any{"port": 9000})
	env := recon.NewMapSource("env", map[string]any{"port": 8080})
	r, _ := recon.New(recon.WithSources(flags, env))
	defer func() { _ = r.Close() }()

	ps, _ := recon.PerSourceFor[int](r, "port")
	for _, entry := range ps.Sources {
		fmt.Printf("%s: %d (set=%v)\n", entry.Source, entry.Value, entry.IsSet)
	}
	fmt.Printf("resolved: %d (winner=%s)\n", ps.Resolved.Value, ps.Resolved.Source)
	// Output:
	// flags: 9000 (set=true)
	// env: 8080 (set=true)
	// resolved: 9000 (winner=flags)
}

// ExampleRegistry_DrainWarnings consumes deprecation warnings the
// bind walker queued when a `deprecated`-tagged field actually had
// a value supplied by a source — the migration window's "you're
// still using the old key" notice.
func ExampleRegistry_DrainWarnings() {
	type C struct {
		Old string `recon:"old_key,deprecated=use 'new_key' instead"`
	}
	r, _ := recon.New()
	defer func() { _ = r.Close() }()
	_ = r.Set("old_key", "value")

	var c C
	_ = r.Bind(&c)

	warnings := r.DrainWarnings()
	for _, w := range warnings {
		// Trim a stable representation for the example output.
		msg := strings.TrimPrefix(w.Message, "recon: ")
		fmt.Printf("warning at %s: %s\n", w.Path, msg)
	}
	// Output: warning at old_key: use 'new_key' instead
}
