package status

import (
	"fasthttp_hpdummy_server/common"
	"strconv"
	"sync"

	json "github.com/bytedance/sonic"
	"github.com/valyala/fasthttp"
)

// StatusResponse wraps RequestJSON with status-specific fields
type StatusResponse struct {
	*common.RequestJSON
	StatusCode    int    `json:"status_code"`
	StatusMessage string `json:"status_message"`
}

// statusResponsePool is a sync.Pool for StatusResponse objects
var statusResponsePool = sync.Pool{
	New: func() interface{} {
		return &StatusResponse{
			RequestJSON: common.AcquireRequestJSON(),
		}
	},
}

// acquireStatusResponse gets a StatusResponse from the pool
func acquireStatusResponse() *StatusResponse {
	return statusResponsePool.Get().(*StatusResponse)
}

// releaseStatusResponse returns a StatusResponse to the pool after clearing it
func releaseStatusResponse(resp *StatusResponse) {
	common.ClearRequestJSON(resp.RequestJSON)
	resp.StatusCode = 0
	resp.StatusMessage = ""
	statusResponsePool.Put(resp)
}

var (
	pathPrefix    = []byte("/status/")
	pathPrefixLen = len(pathPrefix)
)

// init initializes the status handler
func init() {
	// Pre-warm the statusResponsePool
	for i := 0; i < 10; i++ {
		resp := acquireStatusResponse()
		releaseStatusResponse(resp)
	}
}

// Static error messages as byte slices to avoid allocations
var (
	strBadRequest   = []byte(`{"error":"invalid status code - must be a valid HTTP status code","example":"/status/404"}`)
	strInvalidRange = []byte(`{"error":"status code must be between 100 and 599","example":"/status/200"}`)
	strMissingCode  = []byte(`{"error":"status code is required","example":"/status/404"}`)
)

// Description returns a description of the status handler for startup logging
func Description() string {
	return "  - /status/{code} -> Return specific HTTP status code (e.g., /status/404)"
}

// parseStatusCode extracts and validates the status code from the path.
// Returns the status code, or 0 if invalid.
//
// Path format: /status/{code}
// Examples:
//
//	/status/200 -> 200 (OK)
//	/status/404 -> 404 (Not Found)
//	/status/500 -> 500 (Internal Server Error)
//
// Accepts any valid HTTP status code (100-599).
func parseStatusCode(path []byte) (int, error) {
	// Extract status code string after "/status/"
	if len(path) <= pathPrefixLen {
		return 0, strconv.ErrSyntax
	}

	codeStr := common.B2s(path[pathPrefixLen:])

	// Parse as integer
	code, err := strconv.Atoi(codeStr)
	if err != nil {
		return 0, err
	}

	// Validate range (HTTP status codes are 100-599)
	if code < 100 || code > 599 {
		return 0, strconv.ErrRange
	}

	return code, nil
}

// Handler processes status code requests.
// It returns a response with the specified HTTP status code.
//
// Request:  GET /status/404
// Response: HTTP 404 with JSON body including request details and status info
func Handler(ctx *fasthttp.RequestCtx) {
	path := ctx.Path()

	// Parse status code from path
	statusCode, err := parseStatusCode(path)
	if err != nil {
		if len(path) <= pathPrefixLen {
			common.SendJSONResponseWithStatus(ctx, fasthttp.StatusBadRequest, strMissingCode)
		} else if err == strconv.ErrRange {
			common.SendJSONResponseWithStatus(ctx, fasthttp.StatusBadRequest, strInvalidRange)
		} else {
			common.SendJSONResponseWithStatus(ctx, fasthttp.StatusBadRequest, strBadRequest)
		}
		return
	}

	// Build response JSON with status info
	jsonData, err := buildResponseJSON(ctx, statusCode)
	if err != nil {
		common.SendJSONResponseWithStatus(ctx, fasthttp.StatusInternalServerError,
			[]byte(`{"error":"failed to marshal response"}`))
		return
	}

	// Send response using centralized helper with custom status code
	common.SendJSONResponseWithStatus(ctx, statusCode, jsonData)
}

// buildResponseJSON creates the JSON response including status code and message
// Uses pooled StatusResponse struct (with embedded RequestJSON) to minimize allocations
func buildResponseJSON(ctx *fasthttp.RequestCtx, statusCode int) ([]byte, error) {
	// Acquire StatusResponse from pool (includes embedded RequestJSON)
	statusResp := acquireStatusResponse()
	defer releaseStatusResponse(statusResp)

	// Populate request data using shared function
	common.PopulateRequestJSON(ctx, statusResp.RequestJSON)

	// Populate status-specific fields
	statusResp.StatusCode = statusCode
	statusResp.StatusMessage = fasthttp.StatusMessage(statusCode)

	// Marshal to JSON and return
	return json.Marshal(statusResp)
}
