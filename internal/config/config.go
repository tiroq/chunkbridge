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
	Cache     CacheConfig      `yaml:"cache"`
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
	// TokenEnv is the name of the environment variable that holds the bearer token.
	// Required when transport.type = "max".
	TokenEnv string `yaml:"token_env"`
	// BaseURL is the root URL of the MAX Bot API, e.g. "https://api.max.example.com/v1".
	// Required when transport.type = "max".
	BaseURL string `yaml:"base_url"`
	// PeerChatID is the chat ID of the remote chunkbridge endpoint.
	// Required when transport.type = "max".
	PeerChatID string `yaml:"peer_chat_id"`
	// FromHandle is the handle of this endpoint. Messages from this sender
	// are filtered out during receive to avoid echo.
	FromHandle string `yaml:"from_handle"`
	// PollMs is the delay in ms between consecutive poll requests when the
	// previous response was empty. Default: 1000.
	PollMs int `yaml:"poll_ms"`
	// PollTimeoutSec is the server-side long-poll timeout in seconds sent
	// as the `timeout` query parameter. Default: 20.
	PollTimeoutSec int `yaml:"poll_timeout_sec"`
	// DedupeMaxIDs is the maximum number of message IDs kept in the receive
	// deduplication window. When the window is full the oldest ID is evicted;
	// a message whose ID was evicted may be re-delivered.
	// Must be > 0 when transport.type is "max". Default: 4096.
	DedupeMaxIDs int `yaml:"dedupe_max_ids"`
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

// CacheConfig controls the client-side in-memory HTTP response cache.
// The cache is disabled by default and only applies in client/proxy mode.
type CacheConfig struct {
	// Enabled activates the in-memory response cache. Default: false.
	Enabled bool `yaml:"enabled"`
	// MaxEntries is the maximum number of responses kept in the cache.
	// Required when enabled. Default: 512.
	MaxEntries int `yaml:"max_entries"`
	// MaxBytes is the maximum total body bytes stored across all entries.
	// Required when enabled. Default: 67108864 (64 MiB).
	MaxBytes int64 `yaml:"max_bytes"`
	// MaxEntryBytes is the maximum body size of a single cacheable response.
	// Responses larger than this are not stored. Required when enabled.
	// Default: 2097152 (2 MiB).
	MaxEntryBytes int64 `yaml:"max_entry_bytes"`
	// DefaultTTLSeconds is applied to static-looking paths (CSS, JS, images,
	// etc.) when the response carries no explicit Cache-Control or Expires
	// header. Set to 0 to disable heuristic caching. Default: 300.
	DefaultTTLSeconds int `yaml:"default_ttl_seconds"`
	// CachePrivate allows caching of responses marked Cache-Control: private.
	// Default: false.
	CachePrivate bool `yaml:"cache_private"`
	// CacheWithCookies allows caching requests that carry a Cookie header.
	// Default: false.
	CacheWithCookies bool `yaml:"cache_with_cookies"`
	// CacheWithAuthorization allows caching requests that carry an
	// Authorization header. Default: false.
	CacheWithAuthorization bool `yaml:"cache_with_authorization"`
}
