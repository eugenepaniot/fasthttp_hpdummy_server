package binary

import (
	"fasthttp_hpdummy_server/common"
	"log"
	"strconv"

	"github.com/valyala/fasthttp"
)

// Description returns the endpoint description for startup logging
func Description() string {
	return "  - /bin/{size} -> Binary response (1K, 10M, 1G, 10000G or any byte count like 11111) ?chunked=true for chunked encoding"
}

// Static byte slices for commonly used strings to avoid allocations
var (
	strOctetStream = []byte("application/octet-stream")
	strContentDisp = []byte("Content-Disposition")
	strAttachment  = []byte("attachment; filename=\"data.bin\"")
	strBadRequest  = []byte("Please specify size: /bin/1M, /bin/10M, /bin/10000G or /bin/11111, etc.\n")
	strInvalid     = []byte("Invalid size. Supported: K, M, G, T suffixes (e.g., 1K, 10M, 1G, 10000G) or any positive integer\n")
	strEmptySize   = []byte("Size cannot be empty\n")
	binPrefix      = []byte("/bin/")
)

var commonSizes = map[string]int64{
	// Kilobytes
	"1K": 1024, "1k": 1024,
	"10K": 10 * 1024, "10k": 10 * 1024,
	"100K": 100 * 1024, "100k": 100 * 1024,
	// Megabytes
	"1M": 1024 * 1024, "1m": 1024 * 1024,
	"10M": 10 * 1024 * 1024, "10m": 10 * 1024 * 1024,
	"100M": 100 * 1024 * 1024, "100m": 100 * 1024 * 1024,
	"500M": 500 * 1024 * 1024, "500m": 500 * 1024 * 1024,
	// Gigabytes
	"1G": 1024 * 1024 * 1024, "1g": 1024 * 1024 * 1024,
	"10G": 10 * 1024 * 1024 * 1024, "10g": 10 * 1024 * 1024 * 1024,
	"100G": 100 * 1024 * 1024 * 1024, "100g": 100 * 1024 * 1024 * 1024,
	"1000G": 1000 * 1024 * 1024 * 1024, "1000g": 1000 * 1024 * 1024 * 1024,
}

// Handler handles binary response generation
// Supports URLs like /bin/1K, /bin/1M, /bin/10M, /bin/11111, etc.
// Optimized for high performance with minimal allocations
func Handler(ctx *fasthttp.RequestCtx) {
	path := ctx.Path()

	// Extract size string without allocations
	if len(path) <= len(binPrefix) {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		_, _ = ctx.Write(strBadRequest)
		return
	}

	// Get size substring without allocation by using slicing
	sizeBytes := path[len(binPrefix):]

	size, err := parseSize(sizeBytes)
	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		_, _ = ctx.Write(err)
		return
	}

	// Set response headers
	ctx.Response.Header.SetContentTypeBytes(strOctetStream)
	ctx.Response.Header.SetBytesKV(strContentDisp, strAttachment)
	common.SetConnectionHeader(ctx)
	ctx.SetStatusCode(fasthttp.StatusOK)

	// Check if chunked transfer encoding is requested for testing
	useChunked := common.GetBoolQueryParam(ctx, "chunked")

	// Use buffer pool's chunk size for optimal performance
	chunkSize := common.BinaryBufferPool.ChunkSize()

	if useChunked {
		// Chunked transfer encoding for testing proxy behavior
		common.StreamResponse(ctx, size, chunkSize, 0, false, "[BIN]")
	} else {
		// Content-Length mode for maximum performance
		common.StreamResponseWithContentLength(ctx, size, chunkSize, "[BIN]")
	}

	if !common.Quiet {
		log.Printf("[BIN] %d bytes %s", size, common.FormatRequestLog(ctx))
	}
}

// parseSize parses size byte slices like "1K", "10M", "1G", "10000G" or raw bytes like "11111"
// First checks pre-cached common sizes for maximum performance
// Then parses sizes with suffixes (K, M, G, T)
// Falls back to parsing as raw integer for arbitrary byte counts
// Returns error if format is invalid or value is non-positive
func parseSize(sizeBytes []byte) (int64, []byte) {
	if len(sizeBytes) < 1 {
		return 0, strEmptySize
	}

	sizeStr := common.B2s(sizeBytes)

	// Fast path: check pre-cached common sizes (1K, 10M, 1G, etc.)
	if cached, ok := commonSizes[sizeStr]; ok {
		return cached, nil
	}

	// Parse sizes with suffixes (K, M, G, T)
	// Examples: 10000G, 500M, 2048K
	if len(sizeStr) > 1 {
		lastChar := sizeStr[len(sizeStr)-1]
		var multiplier int64

		switch lastChar {
		case 'K', 'k':
			multiplier = 1024
		case 'M', 'm':
			multiplier = 1024 * 1024
		case 'G', 'g':
			multiplier = 1024 * 1024 * 1024
		case 'T', 't':
			multiplier = 1024 * 1024 * 1024 * 1024
		}

		if multiplier > 0 {
			// Parse the numeric part before the suffix
			numStr := sizeStr[:len(sizeStr)-1]
			if num, err := strconv.ParseInt(numStr, 10, 64); err == nil && num > 0 {
				return num * multiplier, nil
			}
			return 0, strInvalid
		}
	}

	// Fallback: parse as raw integer (e.g., "11111" -> 11111 bytes)
	// This allows arbitrary byte counts for flexible testing
	if size, err := strconv.ParseInt(sizeStr, 10, 64); err == nil && size > 0 {
		return size, nil
	}

	// Invalid format - not a known size and not a valid integer
	return 0, strInvalid
}
