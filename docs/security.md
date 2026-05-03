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
