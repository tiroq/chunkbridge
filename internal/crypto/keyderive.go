package crypto

import (
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/argon2"
)

const (
	argon2KeyLen  = 32
	argon2SaltLen = 16
)

// DeriveParams holds the Argon2id cost parameters used during key derivation.
type DeriveParams struct {
	Time    uint32
	Memory  uint32
	Threads uint8
}

// DefaultDeriveParams are the default Argon2id cost parameters.
var DefaultDeriveParams = DeriveParams{Time: 1, Memory: 64 * 1024, Threads: 4}

// DeriveKey derives a 32-byte key from passphrase and salt using Argon2id.
// salt must be exactly 16 bytes. params controls the Argon2id cost; pass
// DefaultDeriveParams when no custom tuning is required.
func DeriveKey(passphrase, salt []byte, params DeriveParams) ([]byte, error) {
	if len(salt) != argon2SaltLen {
		return nil, fmt.Errorf("keyderive: salt must be exactly %d bytes, got %d", argon2SaltLen, len(salt))
	}
	// Apply defaults to a local copy so the caller's struct is not mutated.
	p := params
	if p.Time == 0 {
		p.Time = DefaultDeriveParams.Time
	}
	if p.Memory == 0 {
		p.Memory = DefaultDeriveParams.Memory
	}
	if p.Threads == 0 {
		p.Threads = DefaultDeriveParams.Threads
	}
	key := argon2.IDKey(passphrase, salt, p.Time, p.Memory, p.Threads, argon2KeyLen)
	return key, nil
}

// GenerateSalt returns a cryptographically random 16-byte salt.
func GenerateSalt() ([]byte, error) {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("keyderive: generate salt: %w", err)
	}
	return salt, nil
}
