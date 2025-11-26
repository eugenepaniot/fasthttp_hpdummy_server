package common

import (
	"log"
	"strconv"
	"strings"
	"unsafe"

	"github.com/valyala/fasthttp"
)

// B2s converts a byte slice to string without memory allocation
// This is a zero-copy conversion using unsafe pointer manipulation
// WARNING: The returned string shares the same underlying memory as the byte slice
// Do not modify the original byte slice after this conversion
func B2s(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// GetIntQueryParam parses an integer query parameter from the request
// Returns the parsed value if valid and positive, otherwise returns the default value
// Uses zero-copy B2s conversion for efficient parsing
func GetIntQueryParam(ctx *fasthttp.RequestCtx, paramName string, defaultValue int64) int64 {
	if paramBytes := ctx.QueryArgs().Peek(paramName); len(paramBytes) > 0 {
		if parsed, err := strconv.ParseInt(B2s(paramBytes), 10, 64); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultValue
}

// GetBoolQueryParam parses a boolean query parameter from the request
// Returns true if the parameter exists and is "true", "1", "yes", or "on" (case-insensitive)
// Returns false if the parameter is absent, empty, or any other value
// Uses zero-copy B2s conversion for efficient parsing
func GetBoolQueryParam(ctx *fasthttp.RequestCtx, paramName string) bool {
	if paramBytes := ctx.QueryArgs().Peek(paramName); len(paramBytes) > 0 {
		val := B2s(paramBytes)
		// Case-insensitive comparison for common boolean values
		switch strings.ToLower(val) {
		case "true", "1", "yes", "on":
			return true
		}
	}
	return false
}

// Static byte slices for commonly used header values
// These are reused across all handlers to avoid allocations
var (
	// Connection headers
	strConnection = []byte("Connection")
	strClose      = []byte("close")
	strKeepAlive  = []byte("keep-alive")

	// Content-Type values
	ContentTypeApplicationJSON = []byte("application/json")
)

// SetConnectionHeader sets Connection header based on draining state
// This centralizes the connection header logic and uses byte constants for performance
func SetConnectionHeader(ctx *fasthttp.RequestCtx) {
	if Draining.Load() {
		ctx.Response.Header.SetBytesKV(strConnection, strClose)
	} else {
		ctx.Response.Header.SetBytesKV(strConnection, strKeepAlive)
	}
}

// SendJSONResponse sends a JSON response with standard headers and 200 OK status
// This is a convenience wrapper for SendJSONResponseWithStatus with 200 OK
func SendJSONResponse(ctx *fasthttp.RequestCtx, jsonData []byte) {
	SendJSONResponseWithStatus(ctx, fasthttp.StatusOK, jsonData)
}

// SendJSONResponseWithStatus sends a JSON response with custom status code
// This centralizes the common response pattern used by all JSON handlers
func SendJSONResponseWithStatus(ctx *fasthttp.RequestCtx, statusCode int, jsonData []byte) {
	ctx.Response.Header.SetContentTypeBytes(ContentTypeApplicationJSON)
	ctx.Response.Header.SetContentLength(len(jsonData))
	SetConnectionHeader(ctx)
	ctx.SetStatusCode(statusCode)
	ctx.SetBody(jsonData)

	if !Quiet {
		log.Printf("[HTTP] %d %s", statusCode, FormatRequestLog(ctx))
	}
}

// FormatRequestLog formats request details for logging (excludes body content)
func FormatRequestLog(ctx *fasthttp.RequestCtx) string {
	req := &ctx.Request

	var sb strings.Builder
	sb.Grow(512) // Pre-allocate reasonable size

	sb.WriteString("method=")
	sb.WriteString(B2s(req.Header.Method()))
	sb.WriteString(" uri=")
	sb.WriteString(B2s(req.URI().FullURI()))
	sb.WriteString(" body_size=")
	sb.WriteString(strconv.Itoa(len(req.Body())))
	sb.WriteString(" src=")
	sb.WriteString(ctx.RemoteAddr().String())
	sb.WriteString(" dst=")
	sb.WriteString(ctx.LocalAddr().String())

	return sb.String()
}
