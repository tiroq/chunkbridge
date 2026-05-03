package config

// DefaultClientConfig returns a sensible default configuration for client mode.
func DefaultClientConfig() Config {
	return Config{
		Mode: "client",
		Listen: ListenConfig{
			Address: "127.0.0.1",
			Port:    8080,
		},
		Transport: TransportConfig{
			Type: "memory",
		},
		Crypto: CryptoConfig{
			PassphraseEnv: "CHUNKBRIDGE_SHARED_KEY",
			Salt:          "saltchangeme1234", // 16 bytes — operators must replace this
			Argon2Time:    1,
			Argon2Mem:     64 * 1024,
			Argon2Threads: 4,
		},
		Limits: RateLimitsConfig{
			GlobalRPS:  5,
			DataRPS:    4,
			ControlRPS: 2,
			Burst:      10,
			Message: MessageConfig{
				MaxChars:    4000,
				SafeChars:   3600,
				MaxB64Chars: 3400,
			},
			Ack: AckConfig{
				IntervalMs: 500,
				TimeoutMs:  5000,
				MaxRetries: 5,
			},
			Window: WindowConfig{
				InitialSize: 4,
				MaxSize:     16,
				MinSize:     1,
			},
		},
		Policy: PolicyConfig{
			BlockPrivateRanges: false,
			AllowedSchemes:     []string{"http", "https"},
			MaxResponseBytes:   10 * 1024 * 1024,
		},
		Exit: ExitConfig{
			RequestTimeoutSec:  30,
			ResponseBufferSize: 1024 * 1024,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

// DefaultExitConfig returns a sensible default configuration for exit mode.
func DefaultExitConfig() Config {
	cfg := DefaultClientConfig()
	cfg.Mode = "exit"
	cfg.Policy.BlockPrivateRanges = true
	cfg.Policy.BlockedPorts = []int{22, 25, 465, 587, 6379, 5432, 3306, 27017}
	cfg.Policy.AllowedSchemes = []string{"http", "https"}
	return cfg
}
