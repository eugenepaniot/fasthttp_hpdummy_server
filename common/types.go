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
	Body            string            `json:"body"`      // Zero-copy via B2s, safe since we marshal immediately
	BodySize        int64             `json:"body_size"` // Size of body in bytes
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
