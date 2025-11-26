package delay

import (
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
	DurationMs int64 `json:"duration_ms"`
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
// Note: DurationMs is not cleared - it's always overwritten before use
func releaseDelayResponse(resp *DelayResponse) {
	common.ClearRequestJSON(resp.RequestJSON)
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
	return "  - /delay/{ms} -> Delay simulation (e.g., /delay/1000 for 1s delay)"
}

// parseDuration extracts and validates the delay duration from the path.
// Returns duration in milliseconds, or 0 if invalid.
//
// Path format: /delay/{duration_ms}
// Examples:
//
//	/delay/100  -> 100ms
//	/delay/1000 -> 1000ms (1 second)
//	/delay/5555 -> 5555ms (5.555 seconds)
//
// Accepts any valid positive integer as milliseconds.
func parseDuration(path []byte) (int64, error) {
	// Extract duration string after "/delay/"
	if len(path) <= pathPrefixLen {
		return 0, strconv.ErrSyntax
	}

	durationStr := common.B2s(path[pathPrefixLen:])

	// Parse as integer (supports any positive value)
	duration, err := strconv.ParseInt(durationStr, 10, 64)
	if err != nil || duration <= 0 {
		return 0, strconv.ErrSyntax
	}

	return duration, nil
}

// Handler processes delay requests.
// It delays for the specified duration and returns a JSON response with timing info.
//
// Request:  GET /delay/1000
// Response: {"duration_ms": 1000, "message": "delayed for 1000ms", ...}
func Handler(ctx *fasthttp.RequestCtx) {
	path := ctx.Path()

	// Parse duration from path
	durationMs, err := parseDuration(path)
	if err != nil || durationMs <= 0 {
		common.SendJSONResponseWithStatus(ctx, fasthttp.StatusBadRequest,
			[]byte(`{"error":"invalid delay duration - must be a positive integer (milliseconds)","example":"/delay/1000"}`))
		return
	}

	// Perform the delay
	time.Sleep(time.Duration(durationMs) * time.Millisecond)

	// Build response JSON
	jsonData, err := buildResponseJSON(ctx, durationMs)
	if err != nil {
		common.SendJSONResponseWithStatus(ctx, fasthttp.StatusInternalServerError,
			[]byte(`{"error":"failed to marshal response"}`))
		return
	}

	// Send response using centralized helper
	common.SendJSONResponse(ctx, jsonData)
}

// buildResponseJSON creates the JSON response including delay duration info
// Uses pooled DelayResponse struct (with embedded RequestJSON) to minimize allocations
func buildResponseJSON(ctx *fasthttp.RequestCtx, durationMs int64) ([]byte, error) {
	// Acquire DelayResponse from pool (includes embedded RequestJSON)
	delayResp := acquireDelayResponse()
	defer releaseDelayResponse(delayResp)

	// Populate request data using shared function
	common.PopulateRequestJSON(ctx, delayResp.RequestJSON)

	// Populate delay-specific fields
	delayResp.DurationMs = durationMs

	// Marshal to JSON and return
	// Note: The marshaled data is a copy, so it's safe to release delayResp after this
	return json.Marshal(delayResp)
}
