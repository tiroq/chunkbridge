# Security

## Threat Model

chunkbridge is designed to relay HTTP traffic through a message-oriented platform that would otherwise be unsuitable for general HTTP proxying. The primary threats are:

1. **Message interception** – an adversary reads messages on the shared platform.
2. **Message tampering** – an adversary modifies messages in transit.
3. **SSRF via exit node** – the client tricks the exit node into reaching internal services.
4. **Traffic analysis** – an adversary infers what is being proxied from metadata.
5. **Replay attacks** – an adversary re-sends old messages.

## Encryption

* **Cipher**: XChaCha20-Poly1305 (256-bit key, 192-bit nonce, 128-bit authentication tag).
* **Key derivation**: Argon2id (time=1, memory=64 MiB, threads=4, keyLen=32). A unique random salt must be generated per deployment.
* **AAD**: `sessionID + "|" + seqNum` — binds each ciphertext to its position in the session, making replay or reordering detectable.
* **Nonce**: 24 random bytes prepended to each ciphertext; never reused because it is randomly generated.

## SSRF Prevention (Exit Node)

The `policy.Policy` enforced by the exit node:

| Check | Default |
|-------|---------|
| Scheme allowlist | `http`, `https` only |
| Private IP block | enabled in `exit` mode |
| Port blocklist | 22, 25, 465, 587, 6379, 5432, 3306, 27017 |
| Domain allowlist | empty = allow all (operators should restrict) |
| Max response size | 10 MiB |

> **Operators should always configure a `domain_allow_list`** for production exit nodes.

## Proxy Listener Binding

The client proxy binds only to `127.0.0.1`. It refuses `CONNECT` requests (HTTPS tunnelling) with HTTP 501.

## Sensitive Data Logging Policy

The logger (`internal/observability/logger.go`) never logs:
* Encryption keys or passphrases
* Request/response bodies
* Auth tokens

## Known Limitations

* **Replay protection** relies on AAD binding but does not maintain a seen-sequence-number set. Full replay protection would require stateful tracking.
* **Traffic analysis**: message length and timing are visible to the platform. Padding is not currently implemented.
* **Forward secrecy**: key derivation is static per deployment. Rotate the passphrase/salt to achieve key rotation.

## DNS Rebinding / Post-Resolution SSRF Gap (Known P1)

> **Warning:** `BlockPrivateRanges` is **not complete SSRF protection** in the current implementation. Do not rely on it as a sole defence in production.

### How the gap works

`policy.CheckRequest` validates the hostname against private IP ranges and the domain allow-list **before** any DNS lookup. The exit node's `http.Client.Do` call then resolves the hostname to an IP address via the system resolver — that resolved IP is **never re-validated** against the private IP blocklist.

An attacker who controls DNS (or who can insert a short-TTL record) can point a permitted hostname to `192.168.0.1`, `169.254.169.254` (cloud metadata), or any other internal address, bypassing all SSRF defences.

### Missing block ranges

The current private-range blocklist also does not include:

| Range | Description |
|-------|-------------|
| `fe80::/10` | IPv6 link-local |
| `100.64.0.0/10` | CGNAT / RFC 6598 |

Both ranges can reach internal infrastructure on many deployments.

### Planned fix

Replace the exit executor's `http.Client` with one that uses a custom `net.Dialer` / `DialContext` hook. The hook resolves each target address and calls `policy.isPrivateIP` on every resolved IP before allowing the connection. This ensures the block applies to the actual address the OS will connect to, not the pre-resolution hostname.

This fix is tracked as **GAP-02** in the implementation audit and is planned for the next security-focused PR.
