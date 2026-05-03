package crypto_test

import (
	"bytes"
	"testing"

	"github.com/tiroq/chunkbridge/internal/crypto"
)

var aesKey = []byte("12345678901234567890123456789012") // 32 bytes

func TestEncryptDecryptRoundtrip(t *testing.T) {
	plaintext := []byte("hello, world!")
	aad := []byte("session|42")

	ct, err := crypto.Encrypt(aesKey, plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	got, err := crypto.Decrypt(aesKey, ct, aad)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("decrypted %q, want %q", got, plaintext)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	ct, _ := crypto.Encrypt(aesKey, []byte("secret"), []byte("aad"))
	wrongKey := bytes.Repeat([]byte("Z"), 32)
	_, err := crypto.Decrypt(wrongKey, ct, []byte("aad"))
	if err == nil {
		t.Fatal("expected error with wrong key")
	}
}

func TestDecryptTampered(t *testing.T) {
	ct, _ := crypto.Encrypt(aesKey, []byte("secret"), []byte("aad"))
	ct[len(ct)-1] ^= 0xFF // flip last byte
	_, err := crypto.Decrypt(aesKey, ct, []byte("aad"))
	if err == nil {
		t.Fatal("expected error with tampered ciphertext")
	}
}

func TestDecryptChangedAAD(t *testing.T) {
	ct, _ := crypto.Encrypt(aesKey, []byte("secret"), []byte("aad-original"))
	_, err := crypto.Decrypt(aesKey, ct, []byte("aad-different"))
	if err == nil {
		t.Fatal("expected error with changed AAD")
	}
}
