package websocket

import (
	"fasthttp_hpdummy_server/common"
	"log"

	"github.com/fasthttp/websocket"
	"github.com/valyala/fasthttp"
)

const (
	// MaxMessageSize is the maximum WebSocket message size (16 MiB)
	MaxMessageSize = 16 << 20
)

var upgrader = websocket.FastHTTPUpgrader{
	ReadBufferSize:  64 * 1024, // 64KB I/O buffer for better large message performance
	WriteBufferSize: 64 * 1024,
	CheckOrigin: func(ctx *fasthttp.RequestCtx) bool {
		// Allow all origins
		return true
	},
}

// Description returns the endpoint description for startup logging
func Description() string {
	return "  - /ws         -> WebSocket echo server\n  - /ws/close   -> WebSocket server-initiated close test"
}

// Handler handles WebSocket echo connections
func Handler(ctx *fasthttp.RequestCtx) {
	if !ctx.IsGet() {
		ctx.SetStatusCode(fasthttp.StatusMethodNotAllowed)
		ctx.SetBodyString("Only GET requests are allowed for WebSocket\n")
		return
	}

	path := string(ctx.Path())

	// Route to appropriate WebSocket handler
	if path == "/ws/close" {
		err := upgrader.Upgrade(ctx, handleServerCloseConnection)
		if err != nil {
			log.Printf("[WS] upgrade error: %v", err)
			ctx.SetStatusCode(fasthttp.StatusBadRequest)
			ctx.SetBodyString("Not a websocket handshake\n")
		}
		return
	}

	// Default to echo handler for /ws and any other /ws/* paths
	err := upgrader.Upgrade(ctx, handleConnection)
	if err != nil {
		log.Printf("[WS] upgrade error: %v", err)
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBodyString("Not a websocket handshake\n")
	}
}

// handleServerCloseConnection sends a test message then initiates close
func handleServerCloseConnection(conn *websocket.Conn) {
	defer conn.Close()
	conn.SetReadLimit(MaxMessageSize)

	remoteAddr := conn.RemoteAddr().String()
	logConnection("connected (server-close mode)", remoteAddr)

	// Read client's message
	messageType, message, err := conn.ReadMessage()
	if err != nil {
		handleReadError(err)
		return
	}

	logMessage("recv", len(message), remoteAddr)

	// Send response
	response := "Server received: " + string(message)
	if err := conn.WriteMessage(messageType, []byte(response)); err != nil {
		log.Printf("[WS] write error: %v", err)
		return
	}

	logMessage("sent", len(response), remoteAddr)

	// Initiate graceful close with 1000 (normal closure)
	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "server done")
	if err := conn.WriteMessage(websocket.CloseMessage, closeMsg); err != nil {
		log.Printf("[WS] close error: %v", err)
	}

	logConnection("server-initiated close", remoteAddr)
}

// handleConnection manages a single WebSocket connection
func handleConnection(conn *websocket.Conn) {
	defer conn.Close()
	conn.SetReadLimit(MaxMessageSize)

	remoteAddr := conn.RemoteAddr().String()
	logConnection("connected", remoteAddr)

	for {
		if shouldClose(conn, remoteAddr) {
			break
		}

		messageType, message, err := conn.ReadMessage()
		if err != nil {
			handleReadError(err)
			break
		}

		logMessage("recv", len(message), remoteAddr)

		if err := conn.WriteMessage(messageType, message); err != nil {
			log.Printf("[WS] write error: %v", err)
			break
		}
	}

	logConnection("disconnected", remoteAddr)
}

// shouldClose checks if connection should be closed (draining mode)
func shouldClose(conn *websocket.Conn, remoteAddr string) bool {
	if !common.Draining.Load() {
		return false
	}

	logConnection("draining", remoteAddr)
	closeMsg := websocket.FormatCloseMessage(websocket.CloseGoingAway, "Server shutting down")
	if err := conn.WriteMessage(websocket.CloseMessage, closeMsg); err != nil {
		log.Printf("[WS] close error: %v", err)
	}
	return true
}

// handleReadError logs unexpected read errors.
// Only CloseNormalClosure (1000) is expected - all other codes indicate issues:
// - 1001 CloseGoingAway: server/gateway shutting down (expected during deploys)
// - 1006 CloseAbnormalClosure: TCP closed without close frame (indicates bug)
// - 1011 CloseInternalServerErr: gateway/agent error (indicates bug)
func handleReadError(err error) {
	if websocket.IsUnexpectedCloseError(err,
		websocket.CloseNormalClosure,
	) {
		log.Printf("[WS] read error: %v", err)
	}
}

// logConnection logs connection events if not in quiet mode
func logConnection(event, remoteAddr string) {
	if !common.Quiet {
		log.Printf("[WS] %s %s", event, remoteAddr)
	}
}

// logMessage logs message events if not in quiet mode
func logMessage(direction string, size int, remoteAddr string) {
	if !common.Quiet {
		log.Printf("[WS] %s %d bytes %s", direction, size, remoteAddr)
	}
}
