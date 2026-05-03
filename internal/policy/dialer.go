package policy

import (
	"context"
	"fmt"
	"net"
	"time"
)

// Resolver is the interface used to look up IP addresses for a hostname.
// The production implementation is net.DefaultResolver.
// Tests inject a fake to avoid real DNS lookups.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// SafeDialer wraps a standard net.Dialer with post-resolution IP validation.
// When blockPrivate is true, every IP resolved for the target hostname is
// checked with IsPrivateIP before any connection is attempted.
//
// Implementation note: the dialer resolves the hostname, validates all
// returned IPs, then dials the first allowed IP directly (as "ip:port").
// For HTTPS the TLS ServerName is set by http.Transport from the request
// Host header, so SNI is not affected by dialing an IP directly.
//
// Residual TOCTOU note: because Go's net.DefaultResolver resolves DNS a
// second time inside the kernel when the process actually connects, a very
// short-TTL DNS record could in theory change between our validation and the
// kernel's connect syscall. To eliminate this race entirely we dial the
// selected IP directly (not the hostname), which prevents the second OS
// lookup. This means we are protected for the lifetime of the TCP connection.
type SafeDialer struct {
	inner        *net.Dialer
	resolver     Resolver
	blockPrivate bool
}

// NewSafeDialer constructs a SafeDialer.
// blockPrivate mirrors Policy.BlockPrivateRanges.
func NewSafeDialer(resolver Resolver, blockPrivate bool) *SafeDialer {
	return &SafeDialer{
		inner: &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		},
		resolver:     resolver,
		blockPrivate: blockPrivate,
	}
}

// DialContext implements the DialContext signature expected by http.Transport.
func (d *SafeDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("policy: safeDialer: split host/port %q: %w", addr, err)
	}

	// If addr is already a literal IP we still validate it here, in addition
	// to CheckRequest validation, as defence-in-depth.
	if ip := net.ParseIP(host); ip != nil {
		if d.blockPrivate && IsPrivateIP(ip) {
			return nil, fmt.Errorf("policy: IP %s is not allowed (private/reserved range)", ip)
		}
		// Dial the literal IP directly.
		return d.inner.DialContext(ctx, network, addr)
	}

	// Resolve all IPs for the hostname.
	addrs, err := d.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("policy: safeDialer: lookup %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("policy: safeDialer: no addresses for %q", host)
	}

	if d.blockPrivate {
		for _, a := range addrs {
			if IsPrivateIP(a.IP) {
				return nil, fmt.Errorf("policy: hostname %q resolved to private/reserved IP %s", host, a.IP)
			}
		}
	}

	// Dial the first resolved IP directly to avoid a second OS DNS lookup
	// (eliminates the TOCTOU race described in the type comment).
	dialAddr := net.JoinHostPort(addrs[0].IP.String(), port)
	return d.inner.DialContext(ctx, network, dialAddr)
}
