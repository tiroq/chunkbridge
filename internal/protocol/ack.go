package protocol

// AckFrame carries acknowledgement information.
type AckFrame struct {
	SessionID string `json:"sid"`
	AckTo     uint32 `json:"ack_to"`
}

// NewACKFrame builds a Frame of type FrameACK acknowledging seqNum.
func NewACKFrame(sessionID string, ackToSeq uint32, seq uint32) *Frame {
	return &Frame{
		Version:     1,
		Type:        FrameACK,
		SessionID:   sessionID,
		SeqNum:      seq,
		TotalChunks: 1,
		ChunkIndex:  0,
	}
}

// IsACK reports whether f is an acknowledgement frame.
func IsACK(f *Frame) bool {
	return f.Type == FrameACK
}
