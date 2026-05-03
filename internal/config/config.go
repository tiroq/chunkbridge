package config

// Config is the top-level configuration struct.
type Config struct {
	Mode      string           `yaml:"mode"`
	Listen    ListenConfig     `yaml:"listen"`
	Proxy     ProxyConfig      `yaml:"proxy"`
	Transport TransportConfig  `yaml:"transport"`
	Crypto    CryptoConfig     `yaml:"crypto"`
	Limits    RateLimitsConfig `yaml:"rate_limits"`
	Policy    PolicyConfig     `yaml:"policy"`
	Exit      ExitConfig       `yaml:"exit"`
	Log       LogConfig        `yaml:"log"`
}

// ListenConfig controls the local HTTP proxy listener.
type ListenConfig struct {
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

// ProxyConfig controls client-proxy-side request lifecycle settings.
type ProxyConfig struct {
	// MaxConcurrentRequests limits the number of in-flight pending requests
	// in the relay session. Zero or negative means unlimited (legacy).
	MaxConcurrentRequests int `yaml:"max_concurrent_requests"`
	// RequestTimeoutMs is the per-request relay timeout in milliseconds.
	// Zero falls back to the hard-coded 30 s default.
	RequestTimeoutMs int `yaml:"request_timeout_ms"`
}

// TransportConfig selects and configures the message transport.
type TransportConfig struct {
	Type string    `yaml:"type"` // "memory", "max"
	Max  MaxConfig `yaml:"max"`
}

// MaxConfig holds Max.ai / messaging API settings.
type MaxConfig struct {
	TokenEnv   string `yaml:"token_env"`
	FromHandle string `yaml:"from_handle"`
	ToHandle   string `yaml:"to_handle"`
	PollMs     int    `yaml:"poll_ms"`
}

// CryptoConfig holds key-derivation parameters.
type CryptoConfig struct {
	PassphraseEnv string `yaml:"passphrase_env"`
	Salt          string `yaml:"salt"`
	Argon2Time    uint32 `yaml:"argon2_time"`
	Argon2Mem     uint32 `yaml:"argon2_memory_kb"`
	Argon2Threads uint8  `yaml:"argon2_threads"`
}

// RateLimitsConfig controls global and per-bucket rate limits.
type RateLimitsConfig struct {
	GlobalRPS  float64       `yaml:"global_rps"`
	DataRPS    float64       `yaml:"data_rps"`
	ControlRPS float64       `yaml:"control_rps"`
	Burst      int           `yaml:"burst"`
	Message    MessageConfig `yaml:"message"`
	Ack        AckConfig     `yaml:"ack"`
	Window     WindowConfig  `yaml:"window"`
}

// MessageConfig controls message-level limits.
type MessageConfig struct {
	MaxChars    int `yaml:"max_chars"`
	SafeChars   int `yaml:"safe_chars"`
	MaxB64Chars int `yaml:"max_b64_chars"`
}

// AckConfig controls acknowledgement behaviour.
type AckConfig struct {
	IntervalMs int `yaml:"interval_ms"`
	TimeoutMs  int `yaml:"timeout_ms"`
	MaxRetries int `yaml:"max_retries"`
}

// WindowConfig controls the sliding window.
type WindowConfig struct {
	InitialSize int `yaml:"initial_size"`
	MaxSize     int `yaml:"max_size"`
	MinSize     int `yaml:"min_size"`
}

// PolicyConfig controls outbound request policy enforced by the exit node.
type PolicyConfig struct {
	DomainAllowList     []string `yaml:"domain_allow_list"`
	BlockPrivateRanges  bool     `yaml:"block_private_ranges"`
	BlockedPorts        []int    `yaml:"blocked_ports"`
	AllowedPorts        []int    `yaml:"allowed_ports"`
	MaxResponseBytes    int64    `yaml:"max_response_bytes"`
	AllowedContentTypes []string `yaml:"allowed_content_types"`
	AllowedSchemes      []string `yaml:"allowed_schemes"`
}

// ExitConfig controls the exit-node HTTP executor.
type ExitConfig struct {
	RequestTimeoutSec  int `yaml:"request_timeout_sec"`
	ResponseBufferSize int `yaml:"response_buffer_size"`
}

// LogConfig controls logging behaviour.
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"` // "json" | "text"
}
