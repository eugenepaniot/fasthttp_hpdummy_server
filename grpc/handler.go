package grpc

import (
	"context"
	"fasthttp_hpdummy_server/common"
	pb "fasthttp_hpdummy_server/grpc/proto"
	"io"
	"log"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// logUnaryInterceptor handles logging and draining for unary RPCs
func logUnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	// Check draining state before processing
	if common.Draining.Load() {
		return nil, status.Error(codes.Unavailable, "server is shutting down")
	}

	// Call the handler
	resp, err := handler(ctx, req)

	// Log the request
	if !common.Quiet {
		if err != nil {
			log.Printf("[gRPC] %s error: %v", info.FullMethod, err)
		} else {
			log.Printf("[gRPC] %s OK", info.FullMethod)
		}
	}

	return resp, err
}

// logStreamInterceptor handles logging and draining for streaming RPCs
func logStreamInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	// Check draining state before processing
	if common.Draining.Load() {
		return status.Error(codes.Unavailable, "server is shutting down")
	}

	if !common.Quiet {
		log.Printf("[gRPC] %s stream started", info.FullMethod)
	}

	// Call the handler
	err := handler(srv, ss)

	// Log completion
	if !common.Quiet {
		if err != nil && err != io.EOF {
			log.Printf("[gRPC] %s stream error: %v", info.FullMethod, err)
		} else {
			log.Printf("[gRPC] %s stream ended", info.FullMethod)
		}
	}

	return err
}

// Description returns the endpoint description for startup logging
func Description() string {
	return "  - echo.EchoService/Echo, StreamEcho -> Unary and bidirectional streaming"
}

// Server represents a standalone gRPC server
type Server struct {
	addr         string
	grpcServer   *grpc.Server
	listener     net.Listener
	healthServer *health.Server
}

// NewServer creates a new gRPC server instance
func NewServer(addr string) *Server {
	return &Server{
		addr: addr,
	}
}

// Start starts the gRPC server on a separate port
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = ln

	s.grpcServer = grpc.NewServer(
		grpc.MaxRecvMsgSize(10*1024*1024), // 10MB max receive
		grpc.MaxSendMsgSize(10*1024*1024), // 10MB max send
		grpc.UnaryInterceptor(logUnaryInterceptor),
		grpc.StreamInterceptor(logStreamInterceptor),
	)

	// Register Echo service
	pb.RegisterEchoServiceServer(s.grpcServer, &echoServer{})

	// Register health service
	s.healthServer = health.NewServer()
	healthpb.RegisterHealthServer(s.grpcServer, s.healthServer)
	s.healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	go func() {
		log.Printf("[gRPC] starting on %s", s.addr)
		if err := s.grpcServer.Serve(ln); err != nil {
			log.Printf("[gRPC] stopped: %v", err)
		}
	}()

	return nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	if s.grpcServer == nil {
		return nil
	}

	// Mark as not serving before shutdown
	if s.healthServer != nil {
		s.healthServer.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	}

	stopped := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		log.Printf("[gRPC] shutdown complete")
		return nil
	case <-ctx.Done():
		log.Printf("[gRPC] shutdown timeout, forcing")
		s.grpcServer.Stop()
		return ctx.Err()
	}
}

// GetOpenConnectionsCount returns the number of currently open connections
// Note: gRPC doesn't provide built-in connection counting
func (s *Server) GetOpenConnectionsCount() int {
	// gRPC doesn't expose connection count directly
	return 0
}

// echoServer implements the gRPC echo service
type echoServer struct {
	pb.UnimplementedEchoServiceServer
}

// newEchoResponse creates a new EchoResponse from a request
func newEchoResponse(req *pb.EchoRequest) *pb.EchoResponse {
	return &pb.EchoResponse{
		Message:           req.Message,
		RequestTimestamp:  req.Timestamp,
		ResponseTimestamp: time.Now().UnixNano(),
		ServerHostname:    common.Myhostname,
	}
}

func (s *echoServer) Echo(ctx context.Context, req *pb.EchoRequest) (*pb.EchoResponse, error) {
	// Draining check handled by interceptor
	return newEchoResponse(req), nil
}

func (s *echoServer) StreamEcho(stream pb.EchoService_StreamEchoServer) error {
	// Draining check handled by interceptor (at stream start)
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		if err := stream.Send(newEchoResponse(req)); err != nil {
			return err
		}
	}
}
