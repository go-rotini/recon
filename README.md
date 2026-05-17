# go-rotini/recon

A Go data-in and configuration package: a typed registry that surveys every channel a program receives data through — environment variables, `.env` files, configuration files (YAML / TOML / JSONC / JSON), command-line flags, standard input, in-memory maps and buffers, programmatic defaults and overrides, and remote configuration stores — and resolves them through a documented precedence chain with first-class live reload, schema validation, and per-key provenance.

This package is used as the default configuration and data-in package for [rotini](https://github.com/go-rotini/rotini).

## Features

- One-line setup: `recon.New(...)` wires the go-rotini family defaults — YAML / TOML / JSONC / JSON / Dotenv codecs, the [go-rotini/fs](https://github.com/go-rotini/fs)-backed file watcher, the [go-rotini/jsonschema](https://github.com/go-rotini/jsonschema)-backed validator — and you're loading config
- Generic, type-safe API: `Get[T]`, `Bind[T]`, `Live[T]`, `PerSource[T]`, `Configs` — typed values where the type is statically known
- Pluggable seams: `Codec`, `SchemaValidator`, `WatcherFactory`, `FlagAdapter`, `RemoteBackend`, and `Source` itself — every default is replaceable behind its interface with a one-line option
- Built-in sources: `OSEnvSource`, `FileSource` / `FileSourceFS`, `YAMLSource` / `TOMLSource` / `JSONCSource` / `JSONSource` / `DotenvSource` (format-named convenience), `BufferSource`, `MapSource`, `StdinSource`, `FlagSource`, `RemoteSource`
- Documented precedence chain (explicit → flags → env → config → remote → stdin → defaults) with per-source `WithPrecedence`, per-key `PinSource`, alias graphs with cycle detection, and configurable merge strategies (`MergeShadow` / `MergeAppend`)
- Hierarchical keys (`Path`) with bracket-escaping for dotted segments, configurable delimiter, case-sensitive by default
- Aggregated multi-error reporting with per-field attribution (`*MultiError`, `FormatError`); `WithErrorBehavior` toggles `FailCollect` (default) and `FailFast`
- `context.Context` propagation through `ReloadContext` — cancellation flows to remote backends during refresh
- Atomic, lock-free reads on reload via `Live[T]` and `sync/atomic.Pointer` — readers always observe a complete, validated snapshot
- Per-key change detection across reloads (`Event.Changed` covers added / removed / changed / source-changed cases; `expand`-tagged keys compare post-expansion); `Event.Warnings` carries non-fatal `DeprecationWarning` values out of band
- Per-source value view (`PerSource[T]` / `PerSourceFor[T]`) for explicit resolve hooks — see every source's contribution to a key separately, then pick a winner
- Per-source provenance via `Describe` / `DescribeKey` / `KeyDescription` — every key knows which source supplied it and which other sources had a value for the same key
- Struct-tag system with full env-package compatibility: `required`, `notEmpty`, `default=`, `secret`, `immutable`, `expand`, `fromFile`, `unset`, `inline`, `base64`, `hex`, `layout=`, `separator=`, `kvSeparator=`, `deprecated[=msg]`, plus recon-specific `path=`, `source=`, `format=`, `aliases=`, `transform=`
- Per-field `immutable` enforcement: reload candidates that change a marked field are rejected (old / new values redacted for `secret` fields)
- Schema validation via [go-rotini/jsonschema](https://github.com/go-rotini/jsonschema); supply raw bytes via `WithSchema`, supply a constructor via `WithValidator(recon.JSONSchemaValidator(rawSchema))`, or plug in any other `SchemaValidator`
- Secret redaction (`Secret[T]` is a type alias of [`env.Secret[T]`](https://github.com/go-rotini/env) for free interop); customizable redactor via `WithSecretRedactor`
- Format-agnostic encode: `Save` / `SaveTo` write the current resolved view back to any registered codec; `GenerateTemplate` emits a stub config file from a schema or struct-tag defaults
- Path expansion (POSIX shell-style: `~`, `~user`, `$VAR`, `${VAR-default}`, `${VAR:-default}`, `${VAR:?msg}`); first-match-wins multi-path lookup via `WithSearchPaths`; `WithOptional` for missing-file tolerance
- Built-in support for `time.Duration`, `time.Time`, `[]byte` (raw / base64 / hex), arrays and maps; `RawValue` for deferred sub-document decoding
- Multi-named-config orchestration: `Configs` holds named registries (per the rotini spec's `configuration_files[]`) with multiplexed event delivery and per-config schemas / watchers / precedence
- `io/fs.FS`-backed `FileSourceFS` for testing with `testing/fstest.MapFS` and for loading from `embed.FS` bundles
- Remote-backend adapters (etcd / consul / vault / awsssm / k8s) live in their own modules — opt-in by `go get`; the core ships `NewInMemoryBackend` as a reference and for tests
- Minimal third-party footprint: composes the go-rotini family; transitively only `fsnotify` (via go-rotini/fs)

## Installation

```bash
go get github.com/go-rotini/recon
```

Requires Go 1.26 or later.

## Quick Start

```go
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/go-rotini/recon"
)

type Config struct {
	Port    int           `recon:"server.port,default=8080"`
	DBURL   string        `recon:"database.dsn,required,secret"`
	Timeout time.Duration `recon:"http.timeout,default=30s"`
	Tier    string        `recon:"tier,immutable,default=prod"`
}

func main() {
	// Sensible defaults: every bundled codec / watcher / validator is wired automatically.
	r, err := recon.New(
		recon.WithSource(recon.OSEnvSource()),
		recon.WithSource(recon.FileSource("config.yaml", recon.WithOptional(true))),
	)
	if err != nil {
		log.Fatal(recon.FormatError(r, err))
	}
	defer r.Close()

	var cfg Config
	if err := r.Unmarshal(&cfg); err != nil {
		log.Fatal(recon.FormatError(r, err))
	}
	fmt.Printf("%+v\n", cfg)
}
```

## Sources and precedence

Source chains are explicit and ordered. The first source to return ok=true for a key wins, with `Set` (programmatic override) always sitting above every source and `SetDefault` always below:

```go
r, err := recon.New(
	recon.WithSource(recon.OSEnvSource(recon.WithEnvPrefix("APP_"))), // wins by default
	recon.WithSource(recon.DotenvSource(".env.local")),               // dev overrides
	recon.WithSource(recon.FileSource("config.yaml")),                // baseline
)
```

Per-key overrides are first-class: `RegisterAlias` makes one path resolve to another (cycle-checked), and `PinSource(key, sourceName)` forces a key to resolve only from a named source.

## Live reload

`Live[T]` wraps the registry in an `atomic.Pointer[T]`-backed handle. Reads are O(1) and lock-free; readers always observe a complete, validated snapshot. Any source that implements `Watcher` participates; file-backed sources get watching from the registry's `WatcherFactory` (the bundled `FSWatcher` is backed by go-rotini/fs and is atomic-rename aware, debounced, multi-backend).

```go
type Config struct {
	Port    int    `recon:"server.port,default=8080"`
	LogLvl  string `recon:"log.level,default=info"`
}

live, err := recon.NewLive[Config](r)
if err != nil {
	log.Fatal(err)
}
defer live.Close()

go func() {
	for {
		cfg := live.Get() // *Config — never nil after NewLive succeeds
		serve(cfg)
	}
}()

go func() {
	for ev := range live.Events() {
		if ev.Err != nil {
			log.Printf("reload failed (source %q): %v", ev.Source, ev.Err)
			continue
		}
		for _, w := range ev.Warnings {
			log.Printf("deprecation: %s", w)
		}
		log.Printf("reload: changed=%v", ev.Changed)
	}
}()
```

The reload pipeline parses, validates, and snapshots a fresh candidate before swapping; a failed candidate retains the previous value and emits an error event.

## Per-source resolution (`PerSource[T]`)

When a single key needs custom precedence — env-only in containers, config-first for daemons, merged-list semantics — `PerSourceFor[T]` returns each source's contribution separately so you can pick a winner without reaching into the registry's internals:

```go
sources, _ := recon.PerSourceFor[int](r, "server.port")

if inContainer() && sources.Env.IsSet {
	return sources.Env.Value
}
return sources.Resolved().Value // what Get[int] would have returned
```

## Validation

Schema validation is opt-in. Supply a raw JSON Schema (or any other `SchemaValidator` implementation) and the registry runs it after every load:

```go
r, err := recon.New(
	recon.WithSource(recon.FileSource("config.yaml")),
	recon.WithSchema(schemaBytes),
)
```

For per-struct validation, implement `Validator` (or `ValidatorContext`) on the bind target — the decoder calls it after every field is populated.

## Multi-named configs (`Configs`)

For applications with multiple independent configuration namespaces — each with its own precedence, schema, and watch policy — `Configs` holds named registries and multiplexes events:

```go
cs := recon.NewConfigs()
db, _ := recon.New(recon.WithSource(recon.FileSource("db.yaml")))
srv, _ := recon.New(recon.WithSource(recon.FileSource("server.yaml")))
_ = cs.Register("database", db)
_ = cs.Register("server", srv)

go func() {
	for ev := range cs.Events() {
		log.Printf("%s reloaded: changed=%v", ev.Name, ev.Changed)
	}
}()
```

## Provenance and introspection

`Describe()` returns the full per-key view — which source supplied each value, which other sources had a value, whether the key is secret, what schema rule applies:

```go
for _, k := range r.Describe().Keys {
	fmt.Printf("%s = %s (from %s; aliases: %v)\n",
		k.Path, k.Value, k.Source, k.Aliases)
}
```

`Describe` redacts secret-tagged values automatically. The data feeds straight into a `myapp config show` / `myapp config sources` / `myapp config explain <key>` subcommand without further plumbing.

## Encoding back out (`Save` / `GenerateTemplate`)

`Save` writes the current resolved view to a file in the format you choose; `GenerateTemplate` emits a stub configuration file with every spec-declared key, populated with defaults and commented stubs for required-but-unset keys:

```go
// Dump current config to disk.
_ = r.Save("snapshot.yaml")

// Generate a stub for `myapp config init`.
out, _ := r.GenerateTemplate(recon.FormatYAML)
_ = os.WriteFile("config.example.yaml", out, 0o644)
```

## Documentation

Full API reference is available on [pkg.go.dev](https://pkg.go.dev/github.com/go-rotini/recon).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on how to contribute to this project.

## Code of Conduct

This project follows a code of conduct to ensure a welcoming community. See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## Security

To report a vulnerability, see [SECURITY.md](SECURITY.md).

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
