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

## DNS Rebinding / Post-Resolution IP Validation

### Protection implemented

The exit executor no longer relies solely on URL-string hostname validation. A custom `SafeDialer` (in `internal/policy/dialer.go`) is installed as the `DialContext` hook of the exit executor's `http.Transport`.

When `BlockPrivateRanges = true`, the `SafeDialer`:

1. Resolves all IP addresses for the target hostname using `net.DefaultResolver.LookupIPAddr`.
2. Validates every resolved IP against `policy.IsPrivateIP` before any connection is attempted.
3. If any resolved IP is private, loopback, link-local, CGNAT, or metadata-range, the request fails with a policy error — no TCP connection is opened.
4. Dials the first allowed resolved IP directly (as `ip:port`), which prevents the OS from performing a second DNS lookup at connect time. This eliminates the TOCTOU race that would otherwise exist between validation and connection.

The resolver is injected via an interface (`policy.Resolver`), so tests use a fake resolver without real DNS.

### Extended private ranges

The private IP blocklist now covers:

| Range | Description |
|-------|-------------|
| `10.0.0.0/8` | RFC 1918 |
| `172.16.0.0/12` | RFC 1918 |
| `192.168.0.0/16` | RFC 1918 |
| `127.0.0.0/8` | IPv4 loopback |
| `169.254.0.0/16` | IPv4 link-local / cloud metadata (169.254.169.254) |
| `100.64.0.0/10` | CGNAT, RFC 6598 *(new)* |
| `0.0.0.0/8` | Unspecified *(new)* |
| `::1/128` | IPv6 loopback |
| `fc00::/7` | IPv6 unique-local |
| `fe80::/10` | IPv6 link-local *(new)* |
| `::/128` | IPv6 unspecified *(new)* |

IPv4-mapped IPv6 addresses (e.g. `::ffff:192.168.1.1`) are unwrapped to their IPv4 form before the check.

### Residual limitations

- The `SafeDialer` dials the first resolved IP. If the authoritative DNS returns multiple IPs and the first is public but subsequent IPs are private, the first-public selection will succeed. In practice, public forward DNS records do not include private IPs alongside public ones; this is a cosmetic limitation.
- If an operator sets `BlockPrivateRanges = false`, no post-resolution check is performed and DNS rebinding is possible. **Operators should always set `BlockPrivateRanges = true` in exit mode.**
- Operators should always configure a `domain_allow_list` to restrict which hostnames the exit node will contact.

> **Summary:** With `BlockPrivateRanges = true` (the default in exit mode), DNS rebinding to private/loopback/CGNAT/link-local/metadata addresses is now blocked at the dial layer, not only at URL-string parse time.

## Hop-by-Hop Header Stripping

The exit executor strips all standard hop-by-hop headers before forwarding requests to upstream servers, per RFC 7230 §6.1. Removed headers:

`Connection`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `Proxy-Connection`, `TE`, `Trailer`, `Trailers`, `Transfer-Encoding`, `Upgrade`

Additionally, any header names listed in the `Connection` header value are also removed before forwarding. End-to-end headers such as `Authorization`, `Cookie`, `User-Agent`, and `Accept` are preserved.
