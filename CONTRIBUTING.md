# Contributing

Contributions are welcome! Here's how to get started.

## Setup

```bash
git clone https://github.com/go-rotini/recon.git
cd recon
go mod download
make all   # run all project processes
```

## Making Changes

1. Fork the repository and create a branch from `main`.
2. Write tests for any new functionality.
3. Ensure `make all` passes before submitting a pull request.
4. Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages (e.g., `feat:`, `fix:`, `test:`, `docs:`).

## Linting

```bash
make lint
```

## Testing

```bash
make test              # run tests
make test-acceptance   # run registry acceptance tests against real-world config shapes
make test-bench        # run benchmarks
make test-fuzz         # run fuzz tests (60s per fuzzer)
make test-mutation     # run mutation tests
make test-race         # run tests with race detector
```

## Pull Requests

- Keep PRs focused on a single change.
- Include tests that cover the change.
- Reference any relevant issues.

## Reporting Bugs

Open an issue with the registry configuration that triggered it (sources, options, any custom `Codec` / `SchemaValidator` / `WatcherFactory`), a minimal reproducing struct or `Get` call, and the expected vs. actual behavior. Redact secret values — replace with `<redacted>` and note the type.

## Security

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.
