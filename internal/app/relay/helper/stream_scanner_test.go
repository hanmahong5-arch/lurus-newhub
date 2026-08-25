package helper

import (
	"strings"
	"testing"
)

// outcome represents the result type of parsing a single SSE line.
type outcome int

const (
	outcomeSkipped outcome = iota
	outcomeData
	outcomeDone
)

// parseStreamLine replicates the inline parsing logic of stream_scanner.go's
// scan loop as a pure function so the line shapes can be enumerated cheaply.
// It is a replica, not an oracle — the behavioural cover that actually drives
// StreamScannerHandler lives in stream_scanner_terminal_test.go.
func parseStreamLine(line string) (outcome, string) {
	data := line

	if len(data) < 6 {
		return outcomeSkipped, ""
	}
	// Terminal-vs-data is decided before any stripping: a bare "[DONE]" has no
	// "data:" prefix to strip.
	if strings.HasPrefix(data, "[DONE]") {
		return outcomeDone, data
	}
	if !strings.HasPrefix(data, "data:") {
		return outcomeSkipped, ""
	}
	data = strings.TrimSpace(data[5:])
	if data == "" {
		return outcomeSkipped, ""
	}
	if strings.HasPrefix(data, "[DONE]") {
		return outcomeDone, data
	}
	return outcomeData, data
}

func TestStreamLineParseLogic(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantOutcome outcome
		wantData    string
	}{
		{
			name:        "standard JSON data",
			line:        `data: {"key":"value"}`,
			wantOutcome: outcomeData,
			wantData:    `{"key":"value"}`,
		},
		{
			name:        "trailing CR",
			line:        "data: {\"k\":\"v\"}\r",
			wantOutcome: outcomeData,
			wantData:    `{"k":"v"}`,
		},
		{
			name:        "leading+trailing whitespace after colon",
			line:        `data:   {"k":"v"}   `,
			wantOutcome: outcomeData,
			wantData:    `{"k":"v"}`,
		},
		{
			name:        "no space after colon",
			line:        `data:{"k":"v"}`,
			wantOutcome: outcomeData,
			wantData:    `{"k":"v"}`,
		},
		{
			name:        "standard DONE",
			line:        "data: [DONE]",
			wantOutcome: outcomeDone,
			wantData:    "[DONE]",
		},
		{
			// "data:[DONE]" → data[:5]="data:" matches, data[5:]="[DONE]",
			// TrimSpace → "[DONE]", HasPrefix("[DONE]") → true → outcomeDone
			name:        "DONE no space",
			line:        "data:[DONE]",
			wantOutcome: outcomeDone,
			wantData:    "[DONE]",
		},
		{
			name:        "empty line",
			line:        "",
			wantOutcome: outcomeSkipped,
			wantData:    "",
		},
		{
			name:        "5 chars data colon only",
			line:        "data:",
			wantOutcome: outcomeSkipped,
			wantData:    "",
		},
		{
			name:        "4 chars",
			line:        "ping",
			wantOutcome: outcomeSkipped,
			wantData:    "",
		},
		{
			name:        "non-data prefix",
			line:        "event: message",
			wantOutcome: outcomeSkipped,
			wantData:    "",
		},
		{
			name:        "SSE id field",
			line:        "id: 12345678",
			wantOutcome: outcomeSkipped,
			wantData:    "",
		},
		{
			name:        "whitespace after colon",
			line:        "data:  \r\n",
			wantOutcome: outcomeSkipped,
			wantData:    "",
		},
		{
			name:        "only CR after colon",
			line:        "data:\r",
			wantOutcome: outcomeSkipped,
			wantData:    "",
		},
		{
			name:        "only spaces after colon",
			line:        "data:     ",
			wantOutcome: outcomeSkipped,
			wantData:    "",
		},
		{
			// Was asserted as outcomeData/"]" — that documented the defect rather
			// than the contract: an unconditional 5-byte strip mangled a bare
			// terminator into a payload and lost the [DONE] signal entirely.
			name:        "raw DONE line",
			line:        "[DONE]",
			wantOutcome: outcomeDone,
			wantData:    "[DONE]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOutcome, gotData := parseStreamLine(tt.line)
			if gotOutcome != tt.wantOutcome {
				t.Errorf("parseStreamLine(%q) outcome = %d, want %d", tt.line, gotOutcome, tt.wantOutcome)
			}
			if gotData != tt.wantData {
				t.Errorf("parseStreamLine(%q) data = %q, want %q", tt.line, gotData, tt.wantData)
			}
		})
	}
}
