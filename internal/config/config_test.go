package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validClient returns a DefaultClientConfig that passes all validation checks.
// Tests that need to trigger a specific error should start from this and
// override one field at a time.
func validClient() Config {
	return DefaultClientConfig()
}

// validExit returns a DefaultExitConfig that passes all validation checks.
func validExit() Config {
	return DefaultExitConfig()
}

// --- Happy-path tests ---

func TestValidateClientValidDefault(t *testing.T) {
	cfg := validClient()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config to pass, got: %v", err)
	}
}

func TestValidateExitValidDefault(t *testing.T) {
	cfg := validExit()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid exit config to pass, got: %v", err)
	}
}

// --- Mode ---

func TestValidateUnknownMode(t *testing.T) {
	cfg := validClient()
	cfg.Mode = "proxy"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for unknown mode, got nil")
	}
	if !strings.Contains(err.Error(), "config: mode") {
		t.Errorf("expected 'config: mode' prefix, got: %v", err)
	}
}

// --- Transport ---

func TestValidateUnknownTransport(t *testing.T) {
	cfg := validClient()
	cfg.Transport.Type = "smtp"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for unknown transport, got nil")
	}
	if !strings.Contains(err.Error(), "config: transport.type") {
		t.Errorf("expected 'config: transport.type' prefix, got: %v", err)
	}
}

func TestValidateMemoryTransportIsValidType(t *testing.T) {
	// "memory" is a known transport type and must pass validation.
	// The runtime error for using it in standalone mode is separate.
	cfg := validClient()
	cfg.Transport.Type = "memory"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected memory transport to pass validation, got: %v", err)
	}
}

func TestValidateMaxTransportIsValidType(t *testing.T) {
	cfg := validClient()
	cfg.Transport.Type = "max"
	cfg.Transport.Max.BaseURL = "https://api.max.example.com/v1"
	cfg.Transport.Max.TokenEnv = "MAX_API_TOKEN"
	cfg.Transport.Max.PeerChatID = "chat-123"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected max transport to pass validation, got: %v", err)
	}
}

// --- Crypto ---

func TestValidateMissingPassphraseEnv(t *testing.T) {
	cfg := validClient()
	cfg.Crypto.PassphraseEnv = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing passphrase_env, got nil")
	}
	if !strings.Contains(err.Error(), "crypto.passphrase_env") {
		t.Errorf("expected 'crypto.passphrase_env' in error, got: %v", err)
	}
}

func TestValidateMissingSalt(t *testing.T) {
	cfg := validClient()
	cfg.Crypto.Salt = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing salt, got nil")
	}
	if !strings.Contains(err.Error(), "crypto.salt") {
		t.Errorf("expected 'crypto.salt' in error, got: %v", err)
	}
}

func TestValidateWrongSaltSize(t *testing.T) {
	tests := []struct {
		name string
		salt string
	}{
		{"too short", "tooshort"},
		{"too long", "this_salt_is_way_too_long_for_chunkbridge"},
		{"15 bytes", "onlyfifteenbyte"},
		{"17 bytes", "seventeenbytesalt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validClient()
			cfg.Crypto.Salt = tt.salt
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected error for salt %q (len=%d), got nil", tt.salt, len(tt.salt))
			}
			if !strings.Contains(err.Error(), "crypto.salt") {
				t.Errorf("expected 'crypto.salt' in error, got: %v", err)
			}
		})
	}
}

func TestValidateZeroArgon2Time(t *testing.T) {
	cfg := validClient()
	cfg.Crypto.Argon2Time = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for zero argon2_time, got nil")
	}
	if !strings.Contains(err.Error(), "crypto.argon2_time") {
		t.Errorf("expected 'crypto.argon2_time' in error, got: %v", err)
	}
}

func TestValidateZeroArgon2Memory(t *testing.T) {
	cfg := validClient()
	cfg.Crypto.Argon2Mem = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for zero argon2_memory_kb, got nil")
	}
	if !strings.Contains(err.Error(), "crypto.argon2_memory_kb") {
		t.Errorf("expected 'crypto.argon2_memory_kb' in error, got: %v", err)
	}
}

func TestValidateZeroArgon2Threads(t *testing.T) {
	cfg := validClient()
	cfg.Crypto.Argon2Threads = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for zero argon2_threads, got nil")
	}
	if !strings.Contains(err.Error(), "crypto.argon2_threads") {
		t.Errorf("expected 'crypto.argon2_threads' in error, got: %v", err)
	}
}

// --- Listen (client mode only) ---

func TestValidateEmptyListenRejected(t *testing.T) {
	cfg := validClient()
	cfg.Listen.Address = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for empty listen address, got nil")
	}
	if !strings.Contains(err.Error(), "listen.address") {
		t.Errorf("expected 'listen.address' in error, got: %v", err)
	}
}

func TestValidateUnsafeWildcardListenRejected(t *testing.T) {
	wildcards := []string{"0.0.0.0", "::"}
	for _, addr := range wildcards {
		cfg := validClient()
		cfg.Listen.Address = addr
		err := cfg.Validate()
		if err == nil {
			t.Fatalf("expected error for wildcard address %q, got nil", addr)
		}
		if !strings.Contains(err.Error(), "listen.address") {
			t.Errorf("expected 'listen.address' in error for %q, got: %v", addr, err)
		}
	}
}

func TestValidateListenNotValidatedInExitMode(t *testing.T) {
	// Exit mode does not use the listen address; wildcard should be tolerated.
	cfg := validExit()
	cfg.Listen.Address = "0.0.0.0"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected exit mode to skip listen validation, got: %v", err)
	}
}

// --- Policy ---

func TestValidateInvalidPort(t *testing.T) {
	tests := []struct {
		name        string
		blockedPort int
	}{
		{"zero port", 0},
		{"negative port", -1},
		{"port too large", 65536},
		{"port max+1", 70000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validExit()
			cfg.Policy.BlockedPorts = []int{tt.blockedPort}
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected error for port %d, got nil", tt.blockedPort)
			}
			if !strings.Contains(err.Error(), "policy.blocked_ports") {
				t.Errorf("expected 'policy.blocked_ports' in error, got: %v", err)
			}
		})
	}
}

func TestValidateInvalidAllowedPort(t *testing.T) {
	cfg := validClient()
	cfg.Policy.AllowedPorts = []int{0}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for port 0 in allowed_ports, got nil")
	}
	if !strings.Contains(err.Error(), "policy.allowed_ports") {
		t.Errorf("expected 'policy.allowed_ports' in error, got: %v", err)
	}
}

func TestValidateUnsupportedScheme(t *testing.T) {
	cfg := validClient()
	cfg.Policy.AllowedSchemes = []string{"http", "ftp"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for ftp scheme, got nil")
	}
	if !strings.Contains(err.Error(), "policy.allowed_schemes") {
		t.Errorf("expected 'policy.allowed_schemes' in error, got: %v", err)
	}
}

// --- Message limits ---

func TestValidateMessageLimits(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Config)
		wantInErr string
	}{
		{
			name:      "safe_chars exceeds max_chars",
			mutate:    func(c *Config) { c.Limits.Message.SafeChars = c.Limits.Message.MaxChars + 1 },
			wantInErr: "message.safe_chars",
		},
		{
			name:      "max_b64_chars exceeds safe_chars",
			mutate:    func(c *Config) { c.Limits.Message.MaxB64Chars = c.Limits.Message.SafeChars + 1 },
			wantInErr: "message.max_b64_chars",
		},
		{
			name:      "max_chars is zero",
			mutate:    func(c *Config) { c.Limits.Message.MaxChars = 0 },
			wantInErr: "message.max_chars",
		},
		{
			name:      "safe_chars is zero",
			mutate:    func(c *Config) { c.Limits.Message.SafeChars = 0 },
			wantInErr: "message.safe_chars",
		},
		{
			name:      "max_b64_chars is zero",
			mutate:    func(c *Config) { c.Limits.Message.MaxB64Chars = 0 },
			wantInErr: "message.max_b64_chars",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validClient()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("expected %q in error, got: %v", tt.wantInErr, err)
			}
		})
	}
}

// --- LoadFile ---

func TestLoadFileValidYAML(t *testing.T) {
	content := `mode: client
listen:
  address: "127.0.0.1"
  port: 8080
transport:
  type: max
crypto:
  passphrase_env: "MY_KEY"
  salt: "saltchangeme1234"
  argon2_time: 1
  argon2_memory_kb: 65536
  argon2_threads: 4
`
	path := writeTempYAML(t, content)
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Mode != "client" {
		t.Errorf("expected mode=client, got %q", cfg.Mode)
	}
	if cfg.Crypto.Salt != "saltchangeme1234" {
		t.Errorf("unexpected salt: %q", cfg.Crypto.Salt)
	}
}

func TestLoadFileMalformedYAML(t *testing.T) {
	path := writeTempYAML(t, "{{{not valid yaml")
	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
	if !strings.Contains(err.Error(), "config:") {
		t.Errorf("expected 'config:' prefix, got: %v", err)
	}
}

func TestLoadFileMissingFile(t *testing.T) {
	_, err := LoadFile("/tmp/chunkbridge_nonexistent_test_config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "config:") {
		t.Errorf("expected 'config:' prefix, got: %v", err)
	}
}

// --- helpers ---

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writeTempYAML: %v", err)
	}
	return path
}

// --- Proxy config validation tests ---

func TestValidateProxyZeroMaxConcurrentRequests(t *testing.T) {
	cfg := validClient()
	cfg.Proxy.MaxConcurrentRequests = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for MaxConcurrentRequests=0, got nil")
	}
}

func TestValidateProxyNegativeMaxConcurrentRequests(t *testing.T) {
	cfg := validClient()
	cfg.Proxy.MaxConcurrentRequests = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for MaxConcurrentRequests=-1, got nil")
	}
}

func TestValidateProxyZeroRequestTimeoutMs(t *testing.T) {
	cfg := validClient()
	cfg.Proxy.RequestTimeoutMs = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for RequestTimeoutMs=0, got nil")
	}
}

func TestValidateProxyNotValidatedInExitMode(t *testing.T) {
	// proxy fields are only validated in client mode.
	cfg := validExit()
	cfg.Proxy.MaxConcurrentRequests = 0
	cfg.Proxy.RequestTimeoutMs = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("proxy fields should not be validated in exit mode, got: %v", err)
	}
}
