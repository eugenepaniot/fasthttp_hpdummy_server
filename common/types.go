package common

import "sync/atomic"

// UsageStruct represents token usage information for API responses
type UsageStruct struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	InputTokens      int `json:"input_tokens,omitempty"`
	OutputTokens     int `json:"output_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

// RequestJSON represents the JSON structure for request logging
type RequestJSON struct {
	Myhostname      string            `json:"_myhostname"`
	URI             string            `json:"uri"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers"`
	ContentType     string            `json:"content_type"`
	Body            string            `json:"body"`                     // Request body (truncated if > MaxResponseBodySize)
	BodySize        int64             `json:"body_size"`                // Actual full size of request body in bytes
	BodyTruncated   bool              `json:"body_truncated,omitempty"` // True if body was truncated in response
	Usage           UsageStruct       `json:"usage"`
	SourceAddr      string            `json:"source_addr"`      // Client IP:PORT (RemoteAddr)
	DestinationAddr string            `json:"destination_addr"` // Server IP:PORT (LocalAddr)
}

// Global state shared across all servers
var (
	Quiet      bool
	Myhostname string
	Draining   atomic.Bool // Flag to indicate server is draining (set when SIGTERM received)
)
