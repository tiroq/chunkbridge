package policy_test

import (
	"net"
	"testing"

	"github.com/tiroq/chunkbridge/internal/config"
	"github.com/tiroq/chunkbridge/internal/policy"
)

func newPolicy(cfg config.PolicyConfig) *policy.Policy {
	return policy.New(cfg)
}

func TestAllowedDomain(t *testing.T) {
	p := newPolicy(config.PolicyConfig{
		DomainAllowList: []string{"example.com"},
		AllowedSchemes:  []string{"http", "https"},
	})
	if err := p.CheckRequest("http://example.com/path"); err != nil {
		t.Errorf("expected allow: %v", err)
	}
}

func TestBlockedDomain(t *testing.T) {
	p := newPolicy(config.PolicyConfig{
		DomainAllowList: []string{"example.com"},
		AllowedSchemes:  []string{"http", "https"},
	})
	if err := p.CheckRequest("http://evil.com/path"); err == nil {
		t.Error("expected block for unlisted domain")
	}
}

func TestPrivateIPBlocked(t *testing.T) {
	p := newPolicy(config.PolicyConfig{
		BlockPrivateRanges: true,
		AllowedSchemes:     []string{"http", "https"},
	})
	cases := []string{
		"http://192.168.1.1/",
		"http://10.0.0.1/",
		"http://172.16.0.1/",
		"http://127.0.0.1/",
	}
	for _, tc := range cases {
		if err := p.CheckRequest(tc); err == nil {
			t.Errorf("expected private IP block for %s", tc)
		}
	}
}

func TestPortBlock(t *testing.T) {
	p := newPolicy(config.PolicyConfig{
		BlockedPorts:   []int{22, 25},
		AllowedSchemes: []string{"http", "https"},
	})
	if err := p.CheckRequest("http://example.com:22/"); err == nil {
		t.Error("expected port 22 to be blocked")
	}
	if err := p.CheckRequest("http://example.com:80/"); err != nil {
		t.Errorf("expected port 80 to be allowed: %v", err)
	}
}

func TestOversizedResponseBlocked(t *testing.T) {
	cfg := config.PolicyConfig{MaxResponseBytes: 100}
	if err := policy.CheckResponseSize(101, cfg); err == nil {
		t.Error("expected error for oversized response")
	}
	if err := policy.CheckResponseSize(100, cfg); err != nil {
		t.Errorf("expected nil for exactly max: %v", err)
	}
}

func TestIsAllowedDomainWildcard(t *testing.T) {
	if !policy.IsAllowedDomain("sub.example.com", []string{"*.example.com"}) {
		t.Error("expected wildcard to match subdomain")
	}
	if policy.IsAllowedDomain("evil.com", []string{"*.example.com"}) {
		t.Error("expected wildcard NOT to match unrelated domain")
	}
}

func TestEmptyAllowListPermitsAll(t *testing.T) {
	p := newPolicy(config.PolicyConfig{
		AllowedSchemes: []string{"http", "https"},
	})
	if err := p.CheckRequest("http://anything.example.com/"); err != nil {
		t.Errorf("expected allow with empty domain list: %v", err)
	}
}

func TestPrivateIP_CGNATBlocked(t *testing.T) {
	p := newPolicy(config.PolicyConfig{
		BlockPrivateRanges: true,
		AllowedSchemes:     []string{"http", "https"},
	})
	cases := []string{
		"http://100.64.0.1/",
		"http://100.64.255.255/",
		"http://100.127.255.255/",
	}
	for _, tc := range cases {
		if err := p.CheckRequest(tc); err == nil {
			t.Errorf("expected CGNAT address to be blocked: %s", tc)
		}
	}
}

func TestPrivateIP_IPv6LinkLocalBlocked(t *testing.T) {
	p := newPolicy(config.PolicyConfig{
		BlockPrivateRanges: true,
		AllowedSchemes:     []string{"http", "https"},
	})
	cases := []string{
		"http://[fe80::1]/",
		"http://[fe80::dead:beef]/",
	}
	for _, tc := range cases {
		if err := p.CheckRequest(tc); err == nil {
			t.Errorf("expected IPv6 link-local address to be blocked: %s", tc)
		}
	}
}

func TestIsPrivateIP_Unspecified(t *testing.T) {
	if !policy.IsPrivateIP(net.ParseIP("0.0.0.0")) {
		t.Error("expected 0.0.0.0 to be private")
	}
	if !policy.IsPrivateIP(net.ParseIP("::")) {
		t.Error("expected :: to be private")
	}
}

func TestIsPrivateIP_IPv4MappedIPv6(t *testing.T) {
	// ::ffff:192.168.1.1 is an IPv4-mapped IPv6 address — should be blocked.
	ip := net.ParseIP("::ffff:192.168.1.1")
	if ip == nil {
		t.Fatal("failed to parse ::ffff:192.168.1.1")
	}
	if !policy.IsPrivateIP(ip) {
		t.Error("expected IPv4-mapped IPv6 private address to be blocked")
	}
}
