package upload

import (
	"fasthttp_hpdummy_server/common"
	"io"
	"sync"

	json "github.com/bytedance/sonic"
	"github.com/valyala/fasthttp"
)

// UploadResponse contains information about the discarded upload
type UploadResponse struct {
	*common.RequestJSON
	BytesReceived int64 `json:"bytes_received"`
}

// uploadResponsePool is a sync.Pool for UploadResponse objects
var uploadResponsePool = sync.Pool{
	New: func() interface{} {
		return &UploadResponse{
			RequestJSON: common.AcquireRequestJSON(),
		}
	},
}

// acquireUploadResponse gets an UploadResponse from the pool
func acquireUploadResponse() *UploadResponse {
	return uploadResponsePool.Get().(*UploadResponse)
}

// releaseUploadResponse returns an UploadResponse to the pool after clearing it
// Note: We keep the embedded RequestJSON - just clear its fields via clearRequestJSON
func releaseUploadResponse(resp *UploadResponse) {
	common.ClearRequestJSON(resp.RequestJSON)
	uploadResponsePool.Put(resp)
}

const (
	// discardBufferSize is the size of the buffer used to read and discard data
	// 1MB provides good balance between memory usage and syscall overhead
	discardBufferSize = 1024 * 1024
)

// discardBufferPool provides reusable buffers for streaming discard
var discardBufferPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, discardBufferSize)
		return &buf
	},
}

// init initializes the upload handler
func init() {
	// Pre-warm the pools
	for i := 0; i < 10; i++ {
		resp := acquireUploadResponse()
		releaseUploadResponse(resp)

		buf := discardBufferPool.Get().(*[]byte)
		discardBufferPool.Put(buf)
	}
}

// Description returns a description of the upload handler for startup logging
func Description() string {
	return "  - /upload     -> Upload sink (streams and discards body, returns byte count)"
}

// Handler processes upload requests by streaming and discarding the body
// Uses streaming to handle large uploads without accumulating data in memory
//
// Request:  POST /upload (with body content)
// Response: {"bytes_received": 1048576, ...}
func Handler(ctx *fasthttp.RequestCtx) {
	// Stream and discard the body to avoid memory accumulation
	bytesReceived, err := streamAndDiscard(ctx)
	if err != nil {
		common.SendJSONResponseWithStatus(ctx, fasthttp.StatusInternalServerError,
			[]byte(`{"error":"failed to read request body"}`))
		return
	}

	// Build response JSON
	jsonData, err := buildResponseJSON(ctx, bytesReceived)
	if err != nil {
		common.SendJSONResponseWithStatus(ctx, fasthttp.StatusInternalServerError,
			[]byte(`{"error":"failed to marshal response"}`))
		return
	}

	// Send response using centralized helper
	common.SendJSONResponse(ctx, jsonData)
}

// streamAndDiscard reads the request body in chunks and discards it
// Returns the total number of bytes read
func streamAndDiscard(ctx *fasthttp.RequestCtx) (int64, error) {
	// Get the body stream reader
	// This allows reading the body without buffering it entirely in memory
	bodyStream := ctx.RequestBodyStream()
	if bodyStream == nil {
		// No streaming body, fall back to buffered body (small requests)
		return int64(len(ctx.Request.Body())), nil
	}

	// Acquire a discard buffer from pool
	bufPtr := discardBufferPool.Get().(*[]byte)
	buf := *bufPtr
	defer discardBufferPool.Put(bufPtr)

	var totalBytes int64
	for {
		n, err := bodyStream.Read(buf)
		totalBytes += int64(n)

		if err == io.EOF {
			break
		}
		if err != nil {
			return totalBytes, err
		}
	}

	return totalBytes, nil
}

// buildResponseJSON creates the JSON response with upload statistics
func buildResponseJSON(ctx *fasthttp.RequestCtx, bytesReceived int64) ([]byte, error) {
	// Acquire UploadResponse from pool (includes embedded RequestJSON)
	uploadResp := acquireUploadResponse()
	defer releaseUploadResponse(uploadResp)

	// Populate request data using shared function
	// Note: Body will be empty since we streamed it
	common.PopulateRequestJSON(ctx, uploadResp.RequestJSON)

	// Override body size with actual bytes received
	uploadResp.RequestJSON.BodySize = bytesReceived
	uploadResp.BytesReceived = bytesReceived

	// Marshal to JSON and return
	return json.Marshal(uploadResp)
}
