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

// DeriveKey derives a 32-byte key from passphrase and salt using Argon2id.
func DeriveKey(passphrase, salt []byte) ([]byte, error) {
	if len(salt) == 0 {
		return nil, fmt.Errorf("keyderive: salt must not be empty")
	}
	key := argon2.IDKey(passphrase, salt, 1, 64*1024, 4, argon2KeyLen)
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
