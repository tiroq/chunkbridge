package protocol

import "encoding/json"

// AckFrame carries acknowledgement information.
type AckFrame struct {
	SessionID string `json:"sid"`
	AckTo     uint32 `json:"ack_to"`
}

// NewACKFrame builds a Frame of type FrameACK acknowledging up to ackToSeq.
// The AckFrame is marshalled into the frame payload so the receiver can
// extract the acknowledged sequence number.
func NewACKFrame(sessionID string, ackToSeq uint32, seq uint32) *Frame {
	af := AckFrame{SessionID: sessionID, AckTo: ackToSeq}
	payload, _ := json.Marshal(af)
	return &Frame{
		Version:     1,
		Type:        FrameACK,
		SessionID:   sessionID,
		SeqNum:      seq,
		TotalChunks: 1,
		ChunkIndex:  0,
		Payload:     payload,
	}
}

// IsACK reports whether f is an acknowledgement frame.
func IsACK(f *Frame) bool {
	return f.Type == FrameACK
}
