package protocol

// Envelope wraps an encrypted frame for transport.
// The actual bytes are opaque after encryption.
type Envelope struct {
	// Version is the protocol version (currently 1).
	Version int `json:"v"`
	// SessionID is the plaintext session identifier (used as AAD).
	SessionID string `json:"sid"`
	// SeqNum is the plaintext sequence number (used as AAD).
	SeqNum uint32 `json:"seq"`
	// Ciphertext holds the encrypted+compressed frame JSON bytes.
	Ciphertext []byte `json:"ct"`
}
