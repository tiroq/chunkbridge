package policy

import "github.com/tiroq/chunkbridge/internal/config"

// IsAllowedPort reports whether port is permitted under cfg.
// If AllowedPorts is non-empty, only those ports are allowed.
// BlockedPorts are always denied.
func IsAllowedPort(port int, cfg config.PolicyConfig) bool {
	for _, blocked := range cfg.BlockedPorts {
		if port == blocked {
			return false
		}
	}
	if len(cfg.AllowedPorts) == 0 {
		return true
	}
	for _, allowed := range cfg.AllowedPorts {
		if port == allowed {
			return true
		}
	}
	return false
}
