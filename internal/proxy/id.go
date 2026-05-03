package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to a timestamp-based ID to avoid collisions on rand failure.
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
