package protocol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/tiroq/chunkbridge/internal/compress"
	"github.com/tiroq/chunkbridge/internal/crypto"
)

// EncodeMessage serializes a Frame into a transport-ready string:
//
//	CB1|D|<session_id>|<seq_num>|<base64_encrypted_data>
func EncodeMessage(frame *Frame, key []byte) (string, error) {
	raw, err := json.Marshal(frame)
	if err != nil {
		return "", fmt.Errorf("encode: marshal: %w", err)
	}

	compressed, err := compress.Compress(raw)
	if err != nil {
		return "", fmt.Errorf("encode: compress: %w", err)
	}

	seqStr := strconv.FormatUint(uint64(frame.SeqNum), 10)
	aad := []byte(frame.SessionID + "|" + seqStr)

	ciphertext, err := crypto.Encrypt(key, compressed, aad)
	if err != nil {
		return "", fmt.Errorf("encode: encrypt: %w", err)
	}

	b64 := base64.StdEncoding.EncodeToString(ciphertext)
	text := "CB1|D|" + frame.SessionID + "|" + seqStr + "|" + b64
	return text, nil
}
