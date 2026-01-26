package common

import (
	"io"
	"sync"
	"time"

	"github.com/valyala/bytebufferpool"
	"github.com/valyala/fasthttp"
)

var requestJSONPool sync.Pool

func init() {
	requestJSONPool = sync.Pool{
		New: func() interface{} {
			return &RequestJSON{
				Headers: make(map[string]string, 16), // Pre-allocate with reasonable capacity
				// Set constant values once during initialization
				Myhostname: Myhostname,
				Usage: UsageStruct{
					PromptTokens:     1,
					CompletionTokens: 2,
					InputTokens:      100,
					OutputTokens:     200,
					TotalTokens:      300,
				},
			}
		},
	}
}

// AcquireRequestJSON gets a RequestJSON object from the pool
func AcquireRequestJSON() *RequestJSON {
	return requestJSONPool.Get().(*RequestJSON)
}

// ReleaseRequestJSON resets and returns the RequestJSON object to the pool
func ReleaseRequestJSON(reqJSON *RequestJSON) {
	ClearRequestJSON(reqJSON)
	requestJSONPool.Put(reqJSON)
}

// ClearRequestJSON clears the fields of a RequestJSON without returning it to the pool
// This is useful when the RequestJSON is embedded in another pooled struct
// Clears fields that could leak data or accumulate between requests
func ClearRequestJSON(reqJSON *RequestJSON) {
	reqJSON.Body = ""
	reqJSON.BodySize = 0
	reqJSON.BodyTruncated = false

	for k := range reqJSON.Headers {
		delete(reqJSON.Headers, k)
	}
}

const (
	// MaxResponseBodySize limits the body echoed back in responses to prevent
	// memory issues when receiving large request bodies.
	// Only the first MaxResponseBodySize bytes are read into memory.
	// The rest is discarded while counting total bytes received.
	MaxResponseBodySize = 1 * 1024 * 1024 // 1MB

	// ReadChunkSize is the size of chunks when reading body with delays.
	// 64KB matches typical TCP buffer sizes.
	ReadChunkSize = 64 * 1024 // 64KB
)

// ReadOptions configures how the request body is read.
type ReadOptions struct {
	// DelayPerChunkMs adds a delay (in milliseconds) before reading each chunk.
	// This simulates a slow backend that can't consume data quickly.
	// Set to 0 for fast reading (default behavior).
	DelayPerChunkMs int64
}

// ReadStats contains statistics about the body read operation.
type ReadStats struct {
	ChunksRead      int   // Number of chunks read
	TotalReadTimeMs int64 // Total time spent reading body (including delays)
}

// PopulateRequestJSON fills a RequestJSON object with data from the fasthttp context.
// This is a convenience wrapper around PopulateRequestJSONWithOptions with default options.
func PopulateRequestJSON(ctx *fasthttp.RequestCtx, reqJSON *RequestJSON) ReadStats {
	return PopulateRequestJSONWithOptions(ctx, reqJSON, ReadOptions{})
}

// PopulateRequestJSONWithOptions fills a RequestJSON object with data from the fasthttp context.
// This is a shared helper to avoid code duplication across handlers.
// The caller is responsible for acquiring and releasing the RequestJSON object.
//
// IMPORTANT: This function uses streaming to handle large request bodies efficiently.
// Only the first MaxResponseBodySize (1MB) bytes are kept in memory.
// The remaining bytes are read and discarded while counting total size.
// This prevents OOM when receiving multi-hundred-MB request bodies.
//
// When opts.DelayPerChunkMs > 0, a delay is applied before reading each chunk
// (except the first). This simulates a slow backend that can't consume data quickly,
// causing Envoy to apply backpressure to clients.
func PopulateRequestJSONWithOptions(ctx *fasthttp.RequestCtx, reqJSON *RequestJSON, opts ReadOptions) ReadStats {
	startTime := time.Now()
	stats := ReadStats{}

	req := &ctx.Request

	reqJSON.URI = B2s(req.URI().FullURI())
	reqJSON.Method = B2s(req.Header.Method())
	reqJSON.ContentType = B2s(req.Header.ContentType())

	// Stream the body to avoid loading entire payload into memory
	// Read first MaxResponseBodySize bytes, then discard the rest while counting
	bodyReader := ctx.RequestBodyStream()
	if bodyReader == nil {
		// No streaming body - fallback to regular body (small requests, already in memory)
		body := req.Body()
		reqJSON.BodySize = int64(len(body))
		stats.ChunksRead = 1
		if len(body) > MaxResponseBodySize {
			reqJSON.Body = string(body[:MaxResponseBodySize]) + "...[TRUNCATED]"
			reqJSON.BodyTruncated = true
		} else {
			reqJSON.Body = string(body)
			reqJSON.BodyTruncated = false
		}
	} else if opts.DelayPerChunkMs > 0 {
		// Slow reading mode - read in chunks with delays
		stats = readBodyWithDelay(bodyReader, reqJSON, opts.DelayPerChunkMs)
	} else {
		// Fast reading mode - read first chunk, then discard rest quickly
		stats = readBodyFast(bodyReader, reqJSON)
	}

	reqJSON.SourceAddr = ctx.RemoteAddr().String()
	reqJSON.DestinationAddr = ctx.LocalAddr().String()

	// Iterate headers using All() - zero-allocation iterator over internal slice
	for key, value := range req.Header.All() {
		reqJSON.Headers[B2s(key)] = B2s(value)
	}

	stats.TotalReadTimeMs = time.Since(startTime).Milliseconds()
	return stats
}

// readBodyFast reads the body as quickly as possible.
// Keeps first MaxResponseBodySize bytes, discards the rest while counting.
func readBodyFast(bodyReader io.Reader, reqJSON *RequestJSON) ReadStats {
	stats := ReadStats{ChunksRead: 1}

	// Use bytebufferpool for efficient buffer management
	firstBuf := bytebufferpool.Get()
	defer bytebufferpool.Put(firstBuf)

	// Read up to MaxResponseBodySize bytes
	if cap(firstBuf.B) < MaxResponseBodySize {
		firstBuf.B = make([]byte, MaxResponseBodySize)
	} else {
		firstBuf.B = firstBuf.B[:MaxResponseBodySize]
	}

	n, err := io.ReadFull(bodyReader, firstBuf.B)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		// Body is smaller than MaxResponseBodySize
		reqJSON.Body = string(firstBuf.B[:n])
		reqJSON.BodySize = int64(n)
		reqJSON.BodyTruncated = false
	} else if err != nil {
		// Read error
		reqJSON.Body = ""
		reqJSON.BodySize = 0
		reqJSON.BodyTruncated = false
	} else {
		// Body is larger than MaxResponseBodySize
		// Keep first chunk, discard rest while counting
		reqJSON.Body = string(firstBuf.B[:n]) + "...[TRUNCATED]"
		reqJSON.BodyTruncated = true

		// Discard remaining bytes using another pooled buffer
		discardBuf := bytebufferpool.Get()
		defer bytebufferpool.Put(discardBuf)

		if cap(discardBuf.B) < ReadChunkSize {
			discardBuf.B = make([]byte, ReadChunkSize)
		} else {
			discardBuf.B = discardBuf.B[:ReadChunkSize]
		}

		discarded, _ := io.CopyBuffer(io.Discard, bodyReader, discardBuf.B)
		reqJSON.BodySize = int64(n) + discarded
		// Estimate chunks for discarded data
		stats.ChunksRead += int((discarded + ReadChunkSize - 1) / ReadChunkSize)
	}

	return stats
}

// readBodyWithDelay reads the body in chunks with a delay between each chunk.
// This simulates a slow backend that can't consume data quickly.
// The delay is applied BEFORE reading each chunk (except the first).
// Note: reqJSON.BodySize must be 0 before calling (cleared by ClearRequestJSON)
func readBodyWithDelay(bodyReader io.Reader, reqJSON *RequestJSON, delayMs int64) ReadStats {
	stats := ReadStats{}

	// Use pooled buffer for reading chunks
	buf := bytebufferpool.Get()
	defer bytebufferpool.Put(buf)

	if cap(buf.B) < ReadChunkSize {
		buf.B = make([]byte, ReadChunkSize)
	} else {
		buf.B = buf.B[:ReadChunkSize]
	}

	// Buffer for collecting body preview (up to MaxResponseBodySize)
	var previewBuf []byte
	previewRemaining := MaxResponseBodySize

	for {
		// Delay before reading (except first chunk)
		if stats.ChunksRead > 0 && delayMs > 0 {
			time.Sleep(time.Duration(delayMs) * time.Millisecond)
		}

		// Read a chunk
		n, err := bodyReader.Read(buf.B)
		if n > 0 {
			stats.ChunksRead++
			reqJSON.BodySize += int64(n)

			// Collect preview bytes
			if previewRemaining > 0 {
				toKeep := n
				if toKeep > previewRemaining {
					toKeep = previewRemaining
					reqJSON.BodyTruncated = true
				}
				previewBuf = append(previewBuf, buf.B[:toKeep]...)
				previewRemaining -= toKeep
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			// Read error - stop reading
			break
		}
	}

	reqJSON.Body = string(previewBuf)
	if reqJSON.BodyTruncated {
		reqJSON.Body += "...[TRUNCATED]"
	}

	return stats
}
