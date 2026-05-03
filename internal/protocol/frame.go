package protocol

// FrameType identifies the kind of protocol frame.
type FrameType uint8

const (
	FrameHELLO  FrameType = 1
	FrameOPEN   FrameType = 2
	FrameDATA   FrameType = 3
	FrameACK    FrameType = 4
	FrameWINDOW FrameType = 5
	FrameCLOSE  FrameType = 6
	FramePING   FrameType = 7
	FramePONG   FrameType = 8
	FrameERROR  FrameType = 9
)

// Frame is the core protocol unit.
type Frame struct {
	Version     uint8     `json:"v"`
	Type        FrameType `json:"t"`
	SessionID   string    `json:"sid"`
	StreamID    string    `json:"stid,omitempty"`
	RequestID   string    `json:"rid"`
	SeqNum      uint32    `json:"seq"`
	TotalChunks uint32    `json:"total"`
	ChunkIndex  uint32    `json:"idx"`
	Payload     []byte    `json:"payload,omitempty"`
}
