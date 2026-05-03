package protocol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tiroq/chunkbridge/internal/compress"
	"github.com/tiroq/chunkbridge/internal/crypto"
)

// ErrUnknownVersion is returned when the message prefix is not "CB1".
var ErrUnknownVersion = fmt.Errorf("decode: unknown protocol version")

// ErrMalformed is returned when the message does not match the expected format.
var ErrMalformed = fmt.Errorf("decode: malformed message")

// DecodeMessage parses a transport string back into a Frame.
// It reverses the EncodeMessage pipeline: parse -> base64 -> decrypt -> decompress -> unmarshal.
func DecodeMessage(text string, key []byte) (*Frame, error) {
	// Split into exactly 5 parts: version|type|session|seq|data
	parts := strings.SplitN(text, "|", 5)
	if len(parts) != 5 {
		return nil, ErrMalformed
	}

	if parts[0] != "CB1" {
		return nil, ErrUnknownVersion
	}

	// Validate the message type field: only "D" (data) and "A" (ACK) are defined.
	msgType := parts[1]
	if msgType != "D" && msgType != "A" {
		return nil, fmt.Errorf("%w: unknown message type %q", ErrMalformed, msgType)
	}

	sessionID := parts[2]
	seqStr := parts[3]
	b64Data := parts[4]

	ciphertext, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		return nil, fmt.Errorf("decode: base64: %w", err)
	}

	aad := []byte(sessionID + "|" + seqStr)
	compressed, err := crypto.Decrypt(key, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("decode: decrypt: %w", err)
	}

	raw, err := compress.Decompress(compressed)
	if err != nil {
		return nil, fmt.Errorf("decode: decompress: %w", err)
	}

	var frame Frame
	if err := json.Unmarshal(raw, &frame); err != nil {
		return nil, fmt.Errorf("decode: unmarshal: %w", err)
	}

	return &frame, nil
}
