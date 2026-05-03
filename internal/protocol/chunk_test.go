package protocol_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/tiroq/chunkbridge/internal/protocol"
)

func TestChunkSingleFrame(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 100)
	frame := protocol.Frame{
		SessionID: "s",
		RequestID: "r",
		Payload:   payload,
	}
	chunks := protocol.Chunk(frame, protocol.MaxPayloadBytes)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].TotalChunks != 1 {
		t.Errorf("TotalChunks: got %d want 1", chunks[0].TotalChunks)
	}
}

func TestChunkMultiple(t *testing.T) {
	maxBytes := 10
	payload := bytes.Repeat([]byte("a"), 35)
	frame := protocol.Frame{
		SessionID: "s",
		RequestID: "r",
		Payload:   payload,
	}
	chunks := protocol.Chunk(frame, maxBytes)
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if c.ChunkIndex != uint32(i) {
			t.Errorf("chunk %d: ChunkIndex=%d", i, c.ChunkIndex)
		}
		if c.TotalChunks != 4 {
			t.Errorf("chunk %d: TotalChunks=%d", i, c.TotalChunks)
		}
	}
}

func TestReassemblerOrdered(t *testing.T) {
	r := protocol.NewReassembler(5 * time.Second)

	maxBytes := 10
	payload := bytes.Repeat([]byte("b"), 25)
	frame := protocol.Frame{
		SessionID: "s",
		RequestID: "rq",
		Payload:   payload,
	}
	chunks := protocol.Chunk(frame, maxBytes)

	for _, c := range chunks {
		cp := c
		got, ok := r.Add(&cp)
		if ok {
			if !bytes.Equal(got.Payload, payload) {
				t.Errorf("reassembled payload mismatch")
			}
			return
		}
	}
	t.Fatal("expected reassembly to complete")
}

func TestReassemblerOutOfOrder(t *testing.T) {
	r := protocol.NewReassembler(5 * time.Second)

	maxBytes := 10
	payload := bytes.Repeat([]byte("c"), 25)
	frame := protocol.Frame{
		SessionID: "s",
		RequestID: "rq2",
		Payload:   payload,
	}
	chunks := protocol.Chunk(frame, maxBytes)

	// Send in reverse order.
	for i := len(chunks) - 1; i >= 0; i-- {
		cp := chunks[i]
		got, ok := r.Add(&cp)
		if ok {
			if !bytes.Equal(got.Payload, payload) {
				t.Errorf("reassembled payload mismatch")
			}
			return
		}
	}
	t.Fatal("expected reassembly to complete")
}

func TestReassemblerDuplicates(t *testing.T) {
	r := protocol.NewReassembler(5 * time.Second)

	payload := bytes.Repeat([]byte("d"), 25)
	frame := protocol.Frame{
		SessionID: "s",
		RequestID: "rq3",
		Payload:   payload,
	}
	chunks := protocol.Chunk(frame, 10)

	completions := 0
	// Send the first chunk twice.
	for _, send := range []int{0, 0, 1, 2} {
		cp := chunks[send]
		_, ok := r.Add(&cp)
		if ok {
			completions++
		}
	}
	if completions != 1 {
		t.Errorf("expected 1 completion, got %d", completions)
	}
}

func TestReassemblerTimeout(t *testing.T) {
	r := protocol.NewReassembler(50 * time.Millisecond)

	payload := bytes.Repeat([]byte("e"), 25)
	frame := protocol.Frame{
		SessionID: "s",
		RequestID: "rq4",
		Payload:   payload,
	}
	chunks := protocol.Chunk(frame, 10)

	// Send only the first chunk, wait for timeout, then send all chunks.
	cp := chunks[0]
	_, _ = r.Add(&cp)

	time.Sleep(100 * time.Millisecond)

	// After timeout, the assembly should be evicted; sending remaining chunks
	// starts a fresh (incomplete) assembly.
	for _, c := range chunks[1:] {
		cp2 := c
		got, ok := r.Add(&cp2)
		if ok {
			// Unexpected completion – would be wrong data anyway.
			t.Errorf("unexpected completion with payload len=%d", len(got.Payload))
		}
	}
}
