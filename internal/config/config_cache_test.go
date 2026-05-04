package config

import "testing"

// TestValidateCacheValidDefault verifies that the default config (cache disabled)
// passes validation without errors.
func TestValidateCacheValidDefault(t *testing.T) {
	cfg := validClient()
	// Default has Cache.Enabled = false, which should always pass.
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config (cache disabled) must be valid, got: %v", err)
	}
}

// TestValidateCacheValidEnabled verifies that a fully-populated enabled cache
// config passes validation.
func TestValidateCacheValidEnabled(t *testing.T) {
	cfg := validClient()
	cfg.Cache = CacheConfig{
		Enabled:           true,
		MaxEntries:        512,
		MaxBytes:          64 * 1024 * 1024,
		MaxEntryBytes:     2 * 1024 * 1024,
		DefaultTTLSeconds: 300,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid enabled cache config must pass, got: %v", err)
	}
}

// TestValidateCacheInvalidMaxEntries verifies that MaxEntries=0 with enabled
// cache is rejected.
func TestValidateCacheInvalidMaxEntries(t *testing.T) {
	cfg := validClient()
	cfg.Cache = CacheConfig{
		Enabled:           true,
		MaxEntries:        0, // invalid
		MaxBytes:          1024,
		MaxEntryBytes:     512,
		DefaultTTLSeconds: 60,
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for MaxEntries=0, got nil")
	}
}

// TestValidateCacheInvalidMaxBytes verifies that MaxBytes=0 with enabled
// cache is rejected.
func TestValidateCacheInvalidMaxBytes(t *testing.T) {
	cfg := validClient()
	cfg.Cache = CacheConfig{
		Enabled:           true,
		MaxEntries:        10,
		MaxBytes:          0, // invalid
		MaxEntryBytes:     512,
		DefaultTTLSeconds: 60,
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for MaxBytes=0, got nil")
	}
}

// TestValidateCacheMaxEntryBytesExceedsMaxBytes verifies that
// MaxEntryBytes > MaxBytes is rejected.
func TestValidateCacheMaxEntryBytesExceedsMaxBytes(t *testing.T) {
	cfg := validClient()
	cfg.Cache = CacheConfig{
		Enabled:           true,
		MaxEntries:        10,
		MaxBytes:          1024,
		MaxEntryBytes:     2048, // > MaxBytes
		DefaultTTLSeconds: 60,
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for MaxEntryBytes > MaxBytes, got nil")
	}
}

// TestValidateCacheNegativeDefaultTTL verifies that DefaultTTLSeconds=-1 is
// rejected.
func TestValidateCacheNegativeDefaultTTL(t *testing.T) {
	cfg := validClient()
	cfg.Cache = CacheConfig{
		Enabled:           true,
		MaxEntries:        10,
		MaxBytes:          1024,
		MaxEntryBytes:     512,
		DefaultTTLSeconds: -1, // invalid
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for DefaultTTLSeconds=-1, got nil")
	}
}
