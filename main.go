package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
	"unsafe"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/reuseport"
)

type requestJSON struct {
	URI         string            `json:"uri"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers"`
	ContentType string            `json:"content_type"`
	Body        string            `json:"body"`
}

var quiet bool

func main() {
	flag.BoolVar(&quiet, "quiet", false, "quiet")
	addr := flag.String("addr", "0.0.0.0:8080", "server listen address")
	flag.Parse()

	// Create a new listener on the given address using port reuse
	ln, err := reuseport.Listen("tcp4", *addr)
	if err != nil {
		log.Fatalf("error creating listener: %v", err)
	}
	defer ln.Close()

	// Create a new fasthttp server
	server := &fasthttp.Server{
		TCPKeepalive:    true,
		LogAllErrors:    true,
		ReadBufferSize:  1024 * 1024,
		WriteBufferSize: 1024 * 1024,
		ReadTimeout:     90 * time.Second,
		WriteTimeout:    5 * time.Second,
		Handler:         requestHandler,
	}

	// Start the server in a goroutine
	go func() {
		if err := server.Serve(ln); err != nil {
			log.Fatalf("error starting server: %v", err)
		}
	}()

	// Wait for a signal to stop the server
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	// Stop the server
	server.Shutdown()
}

func requestToJSON(req *fasthttp.Request) ([]byte, error) {
	// Get the request URI, method, headers, content type, and body
	uri := b2s(req.URI().FullURI())
	method := b2s(req.Header.Method())
	headers := make(map[string]string)
	req.Header.VisitAll(func(k, v []byte) {
		headers[string(k)] = string(v)
	})
	contentType := string(req.Header.ContentType())
	body := string(req.Body())

	// Create a requestJSON struct and marshal it to JSON
	reqJSON := &requestJSON{
		URI:         uri,
		Method:      method,
		Headers:     headers,
		ContentType: contentType,
		Body:        body,
	}
	return json.Marshal(reqJSON)
}

func requestHandler(ctx *fasthttp.RequestCtx) {
	jsonData, _ := requestToJSON(&ctx.Request)

	if !quiet {
		fmt.Println(b2s(jsonData))
	}

	ctx.SetContentType("application/json")
	ctx.Response.Header.SetContentLength(len(jsonData))
	// ctx.Response.Header.Set("Connection", "keep-alive")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.Write(jsonData)
}

func b2s(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}
