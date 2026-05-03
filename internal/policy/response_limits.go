package policy

import (
	"fmt"
	"strings"

	"github.com/tiroq/chunkbridge/internal/config"
)

// CheckResponseSize returns an error if size exceeds the configured maximum.
func CheckResponseSize(size int64, cfg config.PolicyConfig) error {
	if cfg.MaxResponseBytes > 0 && size > cfg.MaxResponseBytes {
		return fmt.Errorf("policy: response size %d exceeds limit %d", size, cfg.MaxResponseBytes)
	}
	return nil
}

// CheckContentType returns an error if contentType is not in the allow list.
// An empty allow list means all content types are permitted.
func CheckContentType(contentType string, cfg config.PolicyConfig) error {
	if len(cfg.AllowedContentTypes) == 0 {
		return nil
	}
	// Strip parameters (e.g. "; charset=utf-8").
	ct := contentType
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	ct = strings.ToLower(ct)
	for _, allowed := range cfg.AllowedContentTypes {
		if strings.ToLower(allowed) == ct {
			return nil
		}
	}
	return fmt.Errorf("policy: content type %q not allowed", contentType)
}
