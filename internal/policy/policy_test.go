package policy_test

import (
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
