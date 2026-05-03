package compress

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

// Compress compresses data using gzip.
func Compress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, fmt.Errorf("compress: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("compress: close: %w", err)
	}
	return buf.Bytes(), nil
}

// Decompress decompresses gzip-compressed data.
func Decompress(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("compress: new reader: %w", err)
	}
	defer r.Close()

	out, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("compress: read: %w", err)
	}
	return out, nil
}
