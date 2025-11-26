# fasthttp_hpdummy_server

High-performance HTTP dummy server built with [fasthttp](https://github.com/valyala/fasthttp) for comprehensive proxy and load testing.

## Purpose

**Performance testing infrastructure for proxies and load balancers.**

This server is designed to generate and handle massive amounts of data and requests per second (RPS) to stress-test:
- Reverse proxies (nginx, HAProxy, Envoy, etc.)
- Load balancers (AWS ALB/NLB, GCP Load Balancer, etc.)
- API gateways and service meshes
- CDN and caching layers

Supports testing various scenarios: large file transfers, chunked encoding, status codes, delays, and high-concurrency workloads.

## Features

- **Zero-allocation design** - Extensive use of sync.Pool and zero-copy conversions
- **Container-aware** - Uses [automaxprocs](https://github.com/uber-go/automaxprocs) to match CPU quota (prevents throttling, ~2x throughput in containers)
- **Graceful shutdown** - Kubernetes-ready with proper connection draining
- **High throughput** - Optimized for multi-GB/s data transfers
- **High concurrency** - Handles 256K+ simultaneous connections
- **Flexible testing** - Configurable delays, chunk sizes, and transfer encodings

## Available Endpoints

### Core Handlers

- **`/`** - Echo server  
  Returns request details as JSON including headers, method, URI, body, and connection info

- **`/health`** - Health check  
  Returns `{"status": "ok"}` for readiness/liveness probes

### Data Transfer Testing

- **`/bin/{size}`** - Binary response (optimized streaming)
  Returns binary data of specified size using highly optimized streaming  
  - Sizes: `1K`, `10K`, `100K`, `1M`, `10M`, `100M`, `500M`, `1G`, `10G`, or any byte count (e.g., `11111`)
  - Query params:
    - `?chunked=true` - Force chunked transfer encoding (default: Content-Length for max speed)
  - Optimized with sync.Pool, zero-copy writes, and pre-filled buffers
  - **Proven faster than mmap/sendfile** in benchmarks (~1.5 GB/s on localhost)
  - Examples:
    - `/bin/1M` - 1MB with Content-Length
    - `/bin/10G` - 10GB optimized transfer
    - `/bin/10000G` - 10TB (unlimited size)

- **`/chunked/{count}`** - Chunked response  
  Returns data in multiple chunks with configurable size and delays
  - Query params:
    - `size={bytes}` - Bytes per chunk (default: 1024)
    - `delay={ms}` - Milliseconds between chunks (default: 0)
  - Examples:
    - `/chunked/10` - 10 chunks of 1KB each
    - `/chunked/100?size=2048&delay=100` - 100 chunks of 2KB with 100ms delays

- **`/upload`** - Upload endpoint  
  Accepts POST/PUT with request body, returns received byte count

### Testing Helpers

- **`/status/{code}`** - HTTP status codes  
  Returns specified HTTP status with request details
  - Examples: `/status/200`, `/status/404`, `/status/500`

- **`/delay/{ms}`** - Delayed response  
  Sleeps for specified milliseconds before responding
  - Examples: `/delay/100`, `/delay/1000`

- **`/ws`** - WebSocket echo  
  WebSocket endpoint that echoes received messages

### gRPC

- **`:50051`** - gRPC server  
  Provides gRPC services on separate port

## Performance Optimizations

### Memory & GC
- `sync.Pool` for all frequently allocated objects (requests, responses, buffers)
- Pre-allocated 4MB buffers with repeating data pattern
- Constant fields initialized once in pool constructors
- Zero-copy string/byte conversions using `unsafe`

### Network
- TCP Fast Open (TFO) for reduced connection latency
- SO_REUSEPORT for multi-process load balancing
- 4MB read/write buffers matching chunk sizes
- Optional chunked transfer encoding control

### Routing
- Byte-slice path constants to avoid runtime conversions
- Direct `bytes.Equal` / `bytes.HasPrefix` comparisons
- No regex overhead

### Streaming
- Unified `StreamWriter` implementing both `io.Reader` and `io.WriterTo`
- Conditional per-chunk flushing (enabled for chunked, disabled for binary)
- Configurable buffer sizes (64KB-4MB) via `-buffer-size` flag
- Automatic resource cleanup via wrapper
- Memory scaling examples (with default 256KB buffers):
  - 100 connections: ~77MB (100 × 3 × 256KB)
  - 1000 connections: ~750MB
  - With 1MB buffers: ~3GB for 1000 connections

### Garbage Collection
- Tunable via standard `GOGC` environment variable
- Recommendation: `GOGC=200` for high-throughput scenarios
- Higher values reduce GC frequency at cost of memory usage

## Usage

```bash
# Start server (default port 8080, gRPC on 50051)
./fasthttp_hpdummy_server

# Custom configuration
./fasthttp_hpdummy_server \
  -addr=:8080 \
  -buffer-size=1024 \
  -mem-interval=10s \
  -pprof-addr=:6060

# Performance tuning flags:
#   -buffer-size=256     : Buffer size in KB (64-4096, default 256)
#                          Affects read/write/streaming buffers
#                          Higher = more throughput, more memory per connection
#   -mem-interval=10s    : Memory stats reporting interval (0 to disable)
#   -quiet=true          : Suppress verbose request logging
#   GOGC=200             : GC target % (env var, higher = less frequent GC, more memory)

# Examples for different workloads:

# High concurrency (1000+ connections): use smaller buffers
./fasthttp_hpdummy_server -buffer-size=64

# High throughput (large transfers): use larger buffers
./fasthttp_hpdummy_server -buffer-size=1024

# Balanced (default)
./fasthttp_hpdummy_server -buffer-size=256

# With GOGC tuning for maximum throughput
GOGC=200 ./fasthttp_hpdummy_server -buffer-size=1024

# Test binary transfer with Content-Length (maximum speed)
curl http://localhost:8080/bin/1G -o /dev/null

# Test chunked transfer encoding
curl 'http://localhost:8080/bin/1G?chunked=true' -o /dev/null

# Test chunked response with delays
curl 'http://localhost:8080/chunked/10?size=1024&delay=100'

# Test status codes
curl -i http://localhost:8080/status/404

# Test delayed responses
curl http://localhost:8080/delay/500

# Echo request details
curl -X POST http://localhost:8080/ -d "test data"
```

## Building

```bash
make build
# or
go build -o fasthttp_hpdummy_server
```

## Testing

```bash
# Run all tests
go test -v

# Run specific handler tests
go test -v -run TestBinaryHandler

# Run benchmarks
go test -bench=. -benchmem ./...
```

## Kubernetes Deployment

The server is designed for Kubernetes with:
- Graceful shutdown via `SIGTERM` / `SIGINT`
- 10-second idle timeout to close keep-alive connections
- `Connection: close` header during shutdown (draining mode)
- Health check endpoint at `/health`

## License

MIT
