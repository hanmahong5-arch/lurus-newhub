package common

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// covSwapGinWriters redirects gin.DefaultWriter/DefaultErrorWriter to buffers
// for the duration of the test and restores the originals afterward, so
// SysLog/SysError/LogStartupSuccess output can be asserted without polluting
// stdout/stderr for the rest of the suite.
func covSwapGinWriters(t *testing.T) (out *bytes.Buffer, errOut *bytes.Buffer) {
	t.Helper()
	origOut, origErr := gin.DefaultWriter, gin.DefaultErrorWriter
	out, errOut = &bytes.Buffer{}, &bytes.Buffer{}
	gin.DefaultWriter, gin.DefaultErrorWriter = out, errOut
	t.Cleanup(func() {
		gin.DefaultWriter, gin.DefaultErrorWriter = origOut, origErr
	})
	return out, errOut
}

// TestSysLog_WritesLegacyFormatAndStructuredLog asserts SysLog produces the
// documented "[SYS] <timestamp> | <message>" line on gin.DefaultWriter — the
// legacy console format ops tooling greps for — and does not touch the error
// writer at all.
func TestSysLog_WritesLegacyFormatAndStructuredLog(t *testing.T) {
	out, errOut := covSwapGinWriters(t)

	SysLog("hub booted on node-7")

	got := out.String()
	if !strings.HasPrefix(got, "[SYS] ") {
		t.Errorf("SysLog output missing [SYS] prefix: %q", got)
	}
	if !strings.Contains(got, "hub booted on node-7") {
		t.Errorf("SysLog output missing message: %q", got)
	}
	if !strings.HasSuffix(got, " \n") {
		t.Errorf("SysLog output missing trailing space+newline sentinel: %q", got)
	}
	if errOut.Len() != 0 {
		t.Errorf("SysLog must not write to the error writer, got %q", errOut.String())
	}
}

// TestSysError_WritesToErrorWriterOnly asserts SysError's legacy line lands on
// gin.DefaultErrorWriter (so it flows into stderr/alerting pipelines) and
// leaves the info writer untouched.
func TestSysError_WritesToErrorWriterOnly(t *testing.T) {
	out, errOut := covSwapGinWriters(t)

	SysError("db ping failed: connection refused")

	got := errOut.String()
	if !strings.HasPrefix(got, "[SYS] ") {
		t.Errorf("SysError output missing [SYS] prefix: %q", got)
	}
	if !strings.Contains(got, "db ping failed: connection refused") {
		t.Errorf("SysError output missing message: %q", got)
	}
	if out.Len() != 0 {
		t.Errorf("SysError must not write to the info writer, got %q", out.String())
	}
}

// TestSysLogf_FormatsBeforeDelegating covers the format-then-delegate wrapper:
// the printf verbs must be expanded, not passed through literally.
func TestSysLogf_FormatsBeforeDelegating(t *testing.T) {
	out, _ := covSwapGinWriters(t)

	SysLogf("tenant=%s quota=%d", "acme-co", 42)

	got := out.String()
	if !strings.Contains(got, "tenant=acme-co quota=42") {
		t.Errorf("SysLogf did not expand format verbs: %q", got)
	}
	if strings.Contains(got, "%s") || strings.Contains(got, "%d") {
		t.Errorf("SysLogf leaked raw format verbs: %q", got)
	}
}

// TestSysErrorf_FormatsBeforeDelegating mirrors TestSysLogf_FormatsBeforeDelegating
// for the error-writer sibling.
func TestSysErrorf_FormatsBeforeDelegating(t *testing.T) {
	_, errOut := covSwapGinWriters(t)

	SysErrorf("channel %d disabled after %d failures", 17, 3)

	got := errOut.String()
	if !strings.Contains(got, "channel 17 disabled after 3 failures") {
		t.Errorf("SysErrorf did not expand format verbs: %q", got)
	}
}

// TestLogStartupSuccess_NonContainer_PrintsLocalURL exercises the
// !IsRunningInContainer branch: the "Local:" line must be printed alongside
// version/duration, since a bare-metal/dev boot benefits from a clickable URL.
func TestLogStartupSuccess_NonContainer_PrintsLocalURL(t *testing.T) {
	out, _ := covSwapGinWriters(t)

	// Force the non-container path deterministically regardless of the host
	// this test runs on (CI containers would otherwise flip the branch).
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("DOCKER_CONTAINER", "")
	t.Setenv("container", "")
	if IsRunningInContainer() {
		t.Skip("host environment is detected as a container; branch under test is unreachable here")
	}

	start := time.Now().Add(-250 * time.Millisecond)
	LogStartupSuccess(start, "3000")

	got := out.String()
	if !strings.Contains(got, "Local:") {
		t.Errorf("expected Local URL line outside containers, got %q", got)
	}
	if !strings.Contains(got, "http://localhost:3000/") {
		t.Errorf("expected port 3000 in local URL, got %q", got)
	}
	if !strings.Contains(got, SystemName) {
		t.Errorf("expected SystemName %q in banner, got %q", SystemName, got)
	}
	if !strings.Contains(got, "ready in") {
		t.Errorf("expected duration banner, got %q", got)
	}
}

// TestLogStartupSuccess_Container_SkipsLocalURL exercises the
// IsRunningInContainer branch: no "Local:" line should print since
// localhost is meaningless from inside a container.
func TestLogStartupSuccess_Container_SkipsLocalURL(t *testing.T) {
	out, _ := covSwapGinWriters(t)

	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")

	start := time.Now()
	LogStartupSuccess(start, "8080")

	got := out.String()
	if strings.Contains(got, "Local:") {
		t.Errorf("container boot must not print a Local: URL, got %q", got)
	}
	if !strings.Contains(got, "ready in") {
		t.Errorf("expected duration banner regardless of container status, got %q", got)
	}
}
