# Developer Guide

## Prerequisites

* Go (version from [go.mod](../go.mod)) — <https://go.dev/dl/>
* [Task](https://taskfile.dev) (`brew install go-task` / `go install github.com/go-task/task/v3/cmd/task@latest`)

No other external tools are required for local development or CI.

---

## Running Checks Locally

### Quick reference

| Command | What it does |
|---------|-------------|
| `task fmt` | Format all Go files in-place (`gofmt -w`). **Mutating.** |
| `task fmt-check` | Fail if any file is not `gofmt`-clean. **Non-mutating** (same check CI uses). |
| `task lint` | Run `go vet ./...`. |
| `task test` | Run all tests with a 120 s timeout. |
| `task test-unit` | Run `internal/...` tests only (60 s timeout). |
| `task test-integration` | Run `tests/integration/...` tests only, with verbose output. |
| `task test-race` | Run all tests under Go's race detector (120 s timeout). |
| `task build` | Compile `bin/chunkbridge` for the current platform. |
| `task selftest` | Run the built-in round-trip selftest (no real transport needed). |
| `task check` | Run **all** CI checks in sequence: fmt-check → lint → test → test-race → build → selftest. |
| `task build-all` | Cross-compile release binaries for all supported platforms. |
| `task tidy` | Run `go mod tidy`. |
| `task clean` | Delete `bin/`. |

### Reproducing CI locally

```bash
task check
```

This runs exactly the same steps as the GitHub Actions CI workflow, in order.

---

## selftest

`task selftest` (and the CI `selftest` step) runs `go run ./cmd/chunkbridge selftest`, which performs a round-trip test using an in-process `MemoryTransport`. No MAX API credentials, no network access, and no YAML config file are required.

A passphrase is required to derive the encryption key. `task selftest` defaults to `testpassphrase` if `CHUNKBRIDGE_SHARED_KEY` is not set in the environment. You can override it:

```bash
CHUNKBRIDGE_SHARED_KEY=mypassphrase task selftest
```

CI uses the `testpassphrase` default. **No live MAX credentials are required for CI.**

---

## Format Check vs. Format

| Task | Behaviour |
|------|-----------|
| `task fmt` | Runs `go fmt ./...` — rewrites files in-place. Use locally before committing. |
| `task fmt-check` | Runs `gofmt -l .` — prints unformatted files and exits non-zero if any are found. Does **not** modify files. Used in CI. |

CI uses `fmt-check` so it never mutates repository files during a workflow run.

---

## Release Builds

`task build-all` cross-compiles for all supported platforms and writes binaries into `bin/`:

| File | Target |
|------|--------|
| `bin/chunkbridge-linux-amd64` | Linux x86-64 |
| `bin/chunkbridge-linux-arm64` | Linux ARM64 |
| `bin/chunkbridge-darwin-arm64` | macOS Apple Silicon |
| `bin/chunkbridge-darwin-amd64` | macOS Intel |
| `bin/chunkbridge-windows-amd64.exe` | Windows x86-64 |

Pure Go — no CGo, no external toolchain required for cross-compilation.

```bash
task build-all
ls -lh bin/
```

---

## CI Workflow

CI runs on every push to `main` and every pull request via [`.github/workflows/ci.yml`](../.github/workflows/ci.yml).

Steps in order:

1. **Checkout** — `actions/checkout@v4`
2. **Set up Go** — `actions/setup-go@v5` (reads version from `go.mod`)
3. **Download modules** — `go mod download`
4. **Format check** — `gofmt -l .`; fails if any file is unformatted
5. **Vet** — `go vet ./...`
6. **Test** — `go test ./... -timeout 120s`
7. **Race test** — `go test -race ./... -timeout 120s`
8. **Build** — `go build -o bin/chunkbridge ./cmd/chunkbridge`
9. **Selftest** — `go run ./cmd/chunkbridge selftest` (with `CHUNKBRIDGE_SHARED_KEY=testpassphrase`)

No live MAX API credentials are used or required. The selftest uses an in-process memory transport.

---

## Project Layout

See [docs/architecture.md](architecture.md) for the full package map and data flow.
