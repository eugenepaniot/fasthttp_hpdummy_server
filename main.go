package main

import (
	"context"
	"fasthttp_hpdummy_server/binary"
	"fasthttp_hpdummy_server/chunked"
	"fasthttp_hpdummy_server/common"
	"fasthttp_hpdummy_server/delay"
	"fasthttp_hpdummy_server/echo"
	grpcserver "fasthttp_hpdummy_server/grpc"
	"fasthttp_hpdummy_server/router"
	"fasthttp_hpdummy_server/status"
	"fasthttp_hpdummy_server/upload"
	"fasthttp_hpdummy_server/websocket"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/tcplisten"

	// Import pprof for profiling
	"net/http"
	_ "net/http/pprof"

	// Automatically set GOMAXPROCS to match container CPU quota
	// Critical for consistent performance in Kubernetes/containerized environments
	_ "go.uber.org/automaxprocs"
)

// init runs before main() and sets up the hostname
func init() {
	var err error
	if common.Myhostname, err = os.Hostname(); err != nil {
		log.Fatalf("error getting hostname: %v", err)
	}
}

// writePIDFile writes the current process ID to the specified file
// and returns a cleanup function to remove the file on exit
func writePIDFile(pidfile string) (pid int, cleanup func(), err error) {
	pid = os.Getpid()
	pidContent := fmt.Sprintf("%d\n", pid)

	if err := os.WriteFile(pidfile, []byte(pidContent), 0644); err != nil {
		return 0, nil, fmt.Errorf("error writing PID file: %w", err)
	}

	log.Printf("wrote PID %d to %s", pid, pidfile)

	// Return cleanup function that removes the PID file
	cleanup = func() {
		if err := os.Remove(pidfile); err != nil {
			log.Printf("error removing PID file: %v", err)
		} else {
			log.Printf("removed PID file: %s", pidfile)
		}
	}

	return pid, cleanup, nil
}

// startMemoryMonitor starts a goroutine that periodically reports memory and GC statistics
// It tracks differences between cycles to show memory growth and GC activity
func startMemoryMonitor(interval time.Duration) {
	var prevStats runtime.MemStats
	runtime.ReadMemStats(&prevStats)

	var gcStats debug.GCStats
	gcStats.PauseQuantiles = make([]time.Duration, 5) // Min, 25%, 50%, 75%, Max

	ticker := time.NewTicker(interval)

	go func() {
		defer ticker.Stop()

		for range ticker.C {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)

			// Read GC statistics
			debug.ReadGCStats(&gcStats)

			// Calculate deltas from previous cycle
			allocDelta := int64(m.Alloc) - int64(prevStats.Alloc)
			totalAllocDelta := int64(m.TotalAlloc) - int64(prevStats.TotalAlloc)
			heapAllocDelta := int64(m.HeapAlloc) - int64(prevStats.HeapAlloc)
			numGCDelta := m.NumGC - prevStats.NumGC

			// Format memory sizes with appropriate units
			allocStr := formatBytes(m.Alloc)
			totalAllocStr := formatBytes(m.TotalAlloc)
			heapAllocStr := formatBytes(m.HeapAlloc)
			sysStr := formatBytes(m.Sys)

			// Format deltas with +/- prefix
			allocDeltaStr := formatDelta(allocDelta)
			totalAllocDeltaStr := formatDelta(totalAllocDelta)
			heapAllocDeltaStr := formatDelta(heapAllocDelta)

			// Format GC pause statistics
			var gcPauseInfo string
			if len(gcStats.Pause) > 0 {
				// Most recent pause is first in the slice
				lastPause := gcStats.Pause[0]
				p50 := gcStats.PauseQuantiles[2] // Median
				p99 := gcStats.PauseQuantiles[4] // Max (approximates P99)
				gcPauseInfo = fmt.Sprintf(" | GCPause: last=%v p50=%v p99=%v total=%v",
					lastPause.Round(time.Microsecond),
					p50.Round(time.Microsecond),
					p99.Round(time.Microsecond),
					gcStats.PauseTotal.Round(time.Millisecond),
				)
			}

			log.Printf("[MEMORY] Alloc=%s (%s) | TotalAlloc=%s (%s) | HeapAlloc=%s (%s) | Sys=%s | GC=%d (+%d) | Goroutines=%d%s",
				allocStr, allocDeltaStr,
				totalAllocStr, totalAllocDeltaStr,
				heapAllocStr, heapAllocDeltaStr,
				sysStr,
				m.NumGC, numGCDelta,
				runtime.NumGoroutine(),
				gcPauseInfo,
			)

			// Store current stats for next comparison
			prevStats = m
		}
	}()
}

// formatBytes formats a byte count into a human-readable string (KB, MB, GB)
func formatBytes(bytes uint64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// formatDelta formats a signed byte delta with +/- prefix
func formatDelta(delta int64) string {
	if delta >= 0 {
		return "+" + formatBytes(uint64(delta))
	}
	return "-" + formatBytes(uint64(-delta))
}

// NewServer creates and configures a fasthttp server with optimized settings
func NewServer(bufferSize int) *fasthttp.Server {
	// Create unified router for HTTP endpoints
	r := router.NewRouter()

	return &fasthttp.Server{
		// Connection settings
		TCPKeepalive: true,
		IdleTimeout:  10 * time.Second,

		// Performance tuning
		Concurrency:           256 * 1024,  // Max concurrent connections (256k)
		MaxConnsPerIP:         0,           // No limit per IP
		MaxRequestsPerConn:    0,           // No limit, allow keep-alive
		MaxRequestBodySize:    1024 * 1024, // 1MB - bodies larger than this trigger streaming
		StreamRequestBody:     true,        // Enable streaming for bodies > MaxRequestBodySize
		ReadBufferSize:        bufferSize,  // Configurable read buffer
		WriteBufferSize:       bufferSize,  // Configurable write buffer
		ReduceMemoryUsage:     false,       // Prioritize performance over memory
		DisableKeepalive:      false,       // Enable keep-alive for better performance
		TCPKeepalivePeriod:    30 * time.Second,
		MaxIdleWorkerDuration: 10 * time.Second,

		// Logging
		LogAllErrors: false, // Disable to reduce overhead at high RPS

		// Handler and shutdown
		Handler:         r.Handler,
		CloseOnShutdown: true, // Add 'Connection: close' header during shutdown
	}
}

func main() {
	var err error

	// Parse command-line flags
	flag.BoolVar(&common.Quiet, "quiet", true, "suppress verbose logging")
	addr := flag.String("addr", "0.0.0.0:8080", "HTTP server listen address")
	grpcAddr := flag.String("grpc-addr", "0.0.0.0:50051", "gRPC server listen address")
	pprofAddr := flag.String("pprof-addr", "localhost:6060", "pprof server listen address (use empty string to disable)")
	pidfile := flag.String("pidfile", "fasthttp_hpdummy_server.pid", "path to PID file")
	memInterval := flag.Duration("mem-interval", 10*time.Second, "memory stats reporting interval (0 to disable)")
	bufferSize := flag.Int("buffer-size", 256, "buffer size in KB (read/write/streaming buffers, 64-4096)")
	flag.Parse()

	// Validate and convert buffer size
	if *bufferSize < 64 || *bufferSize > 4096 {
		log.Fatalf("buffer-size must be between 64 and 4096 KB, got %d", *bufferSize)
	}
	bufferSizeBytes := *bufferSize * 1024

	// Write PID file and setup cleanup
	pid, cleanupPID, err := writePIDFile(*pidfile)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer cleanupPID()

	// Start pprof server if enabled
	// Access via http://localhost:6060/debug/pprof/
	if *pprofAddr != "" {
		go func() {
			if err := http.ListenAndServe(*pprofAddr, nil); err != nil {
				log.Printf("pprof server error: %v", err)
			}
		}()
	}

	// Start memory monitor if enabled
	if *memInterval > 0 {
		startMemoryMonitor(*memInterval)
		log.Printf("memory monitor started (interval: %v)", *memInterval)
	}

	// Start gRPC server on separate port
	// gRPC requires HTTP/2, so it needs its own listener
	grpcSrv := grpcserver.NewServer(*grpcAddr)
	if err := grpcSrv.Start(); err != nil {
		log.Fatalf("error starting gRPC server: %v", err)
	}

	// Create TCP listener with SO_REUSEPORT and TCP_FASTOPEN
	cfg := tcplisten.Config{
		ReusePort: true,
		FastOpen:  true,
		Backlog:   4096,
	}
	ln, err := cfg.NewListener("tcp4", *addr)
	if err != nil {
		log.Fatalf("error creating listener: %v", err)
	}
	defer ln.Close()

	// Initialize buffer pool with configured size
	common.InitBinaryBufferPool(bufferSizeBytes)
	log.Printf("buffer sizes: read=%s write=%s streaming=%s",
		formatBytes(uint64(bufferSizeBytes)),
		formatBytes(uint64(bufferSizeBytes)),
		formatBytes(uint64(bufferSizeBytes)))

	// Create fasthttp server with optimized settings
	server := NewServer(bufferSizeBytes)

	// Start the HTTP server in a goroutine
	go func() {
		log.Printf("starting HTTP server on %s", *addr)
		if err := server.Serve(ln); err != nil {
			log.Printf("HTTP server stopped: %v", err)
		}
	}()

	// Log startup summary
	log.Printf("=== Server Started ===")
	log.Printf("Hostname: %s, PID: %d", common.Myhostname, pid)
	log.Printf("HTTP: %s", *addr)
	log.Printf("%s", echo.Description())
	log.Printf("  - /health     -> Health check (returns {\"status\":\"ok\"})")
	log.Printf("  - /help       -> List available endpoints")
	log.Printf("%s", binary.Description())
	log.Printf("%s", chunked.Description())
	log.Printf("%s", delay.Description())
	log.Printf("%s", status.Description())
	log.Printf("%s", upload.Description())
	log.Printf("%s", websocket.Description())
	log.Printf("gRPC: %s", *grpcAddr)
	log.Printf("%s", grpcserver.Description())
	if *pprofAddr != "" {
		log.Printf("pprof: http://%s/debug/pprof/", *pprofAddr)
	}
	log.Printf("======================")

	// Wait for shutdown signal
	// SIGTERM and SIGINT (Ctrl+C) will trigger graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	receivedSig := <-sig
	log.Printf("received signal: %v, entering draining mode...", receivedSig)

	// Set draining flag
	// This signals all handlers to start sending "Connection: close" headers
	// and reject new long-running requests
	common.Draining.Store(true)

	// Create shutdown context with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer shutdownCancel()

	// Check if there are open connections
	openConns := server.GetOpenConnectionsCount()
	if openConns > 0 {
		// Grace period for idle keepalive connections
		// This gives existing idle connections a chance to make one more request
		// and receive the Connection: close header before we shut down
		gracePeriod := 1 * time.Second
		log.Printf("[HTTP] %d open connection(s), waiting %v for drain...", openConns, gracePeriod)
		time.Sleep(gracePeriod)
	}

	// Shutdown servers in parallel
	var wg sync.WaitGroup

	// Shutdown HTTP server
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := server.ShutdownWithContext(shutdownCtx); err != nil {
			if err == context.DeadlineExceeded {
				log.Printf("[HTTP] shutdown timeout, forcing close of %d connection(s)", server.GetOpenConnectionsCount())
			} else {
				log.Printf("[HTTP] shutdown error: %v", err)
			}
		} else {
			log.Printf("[HTTP] shutdown complete")
		}
	}()

	// Shutdown gRPC server
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := grpcSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("[gRPC] shutdown error: %v", err)
		}
	}()

	wg.Wait()
	log.Printf("=== Server Stopped ===")
	log.Printf("bye bye!")
}
