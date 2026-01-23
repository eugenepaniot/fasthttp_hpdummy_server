package common

import (
	"io"
	"sync"

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
// Only clears fields that need clearing - other fields are overwritten by PopulateRequestJSON
func ClearRequestJSON(reqJSON *RequestJSON) {
	reqJSON.Body = ""
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
)

// PopulateRequestJSON fills a RequestJSON object with data from the fasthttp context
// This is a shared helper to avoid code duplication across handlers
// The caller is responsible for acquiring and releasing the RequestJSON object
//
// IMPORTANT: This function uses streaming to handle large request bodies efficiently.
// Only the first MaxResponseBodySize (1MB) bytes are kept in memory.
// The remaining bytes are read and discarded while counting total size.
// This prevents OOM when receiving multi-hundred-MB request bodies.
func PopulateRequestJSON(ctx *fasthttp.RequestCtx, reqJSON *RequestJSON) {
	req := &ctx.Request

	reqJSON.URI = B2s(req.URI().FullURI())
	reqJSON.Method = B2s(req.Header.Method())
	reqJSON.ContentType = B2s(req.Header.ContentType())

	// Stream the body to avoid loading entire payload into memory
	// Read first MaxResponseBodySize bytes, then discard the rest while counting
	bodyReader := ctx.RequestBodyStream()
	if bodyReader == nil {
		// No streaming body - fallback to regular body (small requests)
		body := req.Body()
		reqJSON.BodySize = int64(len(body))
		if len(body) > MaxResponseBodySize {
			reqJSON.Body = string(body[:MaxResponseBodySize]) + "...[TRUNCATED]"
			reqJSON.BodyTruncated = true
		} else {
			reqJSON.Body = string(body)
			reqJSON.BodyTruncated = false
		}
	} else {
		// Streaming body - read first chunk using pooled buffer, discard rest
		// Use bytebufferpool for efficient buffer management
		firstBuf := bytebufferpool.Get()
		defer bytebufferpool.Put(firstBuf)

		// Read up to MaxResponseBodySize bytes
		firstBuf.B = firstBuf.B[:cap(firstBuf.B)]
		if cap(firstBuf.B) < MaxResponseBodySize {
			// Buffer too small, grow it
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

			// Ensure discard buffer has reasonable size (64KB)
			const discardSize = 64 * 1024
			if cap(discardBuf.B) < discardSize {
				discardBuf.B = make([]byte, discardSize)
			} else {
				discardBuf.B = discardBuf.B[:discardSize]
			}

			discarded, _ := io.CopyBuffer(io.Discard, bodyReader, discardBuf.B)
			reqJSON.BodySize = int64(n) + discarded
		}
	}

	reqJSON.SourceAddr = ctx.RemoteAddr().String()
	reqJSON.DestinationAddr = ctx.LocalAddr().String()

	// Iterate headers using All() - zero-allocation iterator over internal slice
	for key, value := range req.Header.All() {
		reqJSON.Headers[B2s(key)] = B2s(value)
	}
}
