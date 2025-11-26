package binary

import (
	"testing"
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int64
		wantErr  bool
	}{
		// Pre-cached sizes
		{"1K cached", "1K", 1024, false},
		{"10M cached", "10M", 10 * 1024 * 1024, false},
		{"1G cached", "1G", 1024 * 1024 * 1024, false},

		// Arbitrary sizes with suffixes
		{"10000G", "10000G", 10000 * 1024 * 1024 * 1024, false},
		{"500M", "500M", 500 * 1024 * 1024, false},
		{"2048K", "2048K", 2048 * 1024, false},
		{"5T", "5T", 5 * 1024 * 1024 * 1024 * 1024, false},

		// Case insensitive
		{"lowercase k", "100k", 100 * 1024, false},
		{"lowercase m", "50m", 50 * 1024 * 1024, false},
		{"lowercase g", "2g", 2 * 1024 * 1024 * 1024, false},
		{"lowercase t", "1t", 1 * 1024 * 1024 * 1024 * 1024, false},

		// Raw integers
		{"raw 11111", "11111", 11111, false},
		{"raw 999999", "999999", 999999, false},
		{"raw 1", "1", 1, false},

		// Invalid inputs
		{"empty", "", 0, true},
		{"invalid suffix", "100X", 0, true},
		{"negative", "-100", 0, true},
		{"zero", "0", 0, true},
		{"invalid format", "abc", 0, true},
		{"no number before suffix", "K", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, errBytes := parseSize([]byte(tt.input))

			hasErr := errBytes != nil
			if hasErr != tt.wantErr {
				t.Errorf("parseSize(%q) error = %v, wantErr %v", tt.input, hasErr, tt.wantErr)
				return
			}

			if !tt.wantErr && result != tt.expected {
				t.Errorf("parseSize(%q) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}
