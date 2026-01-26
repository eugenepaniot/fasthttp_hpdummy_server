package delay

import (
	"bytes"
	"fasthttp_hpdummy_server/common"
	"strconv"
	"sync"
	"time"

	json "github.com/bytedance/sonic"
	"github.com/valyala/fasthttp"
)

// DelayResponse wraps RequestJSON with delay-specific fields
type DelayResponse struct {
	*common.RequestJSON
	DurationMs      int64 `json:"duration_ms"`
	ReadDelayMs     int64 `json:"read_delay_ms,omitempty"`      // Delay per chunk during body read
	ReadChunks      int   `json:"read_chunks,omitempty"`        // Number of chunks read
	TotalReadTimeMs int64 `json:"total_read_time_ms,omitempty"` // Total time spent reading body
}

// delayResponsePool is a sync.Pool for DelayResponse objects
// This eliminates allocations on the hot path for response construction
// Each DelayResponse contains an embedded RequestJSON that is reused
var delayResponsePool = sync.Pool{
	New: func() interface{} {
		return &DelayResponse{
			RequestJSON: common.AcquireRequestJSON(),
		}
	},
}

// acquireDelayResponse gets a DelayResponse from the pool
func acquireDelayResponse() *DelayResponse {
	return delayResponsePool.Get().(*DelayResponse)
}

// releaseDelayResponse returns a DelayResponse to the pool after clearing it
// Note: We keep the embedded RequestJSON - just clear its fields via ClearRequestJSON
func releaseDelayResponse(resp *DelayResponse) {
	common.ClearRequestJSON(resp.RequestJSON)
	// Clear delay-specific fields to avoid data leaks between requests
	resp.DurationMs = 0
	resp.ReadDelayMs = 0
	resp.ReadChunks = 0
	resp.TotalReadTimeMs = 0
	delayResponsePool.Put(resp)
}

var (
	pathPrefix    = []byte("/delay/")
	pathPrefixLen = len(pathPrefix)
)

// init initializes the delay handler
func init() {
	// Pre-warm the delayResponsePool by creating initial objects
	// This ensures first requests don't pay the allocation cost
	for i := 0; i < 10; i++ {
		resp := acquireDelayResponse()
		releaseDelayResponse(resp)
	}
}

// Description returns a description of the delay handler for startup logging
func Description() string {
	return "  - /delay/{reply_ms}[/{read_chunk_delay_ms}] -> Delay simulation\n" +
		"      /delay/1000           -> 1s delay before reply, fast read\n" +
		"      /delay/1000/100       -> 1s delay before reply, 100ms delay per 64KB chunk read\n" +
		"      /delay/0/50           -> No reply delay, 50ms delay per chunk (slow consumer)"
}

// delayParams holds parsed delay parameters from the URL path
type delayParams struct {
	replyDelayMs int64 // Delay before sending reply (ms)
	readDelayMs  int64 // Delay per chunk when reading body (ms), 0 = fast read
}

// parseDelayParams extracts and validates delay parameters from the path.
//
// Path formats:
//
//	/delay/{reply_ms}              -> reply delay only, fast read
//	/delay/{reply_ms}/{read_ms}    -> reply delay + slow read (delay per 64KB chunk)
//
// Examples:
//
//	/delay/1000       -> 1000ms reply delay, fast read
//	/delay/1000/100   -> 1000ms reply delay, 100ms per chunk read delay
//	/delay/0/50       -> no reply delay, 50ms per chunk (pure slow consumer)
//
// Accepts any valid non-negative integer as milliseconds.
func parseDelayParams(path []byte) (delayParams, error) {
	// Extract params string after "/delay/"
	if len(path) <= pathPrefixLen {
		return delayParams{}, strconv.ErrSyntax
	}

	paramStr := path[pathPrefixLen:]

	// Check if there's a second parameter (read delay)
	slashIdx := bytes.IndexByte(paramStr, '/')
	if slashIdx == -1 {
		// Single parameter: /delay/{reply_ms}
		replyDelay, err := strconv.ParseInt(common.B2s(paramStr), 10, 64)
		if err != nil || replyDelay < 0 {
			return delayParams{}, strconv.ErrSyntax
		}
		return delayParams{replyDelayMs: replyDelay, readDelayMs: 0}, nil
	}

	// Two parameters: /delay/{reply_ms}/{read_ms}
	replyDelayStr := paramStr[:slashIdx]
	readDelayStr := paramStr[slashIdx+1:]

	replyDelay, err := strconv.ParseInt(common.B2s(replyDelayStr), 10, 64)
	if err != nil || replyDelay < 0 {
		return delayParams{}, strconv.ErrSyntax
	}

	readDelay, err := strconv.ParseInt(common.B2s(readDelayStr), 10, 64)
	if err != nil || readDelay < 0 {
		return delayParams{}, strconv.ErrSyntax
	}

	return delayParams{replyDelayMs: replyDelay, readDelayMs: readDelay}, nil
}

// Handler processes delay requests.
// It can simulate both slow response (delay before reply) and slow consumption
// (delay while reading request body).
//
// URL formats:
//
//	/delay/{reply_ms}              -> delay before reply, fast read
//	/delay/{reply_ms}/{read_ms}    -> delay before reply + slow body read
//
// Examples:
//
//	GET  /delay/1000       -> 1s delay, then reply
//	POST /delay/1000/100   -> read body with 100ms delay per chunk, then 1s delay, then reply
//	POST /delay/0/50       -> read body slowly (50ms/chunk), reply immediately (slow consumer)
func Handler(ctx *fasthttp.RequestCtx) {
	path := ctx.Path()

	// Parse delay parameters from path
	params, err := parseDelayParams(path)
	if err != nil {
		common.SendJSONResponseWithStatus(ctx, fasthttp.StatusBadRequest,
			[]byte(`{"error":"invalid delay parameters","format":"/delay/{reply_ms}[/{read_chunk_delay_ms}]","examples":["/delay/1000","/delay/1000/100","/delay/0/50"]}`))
		return
	}

	// At least one delay must be specified
	if params.replyDelayMs == 0 && params.readDelayMs == 0 {
		common.SendJSONResponseWithStatus(ctx, fasthttp.StatusBadRequest,
			[]byte(`{"error":"at least one delay must be > 0","examples":["/delay/1000","/delay/0/100"]}`))
		return
	}

	// Acquire DelayResponse from pool (includes embedded RequestJSON)
	delayResp := acquireDelayResponse()
	defer releaseDelayResponse(delayResp)

	// Read body using shared function (with optional delay)
	readStats := common.PopulateRequestJSONWithOptions(ctx, delayResp.RequestJSON, common.ReadOptions{
		DelayPerChunkMs: params.readDelayMs,
	})

	// Perform the reply delay
	if params.replyDelayMs > 0 {
		time.Sleep(time.Duration(params.replyDelayMs) * time.Millisecond)
	}

	// Populate delay-specific fields
	delayResp.DurationMs = params.replyDelayMs
	if params.readDelayMs > 0 {
		delayResp.ReadDelayMs = params.readDelayMs
		delayResp.ReadChunks = readStats.ChunksRead
		delayResp.TotalReadTimeMs = readStats.TotalReadTimeMs
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(delayResp)
	if err != nil {
		common.SendJSONResponseWithStatus(ctx, fasthttp.StatusInternalServerError,
			[]byte(`{"error":"failed to marshal response"}`))
		return
	}

	// Send response using centralized helper
	common.SendJSONResponse(ctx, jsonData)
}
