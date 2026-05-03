package protocol_test

import (
	"testing"

	"github.com/tiroq/chunkbridge/internal/protocol"
)

// testKey is a 32-byte key used in tests (never derive via Argon2 in unit tests).
var testKey = []byte("12345678901234567890123456789012")

func TestEncodeDecodeRoundtrip(t *testing.T) {
	frame := &protocol.Frame{
		Version:     1,
		Type:        protocol.FrameDATA,
		SessionID:   "session-abc",
		RequestID:   "req-001",
		SeqNum:      42,
		TotalChunks: 1,
		ChunkIndex:  0,
		Payload:     []byte(`{"hello":"world"}`),
	}

	text, err := protocol.EncodeMessage(frame, testKey)
	if err != nil {
		t.Fatalf("EncodeMessage: %v", err)
	}

	got, err := protocol.DecodeMessage(text, testKey)
	if err != nil {
		t.Fatalf("DecodeMessage: %v", err)
	}

	if got.SessionID != frame.SessionID {
		t.Errorf("SessionID: got %q want %q", got.SessionID, frame.SessionID)
	}
	if got.RequestID != frame.RequestID {
		t.Errorf("RequestID: got %q want %q", got.RequestID, frame.RequestID)
	}
	if got.SeqNum != frame.SeqNum {
		t.Errorf("SeqNum: got %d want %d", got.SeqNum, frame.SeqNum)
	}
	if string(got.Payload) != string(frame.Payload) {
		t.Errorf("Payload: got %q want %q", got.Payload, frame.Payload)
	}
}

func TestDecodeMalformed(t *testing.T) {
	cases := []string{
		"",
		"CB1|D|only-three",
		"notCB1|D|s|0|data",
		"CB1|D|s|0|!!!not-base64!!!",
	}
	for _, tc := range cases {
		_, err := protocol.DecodeMessage(tc, testKey)
		if err == nil {
			t.Errorf("expected error for %q", tc)
		}
	}
}

func TestDecodeUnknownVersion(t *testing.T) {
	_, err := protocol.DecodeMessage("CB2|D|session|0|aaaa", testKey)
	if err == nil {
		t.Fatal("expected error for unknown version")
	}
}

func TestDecodeTamperedCiphertext(t *testing.T) {
	frame := &protocol.Frame{
		Version:   1,
		Type:      protocol.FrameDATA,
		SessionID: "s",
		RequestID: "r",
		SeqNum:    1,
		Payload:   []byte("secret"),
	}
	text, _ := protocol.EncodeMessage(frame, testKey)
	// Corrupt the base64 payload by replacing the last char.
	corrupted := text[:len(text)-4] + "AAAA"
	_, err := protocol.DecodeMessage(corrupted, testKey)
	if err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
}
