package config_test

import (
	"testing"

	"github.com/tiroq/chunkbridge/internal/config"
)

func TestValidateMaxMissingBaseURL(t *testing.T) {
	cfg := defaultMaxConfig()
	cfg.Transport.Max.BaseURL = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing base_url, got nil")
	}
}

func TestValidateMaxMissingTokenEnv(t *testing.T) {
	cfg := defaultMaxConfig()
	cfg.Transport.Max.TokenEnv = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing token_env, got nil")
	}
}

func TestValidateMaxMissingPeerChatID(t *testing.T) {
	cfg := defaultMaxConfig()
	cfg.Transport.Max.PeerChatID = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing peer_chat_id, got nil")
	}
}

func TestValidateMaxValidConfig(t *testing.T) {
	cfg := defaultMaxConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid max config failed validation: %v", err)
	}
}

func TestValidateMemoryTransportDoesNotRequireMaxFields(t *testing.T) {
	cfg := config.DefaultClientConfig()
	cfg.Transport.Type = "memory"
	// Max fields are empty — must not fail for memory transport.
	if err := cfg.Validate(); err != nil {
		t.Fatalf("memory transport with empty max fields should be valid: %v", err)
	}
}

// defaultMaxConfig returns a minimal valid Config for transport.type = "max".
func defaultMaxConfig() config.Config {
	cfg := config.DefaultClientConfig()
	cfg.Transport.Type = "max"
	cfg.Transport.Max.BaseURL = "https://api.max.example.com/v1"
	cfg.Transport.Max.TokenEnv = "MAX_API_TOKEN"
	cfg.Transport.Max.PeerChatID = "chat-123"
	return cfg
}
