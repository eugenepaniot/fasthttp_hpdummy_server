package router

import (
	"bytes"
	"fasthttp_hpdummy_server/binary"
	"fasthttp_hpdummy_server/chunked"
	"fasthttp_hpdummy_server/common"
	"fasthttp_hpdummy_server/delay"
	"fasthttp_hpdummy_server/echo"
	"fasthttp_hpdummy_server/status"
	"fasthttp_hpdummy_server/upload"
	"fasthttp_hpdummy_server/websocket"

	"github.com/valyala/fasthttp"
)

// Path constants as byte slices to avoid runtime conversions
// These are allocated once at package initialization
var (
	pathRoot       = []byte("/")
	pathBinPfx     = []byte("/bin/")
	pathDelayPfx   = []byte("/delay/")
	pathStatusPfx  = []byte("/status/")
	pathChunkedPfx = []byte("/chunked/")
	pathWS         = []byte("/ws")
	pathUpload     = []byte("/upload")
	pathHealth     = []byte("/health")
	pathHelp       = []byte("/help")
)

// Router handles path-based routing for different server functionalities
type Router struct {
	helpResponse []byte
}

// NewRouter creates a new unified router instance
func NewRouter() *Router {
	r := &Router{}
	r.buildHelpResponse()
	return r
}

// buildHelpResponse constructs help text from handler descriptions
func (r *Router) buildHelpResponse() {
	r.helpResponse = []byte("Available endpoints:\n" +
		echo.Description() + "\n" +
		"  - /health      -> Health check (returns {\"status\":\"ok\"})\n" +
		"  - /help        -> This help message\n" +
		binary.Description() + "\n" +
		chunked.Description() + "\n" +
		delay.Description() + "\n" +
		status.Description() + "\n" +
		upload.Description() + "\n" +
		websocket.Description())
}

// Static response for health check
var healthResponse = []byte(`{"status":"ok"}`)

// Handler is the main request handler that routes to appropriate sub-handlers
func (r *Router) Handler(ctx *fasthttp.RequestCtx) {
	path := ctx.Path()

	// Route to appropriate handlers based on path
	if bytes.Equal(path, pathRoot) {
		echo.Handler(ctx)
		return
	}

	if bytes.HasPrefix(path, pathBinPfx) {
		binary.Handler(ctx)
		return
	}

	if bytes.HasPrefix(path, pathChunkedPfx) {
		chunked.Handler(ctx)
		return
	}

	if bytes.HasPrefix(path, pathDelayPfx) {
		delay.Handler(ctx)
		return
	}

	if bytes.HasPrefix(path, pathStatusPfx) {
		status.Handler(ctx)
		return
	}

	if bytes.Equal(path, pathUpload) {
		upload.Handler(ctx)
		return
	}

	if bytes.HasPrefix(path, pathWS) {
		websocket.Handler(ctx)
		return
	}

	if bytes.Equal(path, pathHealth) {
		ctx.SetStatusCode(200)
		ctx.Response.Header.SetContentTypeBytes(common.ContentTypeApplicationJSON)
		ctx.SetBody(healthResponse)
		return
	}

	if bytes.Equal(path, pathHelp) {
		ctx.SetStatusCode(200)
		ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
		ctx.SetBody(r.helpResponse)
		return
	}

	// Default to echo handler
	echo.Handler(ctx)
}
