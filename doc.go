// Package recon is the entry point for runtime data — environment
// variables, .env files, configuration files (YAML / TOML / JSONC /
// JSON), command-line flags, standard input, in-memory buffers,
// programmatic defaults and overrides, and remote configuration
// stores (etcd / consul / vault / awsssm / k8s via separate adapter
// modules).
//
// The name is a deliberate triple meaning, each accurate:
//
//   - reconnaissance: surveys the runtime environment on load and
//     every reload.
//   - reconciliation: reconciles values across sources via the
//     documented precedence chain and per-key aliases on every Get
//     and Bind.
//   - reconfiguration: mutates the live registry (Set, AddSource,
//     Reload, hot-watch) without re-instantiating.
//
// # Design
//
//   - No global state. Every operation is on an explicit [*Registry].
//   - Generics-first: [Get], [Bind], [Live], [PerSourceFor].
//   - Live on request. [Live] gives a typed atomic-snapshot view
//     that re-binds on every successful reload; file sources reload
//     once a [WatcherFactory] is installed with [WithWatcher] (see the
//     fswatch subpackage). [WithoutWatch] builds a one-shot registry.
//   - Open seams. [Codec], [SchemaValidator], [WatcherFactory],
//     [FlagAdapter], [RemoteBackend], and [Source] are interfaces;
//     one option replaces any default.
//   - Link what you use. [New] and [NewFileSource] resolve formats
//     against [DefaultCodecs]; [NewWithCodecs] and the *WithCodecs
//     source constructors take an explicit set instead, so the binary
//     links only those codecs.
//
// See the README at https://github.com/go-rotini/recon for runnable
// examples.
package recon
