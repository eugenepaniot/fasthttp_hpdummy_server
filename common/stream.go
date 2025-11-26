package common

import (
	"bufio"
	"io"
	"log"
	"sync"
	"time"

	"github.com/valyala/fasthttp"
)

// DataPattern is the pre-allocated pattern for binary/chunked data generation
// Pattern size matches buffer size for efficiency
var DataPattern []byte

// BinaryBufferPool provides buffers for all streaming handlers
// Shared between binary and chunked handlers for simplicity
// Size is configurable via InitBinaryBufferPool()
var BinaryBufferPool *ChunkBufferPool

// InitBinaryBufferPool initializes the buffer pool with the specified size
// Should be called once during server startup before handling any requests
func InitBinaryBufferPool(bufferSize int) {
	// Create data pattern matching buffer size
	basePattern := []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	DataPattern = make([]byte, bufferSize)
	for i := 0; i < bufferSize; i++ {
		DataPattern[i] = basePattern[i%len(basePattern)]
	}

	// Initialize single buffer pool for all streaming handlers
	BinaryBufferPool = NewChunkBufferPool(bufferSize, DataPattern)
	BinaryBufferPool.PreWarm(10)
}

// StreamWriter is a reusable writer for streaming data in chunks
// Using a struct with pooling avoids closure allocations at high RPS
// Implements both io.Reader and io.WriterTo for flexibility
type StreamWriter struct {
	TotalSize     int64   // Total bytes to send (chunks calculated automatically)
	ChunkSize     int     // Size of each chunk in bytes (all chunks are same size)
	DelayMs       int64   // Delay between chunks in milliseconds
	ChunkData     *[]byte // Pre-filled chunk data (borrowed from pool)
	LogPrefix     string  // Prefix for log messages (e.g., "[BIN]", "[CHUNKED]")
	FlushPerChunk bool    // Flush after each chunk (true for chunked, false for binary)

	// State for io.Reader implementation (fallback only, WriteTo is preferred)
	written int64 // Total bytes written/read so far
}

// Write implements the streaming logic for chunked responses
// Optimized for maximum throughput with minimal abstraction overhead
func (sw *StreamWriter) Write(w *bufio.Writer) {
	// Return StreamWriter to pool when done
	defer streamWriterPool.Put(sw)

	// Fast path for binary transfers without delays
	if sw.DelayMs == 0 && !sw.FlushPerChunk {
		// Direct write loop for maximum performance (6+ GB/s)
		// Avoid io.Copy overhead - just write the buffer repeatedly
		remaining := sw.TotalSize
		bufferSize := int64(len(*sw.ChunkData))

		for remaining > 0 {
			writeSize := bufferSize
			if remaining < bufferSize {
				writeSize = remaining
			}

			if _, err := w.Write((*sw.ChunkData)[:writeSize]); err != nil {
				if sw.LogPrefix != "" {
					log.Printf("%s write error: %v", sw.LogPrefix, err)
				}
				return
			}

			remaining -= writeSize
		}

		// Final flush
		if err := w.Flush(); err != nil {
			if sw.LogPrefix != "" {
				log.Printf("%s flush error: %v", sw.LogPrefix, err)
			}
		}
		return
	}

	// For chunked responses with delays, use chunk-based approach
	totalChunks := int((sw.TotalSize + int64(sw.ChunkSize) - 1) / int64(sw.ChunkSize))
	if totalChunks <= 0 {
		return
	}

	remainingBytes := sw.TotalSize

	for i := 0; i < totalChunks; i++ {
		if i > 0 && sw.DelayMs > 0 {
			time.Sleep(time.Duration(sw.DelayMs) * time.Millisecond)
		}

		chunkBytesToWrite := int64(sw.ChunkSize)
		if remainingBytes < int64(sw.ChunkSize) {
			chunkBytesToWrite = remainingBytes
		}

		chunkBytesWritten := int64(0)
		for chunkBytesWritten < chunkBytesToWrite {
			writeSize := int(chunkBytesToWrite - chunkBytesWritten)
			bufferSize := len(*sw.ChunkData)
			if writeSize > bufferSize {
				writeSize = bufferSize
			}

			if _, err := w.Write((*sw.ChunkData)[:writeSize]); err != nil {
				if sw.LogPrefix != "" {
					log.Printf("%s write error on chunk %d/%d: %v", sw.LogPrefix, i+1, totalChunks, err)
				}
				return
			}

			chunkBytesWritten += int64(writeSize)
		}

		remainingBytes -= chunkBytesToWrite

		if sw.FlushPerChunk {
			if err := w.Flush(); err != nil {
				if sw.LogPrefix != "" {
					log.Printf("%s flush error on chunk %d/%d: %v", sw.LogPrefix, i+1, totalChunks, err)
				}
				return
			}
		}
	}

	if err := w.Flush(); err != nil {
		if sw.LogPrefix != "" {
			log.Printf("%s final flush error: %v", sw.LogPrefix, err)
		}
	}
}

// Read implements io.Reader interface for use with SetBodyStream (fallback)
// Note: WriteTo will be called by fasthttp/io.Copy if available (much faster)
func (sw *StreamWriter) Read(p []byte) (n int, err error) {
	if sw.written >= sw.TotalSize {
		return 0, io.EOF
	}

	remaining := sw.TotalSize - sw.written
	toWrite := int64(len(p))
	if toWrite > remaining {
		toWrite = remaining
	}

	bufferSize := int64(len(*sw.ChunkData))

	// Calculate position within the repeating buffer pattern
	// Use simple modulo - only called as fallback, so rare overhead is acceptable
	offset := sw.written % bufferSize
	available := bufferSize - offset

	if toWrite > available {
		toWrite = available
	}

	// Copy from buffer
	copy(p, (*sw.ChunkData)[offset:offset+toWrite])
	sw.written += toWrite

	return int(toWrite), nil
}

// WriteTo implements io.WriterTo for maximum performance
// This is the primary path used by fasthttp for streaming responses
// Optimized to write full buffer chunks whenever possible
func (sw *StreamWriter) WriteTo(w io.Writer) (n int64, err error) {
	remaining := sw.TotalSize
	bufferSize := int64(len(*sw.ChunkData))
	buffer := *sw.ChunkData

	// Fast path: write full buffers
	for remaining >= bufferSize {
		written, writeErr := w.Write(buffer)
		n += int64(written)
		remaining -= int64(written)

		if writeErr != nil {
			return n, writeErr
		}
	}

	// Write final partial buffer if needed
	if remaining > 0 {
		written, writeErr := w.Write(buffer[:remaining])
		n += int64(written)
		if writeErr != nil {
			return n, writeErr
		}
	}

	return n, nil
}

// streamWriterPool provides reusable StreamWriter instances
var streamWriterPool = sync.Pool{
	New: func() interface{} {
		return &StreamWriter{}
	},
}

// AcquireStreamWriter gets a StreamWriter from the pool
func AcquireStreamWriter() *StreamWriter {
	sw := streamWriterPool.Get().(*StreamWriter)
	// Reset state for io.Reader reuse
	sw.written = 0
	return sw
}

// StreamResponse sets up a streaming response using StreamWriter
// Always uses SetBodyStreamWriter (chunked encoding or streaming with unknown size)
// Automatically manages buffer acquisition and cleanup
func StreamResponse(ctx *fasthttp.RequestCtx, totalSize int64, chunkSize int, delayMs int64, flushPerChunk bool, logPrefix string) {
	ctx.SetBodyStreamWriter(func(w *bufio.Writer) {
		chunkData := BinaryBufferPool.Get()
		defer BinaryBufferPool.Put(chunkData)

		sw := AcquireStreamWriter()
		sw.TotalSize = totalSize
		sw.ChunkSize = chunkSize
		sw.DelayMs = delayMs
		sw.FlushPerChunk = flushPerChunk
		sw.ChunkData = chunkData
		sw.LogPrefix = logPrefix
		sw.Write(w)
	})
}

// StreamResponseWithContentLength sets up a streaming response with known Content-Length
// Uses SetBodyStream for maximum performance (no chunking overhead)
// Automatically manages buffer acquisition and cleanup
// Note: SetBodyStream automatically sets Content-Length header
func StreamResponseWithContentLength(ctx *fasthttp.RequestCtx, totalSize int64, chunkSize int, logPrefix string) {
	chunkData := BinaryBufferPool.Get()
	sw := AcquireStreamWriter()
	sw.TotalSize = totalSize
	sw.ChunkSize = chunkSize
	sw.DelayMs = 0
	sw.FlushPerChunk = false
	sw.ChunkData = chunkData
	sw.LogPrefix = logPrefix

	reader := NewAutoCleanupReader(sw, chunkData, BinaryBufferPool)
	// SetBodyStream automatically sets Content-Length header
	ctx.Response.SetBodyStream(reader, int(totalSize))
}

// autoCleanupReader wraps StreamWriter and handles cleanup after EOF
type autoCleanupReader struct {
	sw        *StreamWriter
	chunkData *[]byte
	pool      *ChunkBufferPool
	cleaned   bool
}

// Read implements io.Reader and cleans up resources after EOF
func (r *autoCleanupReader) Read(p []byte) (n int, err error) {
	n, err = r.sw.Read(p)
	if err == io.EOF && !r.cleaned {
		r.cleaned = true
		r.pool.Put(r.chunkData)
		streamWriterPool.Put(r.sw)
	}
	return n, err
}

// WriteTo implements io.WriterTo for performance
func (r *autoCleanupReader) WriteTo(w io.Writer) (n int64, err error) {
	n, err = r.sw.WriteTo(w)
	if !r.cleaned {
		r.cleaned = true
		r.pool.Put(r.chunkData)
		streamWriterPool.Put(r.sw)
	}
	return n, err
}

// NewAutoCleanupReader creates a reader that auto-cleans up resources
func NewAutoCleanupReader(sw *StreamWriter, chunkData *[]byte, pool *ChunkBufferPool) io.Reader {
	return &autoCleanupReader{
		sw:        sw,
		chunkData: chunkData,
		pool:      pool,
	}
}

// ChunkBufferPool provides reusable chunk buffers of a given size
type ChunkBufferPool struct {
	pool        sync.Pool
	chunkSize   int
	fillPattern []byte // Optional pattern to pre-fill chunks
}

// NewChunkBufferPool creates a new chunk buffer pool with the specified chunk size
// If fillPattern is provided, chunks will be pre-filled with the repeating pattern
func NewChunkBufferPool(chunkSize int, fillPattern []byte) *ChunkBufferPool {
	cbp := &ChunkBufferPool{
		chunkSize:   chunkSize,
		fillPattern: fillPattern,
	}
	cbp.pool = sync.Pool{
		New: func() interface{} {
			chunk := make([]byte, chunkSize)
			// Pre-fill chunk with repeating pattern if provided
			// Use copy for efficiency instead of byte-by-byte assignment
			if len(fillPattern) > 0 {
				for filled := 0; filled < len(chunk); {
					n := copy(chunk[filled:], fillPattern)
					filled += n
				}
			}
			return &chunk
		},
	}
	return cbp
}

// Get retrieves a chunk buffer from the pool
func (cbp *ChunkBufferPool) Get() *[]byte {
	return cbp.pool.Get().(*[]byte)
}

// ChunkSize returns the size of chunks in this pool
func (cbp *ChunkBufferPool) ChunkSize() int {
	return cbp.chunkSize
}

// Put returns a chunk buffer to the pool
func (cbp *ChunkBufferPool) Put(chunk *[]byte) {
	cbp.pool.Put(chunk)
}

// PreWarm pre-warms the pool by creating and returning the specified number of buffers
func (cbp *ChunkBufferPool) PreWarm(count int) {
	for i := 0; i < count; i++ {
		chunk := cbp.Get()
		cbp.Put(chunk)
	}
}
