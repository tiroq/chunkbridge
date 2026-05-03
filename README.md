# chunkbridge

[![Go](https://img.shields.io/badge/go-1.25+-00ADD8?logo=go)](https://go.dev)

**chunkbridge** is a private, encrypted, chunk-based HTTP relay for agent-to-agent communication over message-oriented platforms (e.g. Max.ai).

```
 Agent A ──► HTTP Proxy ──► [transport] ──► Exit Node ──► Internet
              (client)        (encrypted)     (exit)
```

## Overview

chunkbridge has two operating modes:

| Mode | Role |
|------|------|
| `client` | Listens on `127.0.0.1` as an HTTP proxy; serialises requests into encrypted chunks and sends them via the configured transport. |
| `exit` | Receives encrypted request chunks, reassembles them, makes the outbound HTTP request, and returns the encrypted response. |

Communication is end-to-end encrypted with **XChaCha20-Poly1305**. Keys are derived from a shared passphrase using **Argon2id**. Large HTTP bodies are automatically split into protocol chunks that fit within the message platform's character limits.

## Quick Start

```bash
# Run the built-in selftest (no transport or config required)
go run ./cmd/chunkbridge selftest
```

## Configuration

Copy the example config files and edit them:

```bash
cp .example.chunkbridge.client.yaml chunkbridge.client.yaml
cp .example.chunkbridge.exit.yaml   chunkbridge.exit.yaml
```

Set a strong, shared `passphrase` and `salt` in both files.

## Running

```bash
# Client side
go run ./cmd/chunkbridge client chunkbridge.client.yaml

# Exit side
go run ./cmd/chunkbridge exit chunkbridge.exit.yaml
```

Configure your HTTP client to use `http://127.0.0.1:8080` as its HTTP proxy.

## Building

```bash
go build -o bin/chunkbridge ./cmd/chunkbridge
```

Or with [Task](https://taskfile.dev):

```bash
task build
task test
task selftest
```

## Security

* All frames are encrypted with XChaCha20-Poly1305 (256-bit key).
* Keys are derived via Argon2id (time=1, mem=64MiB, threads=4).
* Session IDs and sequence numbers are AAD for integrity.
* The exit node enforces domain allow-lists, port blocks, and private-IP blocking.
* The client proxy only listens on `127.0.0.1`.
* HTTPS CONNECT tunnelling is intentionally unsupported (returns 501).

See [docs/security.md](docs/security.md) for full details.

## Documentation

| Document | Contents |
|----------|----------|
| [docs/architecture.md](docs/architecture.md) | Component overview and data flow |
| [docs/protocol.md](docs/protocol.md) | Wire format and frame types |
| [docs/rate-limits.md](docs/rate-limits.md) | Rate-limiting strategy |
| [docs/max-transport.md](docs/max-transport.md) | Max.ai transport integration notes |
| [docs/security.md](docs/security.md) | Threat model and security properties |

## License

MIT