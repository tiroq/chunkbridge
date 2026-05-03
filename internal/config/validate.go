package config

import "fmt"

// Validate checks c for configuration errors.
// All errors are prefixed with "config:".
// This function does not read environment variables.
// Call it after LoadFile and before key derivation.
func (c *Config) Validate() error {
	if err := c.validateMode(); err != nil {
		return err
	}
	if err := c.validateTransport(); err != nil {
		return err
	}
	if err := c.validateCrypto(); err != nil {
		return err
	}
	if c.Mode == "client" {
		if err := c.validateListen(); err != nil {
			return err
		}
	}
	if err := c.validatePolicy(); err != nil {
		return err
	}
	// Optional sections: only validate internal consistency when fields are set.
	if c.Limits.Message.MaxChars > 0 || c.Limits.Message.SafeChars > 0 || c.Limits.Message.MaxB64Chars > 0 {
		if err := c.validateMessageLimits(); err != nil {
			return err
		}
	}
	if c.Limits.GlobalRPS > 0 || c.Limits.DataRPS > 0 || c.Limits.ControlRPS > 0 || c.Limits.Burst > 0 {
		if err := c.validateRateLimits(); err != nil {
			return err
		}
	}
	if c.Limits.Window.InitialSize > 0 || c.Limits.Window.MaxSize > 0 || c.Limits.Window.MinSize > 0 {
		if err := c.validateWindowConfig(); err != nil {
			return err
		}
	}
	if c.Limits.Ack.IntervalMs > 0 || c.Limits.Ack.TimeoutMs > 0 {
		if err := c.validateAckConfig(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) validateMode() error {
	switch c.Mode {
	case "client", "exit":
		return nil
	default:
		return fmt.Errorf("config: mode must be one of client,exit")
	}
}

func (c *Config) validateTransport() error {
	switch c.Transport.Type {
	case "memory", "max":
		return nil
	default:
		return fmt.Errorf("config: transport.type must be one of memory,max")
	}
}

func (c *Config) validateCrypto() error {
	if c.Crypto.PassphraseEnv == "" {
		return fmt.Errorf("config: crypto.passphrase_env must be set")
	}
	if len(c.Crypto.Salt) != 16 {
		return fmt.Errorf("config: crypto.salt must be exactly 16 bytes (got %d)", len(c.Crypto.Salt))
	}
	if c.Crypto.Argon2Time == 0 {
		return fmt.Errorf("config: crypto.argon2_time must be greater than zero")
	}
	if c.Crypto.Argon2Mem == 0 {
		return fmt.Errorf("config: crypto.argon2_memory_kb must be greater than zero")
	}
	if c.Crypto.Argon2Threads == 0 {
		return fmt.Errorf("config: crypto.argon2_threads must be greater than zero")
	}
	return nil
}

func (c *Config) validateListen() error {
	switch c.Listen.Address {
	case "":
		return fmt.Errorf("config: listen.address must not be empty")
	case "0.0.0.0", "::":
		return fmt.Errorf("config: listen.address binds to a wildcard address; use 127.0.0.1 or ::1")
	}
	return nil
}

func (c *Config) validatePolicy() error {
	for _, port := range c.Policy.BlockedPorts {
		if port < 1 || port > 65535 {
			return fmt.Errorf("config: policy.blocked_ports contains invalid port %d (must be 1-65535)", port)
		}
	}
	for _, port := range c.Policy.AllowedPorts {
		if port < 1 || port > 65535 {
			return fmt.Errorf("config: policy.allowed_ports contains invalid port %d (must be 1-65535)", port)
		}
	}
	for _, scheme := range c.Policy.AllowedSchemes {
		if scheme != "http" && scheme != "https" {
			return fmt.Errorf("config: policy.allowed_schemes contains unsupported scheme %q; only http and https are allowed", scheme)
		}
	}
	return nil
}

func (c *Config) validateMessageLimits() error {
	m := c.Limits.Message
	if m.MaxChars <= 0 {
		return fmt.Errorf("config: message.max_chars must be greater than zero")
	}
	if m.SafeChars <= 0 {
		return fmt.Errorf("config: message.safe_chars must be greater than zero")
	}
	if m.MaxB64Chars <= 0 {
		return fmt.Errorf("config: message.max_b64_chars must be greater than zero")
	}
	if m.SafeChars > m.MaxChars {
		return fmt.Errorf("config: message.safe_chars must be <= message.max_chars")
	}
	if m.MaxB64Chars > m.SafeChars {
		return fmt.Errorf("config: message.max_b64_chars must be <= message.safe_chars")
	}
	return nil
}

func (c *Config) validateRateLimits() error {
	rl := c.Limits
	if rl.GlobalRPS <= 0 {
		return fmt.Errorf("config: rate_limits.global_rps must be greater than zero")
	}
	if rl.DataRPS <= 0 {
		return fmt.Errorf("config: rate_limits.data_rps must be greater than zero")
	}
	if rl.ControlRPS <= 0 {
		return fmt.Errorf("config: rate_limits.control_rps must be greater than zero")
	}
	if rl.Burst <= 0 {
		return fmt.Errorf("config: rate_limits.burst must be greater than zero")
	}
	return nil
}

func (c *Config) validateWindowConfig() error {
	w := c.Limits.Window
	if w.MinSize <= 0 {
		return fmt.Errorf("config: window.min_size must be greater than zero")
	}
	if w.InitialSize < w.MinSize {
		return fmt.Errorf("config: window.initial_size must be >= window.min_size")
	}
	if w.MaxSize < w.InitialSize {
		return fmt.Errorf("config: window.max_size must be >= window.initial_size")
	}
	return nil
}

func (c *Config) validateAckConfig() error {
	a := c.Limits.Ack
	if a.IntervalMs <= 0 {
		return fmt.Errorf("config: ack.interval_ms must be greater than zero")
	}
	if a.TimeoutMs <= 0 {
		return fmt.Errorf("config: ack.timeout_ms must be greater than zero")
	}
	if a.MaxRetries < 0 {
		return fmt.Errorf("config: ack.max_retries must be >= 0")
	}
	return nil
}
