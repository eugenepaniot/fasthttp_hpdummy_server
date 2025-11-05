package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	json "github.com/bytedance/sonic"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/tcplisten"
)

type usageStruct struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	InputTokens      int `json:"input_tokens,omitempty"`
	OutputTokens     int `json:"output_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type requestJSON struct {
	Myhostname      string            `json:"_myhostname"`
	URI             string            `json:"uri"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers"`
	ContentType     string            `json:"content_type"`
	Body            string            `json:"body"`
	Usage           usageStruct       `json:"usage"`
	SourceAddr      string            `json:"source_addr"`      // Client IP:PORT (RemoteAddr)
	DestinationAddr string            `json:"destination_addr"` // Server IP:PORT (LocalAddr)
}

var quiet bool
var myhostname string
var draining atomic.Bool // Flag to indicate server is draining (set when SIGTERM received)

var requestJSONPool sync.Pool

// initRequestJSONPool initializes the pool after myhostname is set
func initRequestJSONPool() {
	requestJSONPool = sync.Pool{
		New: func() interface{} {
			return &requestJSON{
				Headers: make(map[string]string, 16), // Pre-allocate with reasonable capacity
				// Set constant values once during initialization
				Myhostname: myhostname,
				Usage: usageStruct{
					PromptTokens:     1,
					CompletionTokens: 2,
					InputTokens:      100,
					OutputTokens:     200,
					TotalTokens:      300,
				},
			}
		},
	}
}

// acquireRequestJSON gets a requestJSON object from the pool
func acquireRequestJSON() *requestJSON {
	return requestJSONPool.Get().(*requestJSON)
}

// releaseRequestJSON resets and returns the requestJSON object to the pool
func releaseRequestJSON(reqJSON *requestJSON) {
	// Reset only variable fields to zero values to prevent memory leaks
	// Note: Myhostname and Usage fields are constant and don't need to be reset
	reqJSON.URI = ""
	reqJSON.Method = ""
	reqJSON.ContentType = ""
	reqJSON.Body = ""
	reqJSON.SourceAddr = ""
	reqJSON.DestinationAddr = ""

	// Clear the map but keep the underlying storage for reuse
	for k := range reqJSON.Headers {
		delete(reqJSON.Headers, k)
	}

	requestJSONPool.Put(reqJSON)
}

func main() {
	var err error

	flag.BoolVar(&quiet, "quiet", true, "quiet")
	addr := flag.String("addr", "0.0.0.0:8080", "server listen address")
	pidfile := flag.String("pidfile", "fasthttp_hpdummy_server.pid", "path to PID file")
	flag.Parse()

	if myhostname, err = os.Hostname(); err != nil {
		log.Fatalf("error getting hostname: %v", err)
	}

	// Initialize the pool after myhostname is set (it's a constant value)
	initRequestJSONPool()

	cfg := tcplisten.Config{
		ReusePort: true,
		FastOpen:  true,
	}
	ln, err := cfg.NewListener("tcp4", *addr)
	if err != nil {
		log.Fatalf("error creating listener: %v", err)
	}
	defer ln.Close()

	// Write PID file
	// The PID file contains the process ID and is used for process management
	// It allows external tools to identify and signal this server instance
	pid := os.Getpid()
	pidContent := fmt.Sprintf("%d\n", pid)
	if err := os.WriteFile(*pidfile, []byte(pidContent), 0644); err != nil {
		log.Fatalf("error writing PID file: %v", err)
	}
	log.Printf("wrote PID %d to %s", pid, *pidfile)

	// Ensure PID file is removed on exit
	// This cleanup happens regardless of how the program exits (normal or error)
	defer func() {
		if err := os.Remove(*pidfile); err != nil {
			log.Printf("error removing PID file: %v", err)
		} else {
			log.Printf("removed PID file: %s", *pidfile)
		}
	}()

	// Create a new fasthttp server
	server := &fasthttp.Server{
		TCPKeepalive:    true,
		LogAllErrors:    true,
		ReadTimeout:     90 * time.Second,
		WriteTimeout:    5 * time.Second,
		IdleTimeout:     10 * time.Second, // Close idle keep-alive connections after 10s
		Handler:         requestHandler,
		CloseOnShutdown: true, // Add 'Connection: close' header during shutdown
	}

	// Start the server in a goroutine
	go func() {
		log.Printf("starting server on %s", *addr)
		if err := server.Serve(ln); err != nil {
			log.Fatalf("error starting server: %v", err)
		}
	}()

	// Wait for a signal to stop the server
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	receivedSig := <-sig
	log.Printf("received signal: %v, entering draining mode...", receivedSig)

	draining.Store(true)

	// Give idle keepalive connections time to make one more request
	// and receive Connection: close header before shutdown
	gracePeriod := 1 * time.Second
	log.Printf("waiting %v for idle keepalive connections to drain...", gracePeriod)
	time.Sleep(gracePeriod)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer shutdownCancel()

	// Log the current state before shutdown
	openConns := server.GetOpenConnectionsCount()
	if openConns > 0 {
		log.Printf("gracefully shutting down with %d open connection(s)...", openConns)
	}

	if err := server.ShutdownWithContext(shutdownCtx); err != nil {
		if err == context.DeadlineExceeded {
			log.Printf("shutdown timeout exceeded, forcing close of %d remaining connection(s)", server.GetOpenConnectionsCount())
		} else {
			log.Printf("error during graceful shutdown: %v", err)
		}
	} else {
		log.Printf("all connections closed gracefully")
	}

	log.Printf("server stopped. bye bye!")
}

func requestToJSON(ctx *fasthttp.RequestCtx) ([]byte, error) {
	// Acquire a requestJSON object from the pool
	// Myhostname and Usage fields are already set to constant values in the pool
	reqJSON := acquireRequestJSON()
	defer releaseRequestJSON(reqJSON)

	req := &ctx.Request

	// Populate the requestJSON struct with request-specific data only
	reqJSON.URI = b2s(req.URI().FullURI())
	reqJSON.Method = b2s(req.Header.Method())
	reqJSON.ContentType = string(req.Header.ContentType())
	reqJSON.Body = string(req.Body())

	reqJSON.SourceAddr = ctx.RemoteAddr().String()     // Client IP:PORT
	reqJSON.DestinationAddr = ctx.LocalAddr().String() // Server IP:PORT

	// Populate headers map (reusing the existing map from pool)
	for k, v := range req.Header.All() {
		reqJSON.Headers[b2s(k)] = b2s(v)
	}

	// Marshal to JSON and return
	// Note: The marshaled data is a copy, so it's safe to release reqJSON after this
	return json.Marshal(reqJSON)
}

func requestHandler(ctx *fasthttp.RequestCtx) {
	jsonData, _ := requestToJSON(ctx)

	ctx.SetContentType("application/json")
	ctx.Response.Header.SetContentLength(len(jsonData))

	// Check if server is draining (pod received SIGTERM)
	// If draining, tell client not to reuse this connection (zero-downtime upgrade)
	if draining.Load() {
		ctx.Response.Header.Set("Connection", "close")
		if !quiet {
			log.Printf("sent Connection: close (draining mode)")
		}
	} else {
		ctx.Response.Header.Set("Connection", "keep-alive")
	}

	ctx.SetStatusCode(fasthttp.StatusOK)
	if _, err := ctx.Write(jsonData); err != nil {
		log.Printf("error writing response: %v", err)
	}

	if !quiet {
		log.Printf("response: %v", &ctx.Response)
	}
}

func b2s(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}
