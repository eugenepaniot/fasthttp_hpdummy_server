package websocket

import (
	"fasthttp_hpdummy_server/common"
	"log"

	"github.com/fasthttp/websocket"
	"github.com/valyala/fasthttp"
)

var upgrader = websocket.FastHTTPUpgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(ctx *fasthttp.RequestCtx) bool {
		// Allow all origins
		return true
	},
}

// Description returns the endpoint description for startup logging
func Description() string {
	return "  - /ws         -> WebSocket echo server"
}

// Handler handles WebSocket echo connections
func Handler(ctx *fasthttp.RequestCtx) {
	if !ctx.IsGet() {
		ctx.SetStatusCode(fasthttp.StatusMethodNotAllowed)
		ctx.SetBodyString("Only GET requests are allowed for WebSocket\n")
		return
	}

	err := upgrader.Upgrade(ctx, handleConnection)
	if err != nil {
		log.Printf("[WS] upgrade error: %v", err)
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBodyString("Not a websocket handshake\n")
	}
}

// handleConnection manages a single WebSocket connection
func handleConnection(conn *websocket.Conn) {
	defer conn.Close()

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

// handleReadError logs unexpected read errors
func handleReadError(err error) {
	if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
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
