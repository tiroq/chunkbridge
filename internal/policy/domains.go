package policy

import (
	"net"
	"strings"
)

// IsAllowedDomain returns true if host is permitted by allowList.
// An empty allowList means all domains are allowed.
func IsAllowedDomain(host string, allowList []string) bool {
	if len(allowList) == 0 {
		return true
	}
	// Strip port if present.
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	h = strings.ToLower(h)

	for _, allowed := range allowList {
		a := strings.ToLower(allowed)
		if h == a {
			return true
		}
		// Subdomain wildcard: allow "*.example.com" to match "foo.example.com".
		if strings.HasPrefix(a, "*.") {
			suffix := a[1:] // ".example.com"
			if strings.HasSuffix(h, suffix) {
				return true
			}
		}
	}
	return false
}
