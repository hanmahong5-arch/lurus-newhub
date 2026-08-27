package common

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// r5bNonEmptyLines counts newline-delimited records in s, ignoring a trailing
// blank line from the final "\n". Used to assert "exactly one record was
// written" without depending on exact byte layout.
func r5bNonEmptyLines(s string) []string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestSysLog_JSONMode_WritesExactlyOneStructuredRecord locks the JSON-mode
// half of the SysLog double-write fix: when InitSlog was set up with
// JSONFormat:true, SysLog must NOT also Fprintf the legacy "[SYS] ..." line
// to gin.DefaultWriter — the slog record already carries the message plus
// source=system, so both together would double the event count in the log
// pipeline. Mutation proof: reintroducing the unconditional Fprintf in
// internal/pkg/common/sys_log.go makes the nonEmpty-line count assertion
// below fail (2 lines instead of 1).
func TestSysLog_JSONMode_WritesExactlyOneStructuredRecord(t *testing.T) {
	buf := &bytes.Buffer{}
	InitSlog(&SlogConfig{JSONFormat: true, Writer: buf, ErrWriter: buf})
	out, errOut := covSwapGinWriters(t)

	SysLog("x")

	if out.Len() != 0 {
		t.Errorf("SysLog JSON mode must not write to gin.DefaultWriter, got %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("SysLog JSON mode must not write to gin.DefaultErrorWriter, got %q", errOut.String())
	}

	lines := r5bNonEmptyLines(buf.String())
	if len(lines) != 1 {
		t.Fatalf("SysLog JSON mode wrote %d record(s), want exactly 1: %q", len(lines), buf.String())
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("SysLog JSON mode output is not valid JSON: %v\nline=%q", err, lines[0])
	}
	if record["msg"] != "x" {
		t.Errorf("record msg = %v, want %q", record["msg"], "x")
	}
	if record["source"] != "system" {
		t.Errorf("record source = %v, want %q", record["source"], "system")
	}
}

// TestSysError_JSONMode_WritesExactlyOneStructuredRecord mirrors the SysLog
// case for the error-writer sibling.
func TestSysError_JSONMode_WritesExactlyOneStructuredRecord(t *testing.T) {
	buf := &bytes.Buffer{}
	InitSlog(&SlogConfig{JSONFormat: true, Writer: buf, ErrWriter: buf})
	out, errOut := covSwapGinWriters(t)

	SysError("x")

	if out.Len() != 0 {
		t.Errorf("SysError JSON mode must not write to gin.DefaultWriter, got %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("SysError JSON mode must not write to gin.DefaultErrorWriter, got %q", errOut.String())
	}

	lines := r5bNonEmptyLines(buf.String())
	if len(lines) != 1 {
		t.Fatalf("SysError JSON mode wrote %d record(s), want exactly 1: %q", len(lines), buf.String())
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("SysError JSON mode output is not valid JSON: %v\nline=%q", err, lines[0])
	}
}

// TestSysLog_TextMode_WritesExactlyOneLegacyLine locks the text-mode half:
// when the logger is in text mode, SysLog must still write exactly the one
// legacy "[SYS] " line (no structured duplicate alongside it — the structured
// side is skipped entirely in this mode).
func TestSysLog_TextMode_WritesExactlyOneLegacyLine(t *testing.T) {
	InitSlog(&SlogConfig{JSONFormat: false, Writer: io.Discard, ErrWriter: io.Discard})
	out, errOut := covSwapGinWriters(t)

	SysLog("x")

	if errOut.Len() != 0 {
		t.Errorf("SysLog text mode must not write to gin.DefaultErrorWriter, got %q", errOut.String())
	}
	lines := r5bNonEmptyLines(out.String())
	if len(lines) != 1 {
		t.Fatalf("SysLog text mode wrote %d line(s), want exactly 1: %q", len(lines), out.String())
	}
	if !strings.HasPrefix(lines[0], "[SYS] ") {
		t.Errorf("SysLog text mode line missing [SYS] prefix: %q", lines[0])
	}
}
