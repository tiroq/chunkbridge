// Package policy_test re-exports the DNS-rebinding dialer tests that now live
// in the safedialer library. These tests verify that the safedialer integration
// used by chunkbridge continues to block private resolved IPs.
package policy_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/tiroq/safedialer"
)

// fakeDialerResolver implements safedialer.Resolver for tests without real DNS.
type fakeDialerResolver struct {
	addrs []net.IPAddr
	err   error
}

func (f *fakeDialerResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.addrs, nil
}

func dialerResolverFor(ips ...string) *fakeDialerResolver {
	var addrs []net.IPAddr
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil {
			panic(fmt.Sprintf("dialerResolverFor: invalid IP %q", s))
		}
		addrs = append(addrs, net.IPAddr{IP: ip})
	}
	return &fakeDialerResolver{addrs: addrs}
}

func containsPrivateError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "private") || strings.Contains(msg, "reserved")
}

func TestExitBlocksDNSRebindingLoopback(t *testing.T) {
	d := safedialer.NewDialer(safedialer.Policy{BlockPrivateRanges: true}, dialerResolverFor("127.0.0.1"))
	_, err := d.DialContext(context.Background(), "tcp", "legit-looking-domain.example.com:80")
	if err == nil {
		t.Fatal("expected dial to fail for loopback-resolving host, got nil")
	}
	if !containsPrivateError(err) {
		t.Fatalf("expected private-IP error, got: %v", err)
	}
}

func TestExitBlocksDNSRebindingMetadata(t *testing.T) {
	d := safedialer.NewDialer(safedialer.Policy{BlockPrivateRanges: true}, dialerResolverFor("169.254.169.254"))
	_, err := d.DialContext(context.Background(), "tcp", "metadata.example.com:80")
	if err == nil {
		t.Fatal("expected dial to fail for metadata IP, got nil")
	}
	if !containsPrivateError(err) {
		t.Fatalf("expected private-IP error, got: %v", err)
	}
}

func TestExitBlocksDNSRebindingCGNAT(t *testing.T) {
	d := safedialer.NewDialer(safedialer.Policy{BlockPrivateRanges: true}, dialerResolverFor("100.64.0.1"))
	_, err := d.DialContext(context.Background(), "tcp", "some-host.example.com:80")
	if err == nil {
		t.Fatal("expected dial to fail for CGNAT IP, got nil")
	}
}

func TestExitBlocksDNSRebindingIPv6LinkLocal(t *testing.T) {
	d := safedialer.NewDialer(safedialer.Policy{BlockPrivateRanges: true}, dialerResolverFor("fe80::1"))
	_, err := d.DialContext(context.Background(), "tcp", "v6host.example.com:80")
	if err == nil {
		t.Fatal("expected dial to fail for IPv6 link-local IP, got nil")
	}
}

func TestExitAllowsPublicResolvedIP(t *testing.T) {
	d := safedialer.NewDialer(safedialer.Policy{BlockPrivateRanges: true}, dialerResolverFor("1.1.1.1"))
	_, err := d.DialContext(context.Background(), "tcp", "public.example.com:80")
	if err != nil && containsPrivateError(err) {
		t.Fatalf("expected public IP to pass policy check, got: %v", err)
	}
}

func TestSafeDialerAllowsWhenBlockPrivateFalse(t *testing.T) {
	d := safedialer.NewDialer(safedialer.Policy{BlockPrivateRanges: false}, dialerResolverFor("127.0.0.1"))
	_, err := d.DialContext(context.Background(), "tcp", "internal.example.com:9999")
	if err != nil && containsPrivateError(err) {
		t.Fatalf("expected blockPrivate=false to skip IP validation, got: %v", err)
	}
}
