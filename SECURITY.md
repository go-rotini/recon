# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly by emailing **matthewcgetz@gmail.com**. Do not open a public issue.

You should receive a response within 72 hours. If accepted, a fix will be developed privately and released as a patch version.

## Resource Limits

This package defaults to safe behavior to mitigate denial-of-service and accidental misuse:

- **`FileSource` path expansion** is one-pass and non-recursive — a default that itself contains `$VAR` is not re-expanded — so a hostile config file cannot induce a runaway expansion loop. `${VAR:?msg}` failures surface a documented error rather than a panic or `os.Exit`.
- **`fromFile` reads** read the named file's contents into the field. Only fields you explicitly tag `fromFile` are eligible; keep the path under your control (don't derive it from untrusted input, e.g. pointed at `/dev/zero`).
- **`expand`-tagged values** detect reference cycles and bounded-depth expansion chains, returning `ErrCycle` rather than overflowing the stack.
- **No subshell execution.** The package never invokes `$(command)` or any shell substitution form.
- **No `os.Setenv` from inside the library** (with the single exception of the `unset` tag option, which is opt-in per field). All "writes" operate on in-memory `Source` chains or `*atomic.Pointer[Snapshot]` slots so the package never collides with libc `setenv` / `getenv` thread-safety on cgo paths.
- **File watching** is delegated to [go-rotini/fs](https://github.com/go-rotini/fs)'s `Watcher`, which handles atomic-rename save patterns (write-temp-then-rename) by watching the parent directory and filtering by filename — the rewritten file appears as a new inode, which a naive single-fd watcher would miss.
- **Codec-specific limits** apply at the format-parser layer ([go-rotini/yaml](https://github.com/go-rotini/yaml), [go-rotini/toml](https://github.com/go-rotini/toml), [go-rotini/jsonc](https://github.com/go-rotini/jsonc), [go-rotini/dotenv](https://github.com/go-rotini/dotenv)); see those packages' `SECURITY.md` files for their bounded-read / max-depth / cycle-detection details.
- **Schema validation** is delegated to [go-rotini/jsonschema](https://github.com/go-rotini/jsonschema), which has its own DoS guards (max ref depth, max recursion depth, max document size, ref-loop detection).

## Secret Handling

Fields tagged `secret` are redacted in:

- `Describe` / `DescribeKey` / `KeyDescription.Value` output
- `Snapshot.String` output
- Error messages (`*ParseError`, `*CoercionError`, `*ImmutableChangedError` — old / new values replaced with the redactor's output)
- `Save` / `SaveTo` output (replaced with `***` unless `WithSaveIncludeSecrets()` is explicitly set)

The `Secret[T]` wrapper (a type alias of [`env.Secret[T]`](https://github.com/go-rotini/env)) threads redaction through `fmt.Stringer`, `fmt.GoStringer`, `slog.LogValuer`, and JSON marshaling so secret values do not leak through user-side logging. Override the redactor via `WithSecretRedactor(fn func(string) string)`.

## Errors are delivered, never imposed

No code path inside `recon` prints to stderr, calls `os.Exit`, or formats an error for human consumption on the caller's behalf. Errors return from constructors / `Load` / `Bind` / `Validate` / `Reload`; non-fatal events arrive on `Registry.Events()` (with `Err` for failures and `Warnings` for deprecations). The caller — a CLI's lifecycle, an HTTP server, a test harness — decides how to format, log, recover, or exit.
