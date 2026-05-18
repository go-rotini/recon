// Package recon is the canonical entry point for every piece of data that
// comes into a Go program at runtime — environment variables, .env files,
// configuration files (YAML / TOML / JSONC / JSON), command-line flags,
// standard input, in-memory buffers, programmatic defaults and overrides,
// and remote configuration stores (etcd / consul / vault / awsssm / k8s
// via separate adapter modules).
//
// The name is a deliberate triple-entendre — every reading is accurate:
//
//   - reconnaissance: surveys the runtime environment and gathers what's
//     there. Done at initial load and on every reload.
//   - reconciliation: reconciles values across sources via the documented
//     precedence chain (CLI → Env → Config → Default) and per-key aliases.
//     Done on every Get / Bind.
//   - reconfiguration: mutates the live registry — Set, SetDefault,
//     AddSource, InsertSource, Reload, hot-watch — without re-instantiating.
//     Done throughout the process lifetime, especially in REPL / daemon /
//     server modes.
//
// recon is the runtime data-in service for the rotini CLI framework
// (github.com/go-rotini/rotini), and works standalone for any Go program.
//
// # Design highlights
//
//   - No global state. Every operation is on an explicit [*Registry].
//   - Generics-first: [Get], [Bind], [Live], [PerSource].
//   - Live by default. Watched sources stream change events on a single
//     channel; [Live] gives a typed atomic-snapshot view of a config struct.
//   - Strong defaults, open seams. Every dependency ([Codec],
//     [SchemaValidator], [WatcherFactory], [FlagAdapter], [RemoteBackend],
//     [Source]) is an interface; the go-rotini family wires up automatically;
//     a one-line option replaces any default.
//   - Minimal third-party footprint. The core depends only on the go-rotini
//     family (transitively, only fsnotify via go-rotini/fs).
//
// # Quick start
//
// See the package README at https://github.com/go-rotini/recon for
// runnable examples covering every major API.
package recon
