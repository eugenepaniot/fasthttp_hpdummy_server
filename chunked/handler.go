package chunked

import (
	"fasthttp_hpdummy_server/common"
	"log"
	"strconv"

	"github.com/valyala/fasthttp"
)

const (
	defaultChunkSize = 1024 // Default: 1KB per chunk
)

// Static byte slices for commonly used strings to avoid allocations
var (
	strTextPlain  = []byte("text/plain; charset=utf-8")
	strBadRequest = []byte("Please specify chunk count: /chunked/10?size=1024&delay=100\n")
	strInvalid    = []byte("Invalid chunk count. Must be a positive integer\n")
	strEmptyCount = []byte("Chunk count cannot be empty\n")
	chunkedPrefix = []byte("/chunked/")
)

// Description returns the endpoint description for startup logging
func Description() string {
	return "  - /chunked/{count} -> Chunked response (e.g., /chunked/10?size=1024&delay=100)"
}

// Handler handles chunked response generation
// Supports URLs like /chunked/5, /chunked/10?size=2048&delay=100, etc.
// Query parameters:
//   - size: bytes per chunk (optional, default: 1024)
//   - delay: milliseconds to wait between chunks (optional, default: 0)
//
// Optimized for high performance with minimal allocations
func Handler(ctx *fasthttp.RequestCtx) {
	path := ctx.Path()

	// Extract chunk count without allocations
	if len(path) <= len(chunkedPrefix) {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		_, _ = ctx.Write(strBadRequest)
		return
	}

	// Get chunk count substring
	countBytes := path[len(chunkedPrefix):]

	count, err := parseChunkCount(countBytes)
	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		_, _ = ctx.Write(err)
		return
	}

	// Parse optional chunk size query parameter
	chunkSize := int(common.GetIntQueryParam(ctx, "size", defaultChunkSize))

	// Parse optional delay query parameter
	delayMs := common.GetIntQueryParam(ctx, "delay", 0)

	// Set response headers
	ctx.Response.Header.SetContentTypeBytes(strTextPlain)
	common.SetConnectionHeader(ctx)
	ctx.SetStatusCode(fasthttp.StatusOK)

	// Stream the response with chunked encoding
	// Flush after each chunk for immediate delivery, with optional delays
	totalSize := int64(count * chunkSize)
	common.StreamResponse(ctx, totalSize, chunkSize, delayMs, true, "[CHUNKED]")

	if !common.Quiet {
		if delayMs > 0 {
			log.Printf("[CHUNKED] %d chunks × %d bytes (%d total) delay=%dms %s",
				count, chunkSize, totalSize, delayMs, common.FormatRequestLog(ctx))
		} else {
			log.Printf("[CHUNKED] %d chunks × %d bytes (%d total) %s",
				count, chunkSize, totalSize, common.FormatRequestLog(ctx))
		}
	}
}

// parseChunkCount parses chunk count from path
// Only accepts positive integers
func parseChunkCount(countBytes []byte) (int, []byte) {
	if len(countBytes) < 1 {
		return 0, strEmptyCount
	}

	// Parse as integer
	count, err := strconv.Atoi(common.B2s(countBytes))
	if err != nil || count <= 0 {
		return 0, strInvalid
	}

	return count, nil
}
