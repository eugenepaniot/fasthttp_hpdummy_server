package common

import (
	"sync"

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
	// The full body is still received and BodySize reflects the actual size,
	// but only the first MaxResponseBodySize bytes are included in the response.
	MaxResponseBodySize = 1 * 1024 * 1024 // 1MB
)

// PopulateRequestJSON fills a RequestJSON object with data from the fasthttp context
// This is a shared helper to avoid code duplication across handlers
// The caller is responsible for acquiring and releasing the RequestJSON object
//
// Note: The Body field is truncated to MaxResponseBodySize (1MB) to prevent
// memory issues when echoing large request bodies back in responses.
// BodySize always reflects the actual full request body size.
func PopulateRequestJSON(ctx *fasthttp.RequestCtx, reqJSON *RequestJSON) {
	req := &ctx.Request

	reqJSON.URI = B2s(req.URI().FullURI())
	reqJSON.Method = B2s(req.Header.Method())
	reqJSON.ContentType = B2s(req.Header.ContentType())

	// Get full body and record actual size
	body := req.Body()
	reqJSON.BodySize = int64(len(body))

	// Truncate body in response to prevent memory issues with large payloads
	// Client can check BodySize to see the actual size received
	if len(body) > MaxResponseBodySize {
		reqJSON.Body = B2s(body[:MaxResponseBodySize]) + "...[TRUNCATED]"
		reqJSON.BodyTruncated = true
	} else {
		reqJSON.Body = B2s(body)
		reqJSON.BodyTruncated = false
	}

	reqJSON.SourceAddr = ctx.RemoteAddr().String()
	reqJSON.DestinationAddr = ctx.LocalAddr().String()

	// Iterate headers using All() - zero-allocation iterator over internal slice
	for key, value := range req.Header.All() {
		reqJSON.Headers[B2s(key)] = B2s(value)
	}
}
