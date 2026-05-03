package policy
package policy_test

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/tiroq/chunkbridge/internal/policy"
)

// fakeResolver implements policy.Resolver for tests without real DNS.
type fakeResolver struct {
	addrs []net.IPAddr
	err   error
}

func (f *fakeResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.addrs, nil
}

func resolverFor(ips ...string) *fakeResolver {
	var addrs []net.IPAddr
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil {
			panic(fmt.Sprintf("resolverFor: invalid IP %q", s))
		}
		addrs = append(addrs, net.IPAddr{IP: ip})
	}
	return &fakeResolver{addrs: addrs}
}

func TestExitBlocksDNSRebindingLoopback(t *testing.T) {
	d := policy.NewSafeDialer(resolverFor("127.0.0.1"), true)
	_, err := d.DialContext(context.Background(), "tcp", "legit-looking-domain.example.com:80")
	if err == nil {
		t.Fatal("expected dial to fail for loopback-resolving host, got nil")
	}
}

func TestExitBlocksDNSRebindingMetadata(t *testing.T) {
	d := policy.NewSafeDialer(resolverFor("169.254.169.254"), true)
	_, err := d.DialContext(context.Background(), "tcp", "metadata.example.com:80")
	if err == nil {
		t.Fatal("expected dial to fail for metadata IP, got nil")
	}
}

func TestExitBlocksDNSRebindingCGNAT(t *testing.T) {
	d := policy.NewSafeDialer(resolverFor("100.64.0.1"), true)
	_, err := d.DialContext(context.Background(), "tcp", "some-host.example.com:80")
	if err == nil {
		t.Fatal("expected dial to fail for CGNAT IP, got nil")
	}
}

func TestExitBlocksDNSRebindingIPv6LinkLocal(t *testing.T) {
	d := policy.NewSafeDialer(resolverFor("fe80::1"), true)
	_, err := d.DialContext(context.Background(), "tcp", "v6host.example.com:80")
	if err == nil {
		t.Fatal("expected dial to fail for IPv6 link-local IP, got nil")
	}
}

func TestExitAllowsPublicResolvedIP(t *testing.T) {
	// 1.1.1.1 is Cloudflare's public DNS — clearly not private.
	// We use a fake resolver so no real network call is made; the dial itself
	// may fail (no listener), but the error must not be the policy rejection.
	d := policy.NewSafeDialer(resolverFor("1.1.1.1"), true)
	_, err := d.DialContext(context.Background(), "tcp", "public.example.com:80")
	if err != nil {
		// The dial will fail because 1.1.1.1:80 isn't listening in the test env,
		// but the error must not mention "private/reserved".
		if containsPrivateError(err) {
			t.Fatalf("expected public IP to pass policy check, got: %v", err)
		}
	}
}

func TestSafeDialerAllowsWhenBlockPrivateFalse(t *testing.T) {
	// When blockPrivate is false, private IPs must be diallable.
	// Dial will fail at network level, but not due to policy.
	d := policy.NewSafeDialer(resolverFor("127.0.0.1"), false)
	_, err := d.DialContext(context.Background(), "tcp", "internal.example.com:9999")
	if err != nil && containsPrivateError(err) {
		t.Fatalf("expected blockPrivate=false to skip IP validation, got: %v", err)
	}
}

func containsPrivateError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "private") || contains(msg, "reserved")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && (func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}
