package gossip

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"

	"github.com/raj/fluid/pkg/metrics"
)

// compressMessage compresses a message if it exceeds maxSize.
func compressMessage(data []byte, maxSize int) ([]byte, bool) {
	if len(data) <= maxSize {
		return data, false
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		metrics.SetGossipCompressionRatio("error", 1.0)
		return data, false
	}
	if err := gz.Close(); err != nil {
		metrics.SetGossipCompressionRatio("error", 1.0)
		return data, false
	}

	compressed := buf.Bytes()
	ratio := float64(len(compressed)) / float64(len(data))
	metrics.SetGossipCompressionRatio("gzip", ratio)

	if len(compressed) < len(data) {
		return compressed, true
	}
	return data, false
}

// decompressMessage decompresses a message if it's compressed.
func decompressMessage(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		// Not gzip compressed
		return data, nil
	}

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	return io.ReadAll(gz)
}

// batchMessages combines multiple messages into a single batch.
func batchMessages(messages [][]byte, maxBatchSize int) [][]byte {
	if len(messages) == 0 {
		return nil
	}

	var batches [][]byte
	var currentBatch []byte

	for _, msg := range messages {
		// If adding this message would exceed batch size, start a new batch
		if len(currentBatch) > 0 && len(currentBatch)+len(msg)+1 > maxBatchSize {
			batches = append(batches, currentBatch)
			currentBatch = nil
		}

		if len(currentBatch) == 0 {
			currentBatch = msg
		} else {
			// Simple concatenation with newline separator
			currentBatch = append(currentBatch, '\n')
			currentBatch = append(currentBatch, msg...)
		}
	}

	if len(currentBatch) > 0 {
		batches = append(batches, currentBatch)
	}

	return batches
}

// unbatchMessages splits a batched message back into individual messages.
func unbatchMessages(batch []byte) [][]byte {
	if len(batch) == 0 {
		return nil
	}

	// Split by newline separator
	parts := bytes.Split(batch, []byte{'\n'})
	var messages [][]byte

	for _, part := range parts {
		if len(part) > 0 {
			messages = append(messages, part)
		}
	}

	return messages
}

// wireMsgWithCompression adds compression metadata to wire messages.
type wireMsgWithCompression struct {
	Compressed bool   `json:"compressed,omitempty"`
	Data       []byte `json:"data"`
}

// encodeMessage encodes a message with optional compression.
func encodeMessage(msg interface{}, maxSize int) ([]byte, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	compressed, isCompressed := compressMessage(data, maxSize)

	if isCompressed {
		wrapped := wireMsgWithCompression{
			Compressed: true,
			Data:       compressed,
		}
		return json.Marshal(wrapped)
	}

	return data, nil
}

// decodeMessage decodes a message with optional decompression.
func decodeMessage(data []byte, target interface{}) error {
	// Try to decode as wrapped message first
	var wrapped wireMsgWithCompression
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Compressed {
		decompressed, err := decompressMessage(wrapped.Data)
		if err != nil {
			return err
		}
		return json.Unmarshal(decompressed, target)
	}

	// Try direct decompression
	decompressed, err := decompressMessage(data)
	if err != nil {
		return err
	}

	return json.Unmarshal(decompressed, target)
}
