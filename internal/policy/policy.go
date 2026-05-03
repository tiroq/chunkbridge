package policy

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/tiroq/chunkbridge/internal/config"
)

// Policy combines all policy checks for outbound HTTP requests.
type Policy struct {
	cfg config.PolicyConfig
}

// New creates a Policy from the given configuration.
func New(cfg config.PolicyConfig) *Policy {
	return &Policy{cfg: cfg}
}

// CheckRequest validates a target URL against all configured policies.
// It returns a non-nil error if the request should be denied.
func (p *Policy) CheckRequest(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("policy: invalid url: %w", err)
	}

	// Scheme check.
	scheme := u.Scheme
	if len(p.cfg.AllowedSchemes) > 0 {
		allowed := false
		for _, s := range p.cfg.AllowedSchemes {
			if s == scheme {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("policy: scheme %q not allowed", scheme)
		}
	}

	host := u.Hostname()
	portStr := u.Port()

	// Default ports.
	port := 80
	if scheme == "https" {
		port = 443
	}
	if portStr != "" {
		port, err = strconv.Atoi(portStr)
		if err != nil {
			return fmt.Errorf("policy: invalid port %q", portStr)
		}
	}

	// Port check.
	if !IsAllowedPort(port, p.cfg) {
		return fmt.Errorf("policy: port %d not allowed", port)
	}

	// Private IP check.
	if p.cfg.BlockPrivateRanges {
		ip := net.ParseIP(host)
		if ip != nil {
			if isPrivateIP(ip) {
				return fmt.Errorf("policy: private IP %s not allowed", host)
			}
		}
		// If the host is a hostname (not a literal IP), we skip DNS resolution
		// here to avoid SSRF-via-DNS; exit node should also validate post-resolve.
	}

	// Domain allow list.
	if !IsAllowedDomain(host, p.cfg.DomainAllowList) {
		return fmt.Errorf("policy: domain %q not in allow list", host)
	}

	return nil
}

// isPrivateIP reports whether ip is in a private, loopback, link-local,
// CGNAT, or metadata range.
func isPrivateIP(ip net.IP) bool {
	return IsPrivateIP(ip)
}

// IsPrivateIP reports whether ip should be treated as a non-publicly-routable
// address. This covers:
//   - RFC 1918 private ranges (10/8, 172.16/12, 192.168/16)
//   - Loopback (127/8, ::1/128)
//   - Link-local (169.254/16, fe80::/10)
//   - CGNAT / RFC 6598 (100.64.0.0/10)
//   - Unique-local IPv6 (fc00::/7)
//   - Unspecified (0.0.0.0/8, ::/128)
//   - IPv4-mapped IPv6 addresses are unwrapped and checked against IPv4 ranges
func IsPrivateIP(ip net.IP) bool {
	// Unwrap IPv4-in-IPv6 representation (e.g. ::ffff:192.168.1.1).
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}

	private := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16", // IPv4 link-local / cloud metadata
		"100.64.0.0/10",  // CGNAT, RFC 6598
		"0.0.0.0/8",      // unspecified
		"::1/128",        // IPv6 loopback
		"fc00::/7",       // IPv6 unique-local
		"fe80::/10",      // IPv6 link-local
		"::/128",         // IPv6 unspecified
	}
	for _, cidr := range private {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
