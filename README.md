# go-rotini/recon

A Go data-in and configuration package: a typed registry that surveys every channel a program receives data through — environment variables, `.env` files, configuration files (YAML / TOML / JSONC / JSON), command-line flags, standard input, in-memory maps and buffers, programmatic defaults and overrides, and remote configuration stores — and resolves them through a documented precedence chain with first-class live reload, schema validation, and per-key provenance.

This package is used as the default configuration and data-in package for [rotini](https://github.com/go-rotini/rotini).

## Features

- One-line setup: `recon.New(...)` wires the go-rotini family defaults — YAML / TOML / JSONC / JSON / Dotenv codecs, the [go-rotini/fs](https://github.com/go-rotini/fs)-backed file watcher, the [go-rotini/jsonschema](https://github.com/go-rotini/jsonschema)-backed validator — and you're loading config
- Generic, type-safe API: `Get[T]`, `Bind`, `Live[T]`, `PerSourceFor[T]`, `Configs` — typed values where the type is statically known
- Pluggable seams: `Codec`, `SchemaValidator`, `WatcherFactory`, `FlagAdapter`, `RemoteBackend`, and `Source` itself — every default is replaceable behind its interface with a one-line option
- Built-in sources: `NewOSEnvSource`, `NewFileSource` / `NewFileSourceFS`, `YAMLSource` / `TOMLSource` / `JSONCSource` / `JSONSource` / `DotenvSource` (format-named convenience), `NewBufferSource`, `NewMapSource`, `NewStdinSource`, `NewFlagSource`, `NewRemoteSource`
- Documented precedence chain (explicit → flags → env → config → remote → stdin → defaults) with per-source `WithPrecedence`, per-key `PinSource`, alias graphs with cycle detection
- Hierarchical keys (`Path`) with bracket-escaping for dotted segments, configurable delimiter, case-sensitive by default
- Aggregated multi-error reporting with per-field attribution (`*MultiError`) and a `FormatError(r, err)` pretty-printer that surfaces path, source provenance, and the precedence chain; `WithErrorBehavior` toggles `FailCollect` (default) and `FailFast`
- `context.Context` propagation through `ReloadContext` and `BindContext`
- Atomic, lock-free reads on reload via `Live[T]` and `sync/atomic.Pointer` — readers always observe a complete, validated snapshot
- Per-key change detection across reloads (`Event.Changed` covers added / removed / modified cases); `Event.Warnings` carries non-fatal `DeprecationWarning` values out of band
- Per-source provenance via `Describe` / `DescribeKey` / `KeyDescription` — every key knows which source supplied it and which other sources had a value for the same key
- Struct-tag system: `required`, `notEmpty`, `default=`, `secret`, `immutable`, `inline`, `base64`, `hex`, `layout=`, `separator=`, `kvSeparator=`, plus recon-specific `path=`, `source=`, `aliases=`, `transform=`
- `immutable`-tagged fields are baselined at first `Bind`; subsequent reload candidates that change a baselined value are rejected (the old / new pair is redacted via `WithSecretRedactor` when the field is also `secret`)
- Schema validation via [go-rotini/jsonschema](https://github.com/go-rotini/jsonschema); supply raw bytes via `WithSchema(rawJSON)` for the one-line case or `WithValidator(...)` for a pre-built `SchemaValidator` — including a custom one behind the same interface
- Secret redaction: `Secret[T]` is a type alias of [`env.Secret[T]`](https://github.com/go-rotini/env) for free interop; the `secret` struct tag and `MarkSecret(key)` both feed `Describe` and `Save` redaction; customizable redactor via `WithSecretRedactor`
- Format-agnostic encode: `Save` / `SaveTo` write the current resolved view back to any registered codec; `GenerateTemplate` emits a stub configuration document populated from defaults — the "myapp config init" path
- Path expansion (POSIX shell-style: `~`, `$VAR`, `${VAR}`); first-match-wins multi-path lookup via `WithSearchPaths`; `WithOptional` for missing-file tolerance
- Built-in support for `time.Duration`, `time.Time`, `[]byte` (raw / base64 / hex), arrays and maps
- Multi-named-config orchestration: `Configs` holds named registries (per the rotini spec's `configuration_files[]`) and multiplexes their events through a single `<-chan NamedEvent`
- `io/fs.FS`-backed `NewFileSourceFS` for testing with `testing/fstest.MapFS` and for loading from `embed.FS` bundles
- Remote-backend adapters (etcd / consul / vault / awsssm / k8s) live in their own modules — opt-in by `go get`; the core ships `NewInMemoryBackend` as a reference and for tests
- Minimal third-party footprint: composes the go-rotini family

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
}

func main() {
	envSrc := recon.NewOSEnvSource()
	fileSrc, err := recon.YAMLSource("config.yaml", recon.WithOptional(true))
	if err != nil {
		log.Fatal(err)
	}

	r, err := recon.New(
		recon.WithSource(envSrc),
		recon.WithSource(fileSrc),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	var cfg Config
	if err := r.Bind(&cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%+v\n", cfg)
}
```

## Sources and precedence

Source chains are explicit and ordered. The first source to return ok=true for a key wins, with `Set` (programmatic override) always sitting above every source and `SetDefault` always below:

```go
envSrc := recon.NewOSEnvSource(recon.WithEnvPrefix("APP_"))
localSrc, _ := recon.DotenvSource(".env.local", recon.WithOptional(true))
fileSrc, _ := recon.YAMLSource("config.yaml")

r, err := recon.New(
	recon.WithSource(envSrc),    // wins by default
	recon.WithSource(localSrc),  // dev overrides
	recon.WithSource(fileSrc),   // baseline
)
```

Per-key overrides are first-class: `RegisterAlias` makes one path resolve to another (cycle-checked), and `PinSource(key, sourceName)` forces a key to resolve only from a named source.

## Live reload

`Live[T]` wraps the registry in an `atomic.Pointer[T]`-backed handle. Reads are O(1) and lock-free; readers always observe a complete, validated snapshot. Any source that implements `Watcher` participates; file-backed sources get watching from the registry's `WatcherFactory` (the bundled `FSWatcher` is backed by go-rotini/fs and is atomic-rename aware, debounced, multi-backend).

```go
type Config struct {
	Port   int    `recon:"server.port,default=8080"`
	LogLvl string `recon:"log.level,default=info"`
}

live, err := recon.NewLive[Config](r)
if err != nil {
	log.Fatal(err)
}
defer live.Close()

go func() {
	for ev := range live.Events() {
		if ev.Err != nil {
			log.Printf("reload failed (source %q): %v", ev.Source, ev.Err)
			continue
		}
		log.Printf("reload: changed=%v", ev.Changed)
	}
}()

for {
	cfg := live.Get() // *Config — never nil after NewLive succeeds
	serve(cfg)
}
```

The reload pipeline rebuilds the snapshot, computes the changed-key delta, optionally validates, and atomic-swaps the pointer; a failed candidate retains the previous value and emits an `Event` with `Err` set.

## Per-source resolution (`PerSourceFor`)

When a single key needs custom precedence — env-only in containers, config-first for daemons — `PerSourceFor[T]` returns each source's contribution separately so the caller picks a winner:

```go
ps, _ := recon.PerSourceFor[int](r, "server.port")

if inContainer() {
	if e := ps.BySource("env"); e.IsSet {
		return e.Value
	}
}
return ps.Resolved.Value // what Get[int] would have returned
```

Every entry carries its own `IsSet` + `Err`, so "source supplied an unparseable value" is distinguishable from "source had nothing".

## Error formatting

`FormatError(r, err)` renders a `*MultiError` (or any single typed error) into a multi-line summary with path, reason, source attribution, and — when the registry is non-nil — the full precedence chain for each failing key. Drop-in printable output for `log.Fatal`:

```go
if err := r.Bind(&cfg); err != nil {
	log.Fatal(recon.FormatError(r, err))
}
```

## Validation

Schema validation is opt-in. `WithSchema(bytes)` is the one-line form for raw JSON Schema; for YAML / TOML / JSONC schemas or pre-compiled `*jsonschema.Schema` values, build the validator explicitly and pass it via `WithValidator`:

```go
r, err := recon.New(
	recon.WithSource(fileSrc),
	recon.WithSchema(schemaBytes),
)
```

Validation failures during reload are reported on the `Registry.Events()` channel and via `Live.LastError()`; the previous snapshot is retained so live config keeps working. For per-struct validation, implement `Validator` (or `ValidatorContext`) on the bind target — the decoder calls it after every field is populated.

## Multi-named configs (`Configs`)

For applications with multiple independent configuration namespaces — each with its own precedence, schema, and watch policy — `Configs` holds named registries and multiplexes their reload events:

```go
cs := recon.NewConfigs()
defer cs.Close()
_ = cs.Register("database", dbRegistry)
_ = cs.Register("server", srvRegistry)

go func() {
	for ev := range cs.Events() {
		log.Printf("%s reloaded: changed=%v err=%v", ev.Name, ev.Changed, ev.Err)
	}
}()
```

Registries added via `Register` after `Events()` has been called are folded into the stream automatically; `Remove(name)` tears the per-name forwarder down cleanly.

## Provenance and introspection

`Describe()` returns the full per-key view — which source supplied each value, which other sources had a value, whether the key is secret:

```go
for _, k := range r.Describe().Keys {
	fmt.Printf("%s = %s (from %s; aliases: %v)\n",
		k.Path, k.Value, k.Source, k.Aliases)
}
```

`Describe` redacts secret-tagged values automatically. The data feeds straight into a `myapp config show` / `myapp config sources` subcommand without further plumbing.

## Encoding back out (`Save` / `SaveTo` / `GenerateTemplate`)

`Save` writes the current resolved view to an `io.Writer`; `SaveTo` writes to a file path and atomic-renames into place. Default policy is safe-to-pipe-anywhere — secret-marked keys are redacted, default-only keys are omitted; opt back in with `WithSaveIncludeSecrets` / `WithSaveIncludeDefaults`:

```go
// Dump current config to disk.
_ = r.SaveTo("snapshot.yaml")

// Dump just one sub-tree.
_ = r.SaveTo("server.yaml",
	recon.WithSaveOnly("server"),
	recon.WithSaveFormat(recon.FormatYAML),
)
```

`GenerateTemplate(format)` emits a stub document populated from the registered defaults — the "myapp config init" entry point. Secret keys are redacted unless `WithSaveIncludeSecrets` is passed:

```go
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
