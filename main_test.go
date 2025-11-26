package main

import (
	"bytes"
	"encoding/json"
	"fasthttp_hpdummy_server/common"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/valyala/fasthttp"

	// Import handler packages to trigger their init() functions
	_ "fasthttp_hpdummy_server/binary"
	_ "fasthttp_hpdummy_server/chunked"
	_ "fasthttp_hpdummy_server/delay"
	_ "fasthttp_hpdummy_server/echo"
	_ "fasthttp_hpdummy_server/status"
	_ "fasthttp_hpdummy_server/upload"
)

// setupTestServer initializes handlers and creates a real TCP test server
func setupTestServer(t *testing.T) (string, *fasthttp.Client, func()) {
	// Set hostname for tests
	common.Myhostname = "test-host"
	common.Quiet = true

	// Initialize buffer pool with default test size (256KB)
	common.InitBinaryBufferPool(256 * 1024)

	// Listen on random available port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	addr := ln.Addr().String()

	// Create server using shared function
	server := NewServer(256 * 1024)

	// Start server in background
	go func() {
		if err := server.Serve(ln); err != nil {
			t.Logf("server error: %v", err)
		}
	}()

	// Create HTTP client with streaming support for large responses
	client := &fasthttp.Client{
		MaxConnsPerHost:     1,
		MaxResponseBodySize: 0,    // Unlimited response body size for testing large responses
		StreamResponseBody:  true, // Enable streaming to avoid buffering entire response
	}

	// Cleanup function
	cleanup := func() {
		server.Shutdown()
		ln.Close()
	}

	return addr, client, cleanup
}

// getResponseBody reads the response body and returns both content and size
// For streaming responses, it reads in chunks and buffers up to maxBufferSize (1MB)
// For non-streaming responses, it returns the body content directly
// Returns: (body content up to 1MB, actual total size, error)
// This helper avoids memory issues when testing large responses while still
// allowing content verification for smaller responses
func getResponseBody(resp *fasthttp.Response) ([]byte, int64, error) {
	const maxBufferSize = 1024 * 1024 // 1MB content buffer limit

	if resp.IsBodyStream() {
		// For streaming responses, read from body stream
		bodyStream := resp.BodyStream()

		// Read first 1MB for content verification
		contentBuf := make([]byte, maxBufferSize)
		totalRead := 0
		for totalRead < maxBufferSize {
			n, err := bodyStream.Read(contentBuf[totalRead:])
			totalRead += n
			if err == io.EOF {
				// Response is smaller than 1MB
				return contentBuf[:totalRead], int64(totalRead), nil
			}
			if err != nil {
				return contentBuf[:totalRead], int64(totalRead), err
			}
		}

		// Discard remaining data using io.Copy (much faster than reading in loop)
		discarded, err := io.Copy(io.Discard, bodyStream)
		totalSize := int64(totalRead) + discarded

		if err != nil && err != io.EOF {
			return contentBuf, totalSize, err
		}

		return contentBuf, totalSize, nil
	}

	// For non-streaming responses, get body content directly
	body := resp.Body()
	return body, int64(len(body)), nil
}

// TestEchoHandler tests the echo endpoint
func TestEchoHandler(t *testing.T) {
	addr, client, cleanup := setupTestServer(t)
	defer cleanup()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI("http://" + addr + "/")
	req.Header.SetMethod("POST")
	req.SetBodyString(`{"test":"data"}`)

	if err := client.Do(req, resp); err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if resp.StatusCode() != 200 {
		t.Errorf("expected status 200, got %d", resp.StatusCode())
	}

	// Get response body for content verification
	body, _, err := getResponseBody(resp)
	if err != nil {
		t.Fatalf("error reading response body: %v", err)
	}

	var result common.RequestJSON
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if result.Method != "POST" {
		t.Errorf("expected method POST, got %s", result.Method)
	}
	if result.Myhostname != "test-host" {
		t.Errorf("expected hostname test-host, got %s", result.Myhostname)
	}
}

// TestBinaryHandler tests the binary endpoint with different sizes
func TestBinaryHandler(t *testing.T) {
	addr, client, cleanup := setupTestServer(t)
	defer cleanup()

	tests := []struct {
		name     string
		size     string
		expected int64
	}{
		{"1KB", "1K", 1024},
		{"1MB", "1M", 1024 * 1024},
		{"10MB", "10M", 10 * 1024 * 1024},
		{"100MB", "100M", 100 * 1024 * 1024},
		{"1GB", "1G", 1024 * 1024 * 1024}, // Should be fast on localhost with optimized reading
		{"Arbitrary 11111", "11111", 11111},
		{"Arbitrary 999", "999", 999},
		// Sizes with arbitrary numeric prefixes
		{"500M", "500M", 500 * 1024 * 1024},
		{"2048K", "2048K", 2048 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := fasthttp.AcquireRequest()
			defer fasthttp.ReleaseRequest(req)
			resp := fasthttp.AcquireResponse()
			defer fasthttp.ReleaseResponse(resp)

			req.SetRequestURI("http://" + addr + "/bin/" + tt.size)
			req.Header.SetMethod("GET")

			if err := client.Do(req, resp); err != nil {
				t.Fatalf("request failed: %v", err)
			}

			if resp.StatusCode() != 200 {
				t.Errorf("expected status 200, got %d", resp.StatusCode())
			}

			// Check content type
			if !bytes.Equal(resp.Header.ContentType(), []byte("application/octet-stream")) {
				t.Errorf("expected content-type application/octet-stream, got %s", resp.Header.ContentType())
			}

			// Verify exact size using helper that handles streaming responses
			_, actualSize, err := getResponseBody(resp)
			if err != nil {
				t.Fatalf("error reading response body: %v", err)
			}

			if actualSize != tt.expected {
				t.Errorf("expected size %d, got %d", tt.expected, actualSize)
			}
		})
	}
}

// TestChunkedHandler tests the chunked endpoint
func TestChunkedHandler(t *testing.T) {
	addr, client, cleanup := setupTestServer(t)
	defer cleanup()

	tests := []struct {
		name      string
		count     string
		chunkSize string
		expected  int
	}{
		{"10 chunks default size", "10", "", 10 * 1024},
		{"5 chunks 2KB", "5", "2048", 5 * 2048},
		{"100 chunks 64 bytes", "100", "64", 100 * 64},
		{"2 chunks 2MB each", "2", "2097152", 2 * 2097152},
		{"10 chunks 1MB each", "10", "1048576", 10 * 1048576},       // 10MB
		{"100 chunks 1MB each", "100", "1048576", 100 * 1048576},    // 100MB
		{"1000 chunks 1MB each", "1000", "1048576", 1000 * 1048576}, // 1GB - fast with optimized reading
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := fasthttp.AcquireRequest()
			defer fasthttp.ReleaseRequest(req)
			resp := fasthttp.AcquireResponse()
			defer fasthttp.ReleaseResponse(resp)

			uri := "http://" + addr + "/chunked/" + tt.count
			if tt.chunkSize != "" {
				uri += "?size=" + tt.chunkSize
			}
			req.SetRequestURI(uri)
			req.Header.SetMethod("GET")

			if err := client.Do(req, resp); err != nil {
				t.Fatalf("request failed: %v", err)
			}

			if resp.StatusCode() != 200 {
				t.Errorf("expected status 200, got %d", resp.StatusCode())
			}

			// Verify size using helper that handles streaming responses
			_, actualSize, err := getResponseBody(resp)
			if err != nil {
				t.Fatalf("error reading response body: %v", err)
			}

			if int(actualSize) != tt.expected {
				t.Errorf("expected size %d, got %d", tt.expected, actualSize)
			}
		})
	}
}

// TestChunkedHandlerWithDelay tests the chunked endpoint with delay parameter
func TestChunkedHandlerWithDelay(t *testing.T) {
	addr, client, cleanup := setupTestServer(t)
	defer cleanup()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	// Request 5 chunks with 50ms delay between them
	// Expected time: at least 4 * 50ms = 200ms (no delay before first chunk)
	req.SetRequestURI("http://" + addr + "/chunked/5?size=1024&delay=50")
	req.Header.SetMethod("GET")

	start := time.Now()
	if err := client.Do(req, resp); err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if resp.StatusCode() != 200 {
		t.Errorf("expected status 200, got %d", resp.StatusCode())
	}

	// Read response body (this also measures elapsed time including reading)
	_, actualSize, err := getResponseBody(resp)
	if err != nil {
		t.Fatalf("error reading response body: %v", err)
	}
	elapsed := time.Since(start)

	// Verify response size (5 chunks × 1024 bytes)
	expectedSize := int64(5 * 1024)
	if actualSize != expectedSize {
		t.Errorf("expected size %d, got %d", expectedSize, actualSize)
	}

	// Verify delay occurred (at least 180ms, accounting for timing variance)
	// 4 delays × 50ms = 200ms, allow 20ms tolerance
	minExpected := 180 * time.Millisecond
	if elapsed < minExpected {
		t.Errorf("expected delay >= %v, got %v", minExpected, elapsed)
	}

	// Also verify it's not too slow (max 400ms)
	maxExpected := 400 * time.Millisecond
	if elapsed > maxExpected {
		t.Errorf("expected delay <= %v, got %v (too slow)", maxExpected, elapsed)
	}
}

// TestDelayHandler tests the delay endpoint
func TestDelayHandler(t *testing.T) {
	addr, client, cleanup := setupTestServer(t)
	defer cleanup()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI("http://" + addr + "/delay/100")
	req.Header.SetMethod("GET")

	start := time.Now()
	if err := client.Do(req, resp); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	elapsed := time.Since(start)

	if resp.StatusCode() != 200 {
		t.Errorf("expected status 200, got %d", resp.StatusCode())
	}

	// Verify delay occurred (at least 90ms, accounting for timing variance)
	if elapsed < 90*time.Millisecond {
		t.Errorf("expected delay >= 90ms, got %v", elapsed)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if durationMs, ok := result["duration_ms"].(float64); !ok || durationMs != 100 {
		t.Errorf("expected duration_ms 100, got %v", result["duration_ms"])
	}
}

// TestStatusHandler tests the status endpoint with different codes
func TestStatusHandler(t *testing.T) {
	addr, client, cleanup := setupTestServer(t)
	defer cleanup()

	tests := []struct {
		name       string
		statusCode string
		expected   int
	}{
		{"200 OK", "200", 200},
		{"404 Not Found", "404", 404},
		{"500 Internal Server Error", "500", 500},
		{"201 Created", "201", 201},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := fasthttp.AcquireRequest()
			defer fasthttp.ReleaseRequest(req)
			resp := fasthttp.AcquireResponse()
			defer fasthttp.ReleaseResponse(resp)

			req.SetRequestURI("http://" + addr + "/status/" + tt.statusCode)
			req.Header.SetMethod("GET")

			if err := client.Do(req, resp); err != nil {
				t.Fatalf("request failed: %v", err)
			}

			if resp.StatusCode() != tt.expected {
				t.Errorf("expected status %d, got %d", tt.expected, resp.StatusCode())
			}

			var result map[string]interface{}
			if err := json.Unmarshal(resp.Body(), &result); err != nil {
				t.Fatalf("failed to parse JSON: %v", err)
			}

			if code, ok := result["status_code"].(float64); !ok || int(code) != tt.expected {
				t.Errorf("expected status_code %d, got %v", tt.expected, result["status_code"])
			}
		})
	}
}

// TestUploadHandler tests the upload endpoint
func TestUploadHandler(t *testing.T) {
	addr, client, cleanup := setupTestServer(t)
	defer cleanup()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI("http://" + addr + "/upload")
	req.Header.SetMethod("POST")
	req.SetBodyString("test upload data")

	if err := client.Do(req, resp); err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if resp.StatusCode() != 200 {
		t.Errorf("expected status 200, got %d", resp.StatusCode())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if size, ok := result["bytes_received"].(float64); !ok || int(size) != 16 {
		t.Errorf("expected bytes_received 16, got %v", result["bytes_received"])
	}
}

// TestHealthHandler tests the health check endpoint
func TestHealthHandler(t *testing.T) {
	addr, client, cleanup := setupTestServer(t)
	defer cleanup()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI("http://" + addr + "/health")
	req.Header.SetMethod("GET")

	if err := client.Do(req, resp); err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if resp.StatusCode() != 200 {
		t.Errorf("expected status 200, got %d", resp.StatusCode())
	}

	expected := `{"status":"ok"}`
	if string(resp.Body()) != expected {
		t.Errorf("expected body %s, got %s", expected, string(resp.Body()))
	}
}

// TestInvalidRequests tests error handling
func TestInvalidRequests(t *testing.T) {
	addr, client, cleanup := setupTestServer(t)
	defer cleanup()

	tests := []struct {
		name           string
		path           string
		expectedStatus int
	}{
		{"Invalid binary size", "/bin/invalid", 400},
		{"Empty binary size", "/bin/", 400},
		{"Invalid chunk count", "/chunked/abc", 400},
		{"Invalid delay", "/delay/invalid", 400},
		{"Invalid status code", "/status/abc", 400},
		{"Status code out of range", "/status/999", 400},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := fasthttp.AcquireRequest()
			defer fasthttp.ReleaseRequest(req)
			resp := fasthttp.AcquireResponse()
			defer fasthttp.ReleaseResponse(resp)

			req.SetRequestURI("http://" + addr + tt.path)
			req.Header.SetMethod("GET")

			if err := client.Do(req, resp); err != nil {
				t.Fatalf("request failed: %v", err)
			}

			if resp.StatusCode() != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, resp.StatusCode())
			}
		})
	}
}

func TestMain(m *testing.M) {
	// Setup: Initialize common hostname
	common.Myhostname = "test-host"

	// Run tests
	code := m.Run()

	// Exit
	os.Exit(code)
}
