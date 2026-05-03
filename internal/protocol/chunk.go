package protocol

// MaxPayloadBytes is the maximum number of bytes allowed in a single frame's
// payload before chunking is required. Chosen to keep the final encoded
// transport string well within the 3400 base64-character safety limit.
const MaxPayloadBytes = 1600

// Chunk splits a Frame whose payload exceeds maxPayloadBytes into a slice of
// frames with the same metadata but distinct ChunkIndex values.
// SeqNum is NOT assigned here; callers must assign unique SeqNums before
// encoding each chunk.
func Chunk(frame Frame, maxPayloadBytes int) []Frame {
	if len(frame.Payload) <= maxPayloadBytes {
		frame.TotalChunks = 1
		frame.ChunkIndex = 0
		return []Frame{frame}
	}

	total := (len(frame.Payload) + maxPayloadBytes - 1) / maxPayloadBytes
	chunks := make([]Frame, total)

	for i := 0; i < total; i++ {
		start := i * maxPayloadBytes
		end := start + maxPayloadBytes
		if end > len(frame.Payload) {
			end = len(frame.Payload)
		}

		c := frame // shallow copy
		c.Payload = make([]byte, end-start)
		copy(c.Payload, frame.Payload[start:end])
		c.TotalChunks = uint32(total)
		c.ChunkIndex = uint32(i)
		chunks[i] = c
	}

	return chunks
}
