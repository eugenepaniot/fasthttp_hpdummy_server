package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
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
	Myhostname  string            `json:"_myhostname"`
	URI         string            `json:"uri"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers"`
	ContentType string            `json:"content_type"`
	Body        string            `json:"body"`
	Usage       usageStruct       `json:"usage"`
}

var quiet bool
var myhostname string

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
	log.Printf("received signal: %v, initiating graceful shutdown...", receivedSig)

	// Create a context with timeout for graceful shutdown
	// This gives the server up to 60 seconds to close all connections gracefully
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Log the current state before shutdown
	openConns := server.GetOpenConnectionsCount()
	if openConns > 0 {
		log.Printf("gracefully shutting down with %d open connection(s)...", openConns)
	}

	// Gracefully shutdown the server
	// This will:
	// 1. Close all listeners (stop accepting new connections)
	// 2. Add 'Connection: close' header to responses (due to CloseOnShutdown: true)
	// 3. Wait for all active connections to finish their requests or until context timeout
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

func requestToJSON(req *fasthttp.Request) ([]byte, error) {
	// Acquire a requestJSON object from the pool
	// Myhostname and Usage fields are already set to constant values in the pool
	reqJSON := acquireRequestJSON()
	defer releaseRequestJSON(reqJSON)

	// Populate the requestJSON struct with request-specific data only
	reqJSON.URI = b2s(req.URI().FullURI())
	reqJSON.Method = b2s(req.Header.Method())
	reqJSON.ContentType = string(req.Header.ContentType())
	reqJSON.Body = string(req.Body())

	// Populate headers map (reusing the existing map from pool)
	for k, v := range req.Header.All() {
		reqJSON.Headers[b2s(k)] = b2s(v)
	}

	// Marshal to JSON and return
	// Note: The marshaled data is a copy, so it's safe to release reqJSON after this
	return json.Marshal(reqJSON)
}

func requestHandler(ctx *fasthttp.RequestCtx) {
	jsonData, _ := requestToJSON(&ctx.Request)

	if !quiet {
		fmt.Println(b2s(jsonData))
	}

	ctx.SetContentType("application/json")
	ctx.Response.Header.SetContentLength(len(jsonData))
	ctx.Response.Header.Set("Connection", "keep-alive")
	ctx.SetStatusCode(fasthttp.StatusOK)
	if _, err := ctx.Write(jsonData); err != nil {
		log.Printf("error writing response: %v", err)
	}
}

func b2s(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}
