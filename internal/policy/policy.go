package policy

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/tiroq/chunkbridge/internal/config"
	"github.com/tiroq/safedialer"
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
			if IsPrivateIP(ip) {
				return fmt.Errorf("policy: private IP %s not allowed", host)
			}
		}
		// If the host is a hostname (not a literal IP), we skip DNS resolution
		// here to avoid SSRF-via-DNS; exit node also validates post-resolve.
	}

	// Domain allow list.
	if !IsAllowedDomain(host, p.cfg.DomainAllowList) {
		return fmt.Errorf("policy: domain %q not in allow list", host)
	}

	return nil
}

// IsPrivateIP reports whether ip is in a private, loopback, link-local, CGNAT,
// cloud-metadata, or otherwise non-publicly-routable range.
//
// This is a thin wrapper around [safedialer.IsPrivateIP].
func IsPrivateIP(ip net.IP) bool {
	return safedialer.IsPrivateIP(ip)
}
