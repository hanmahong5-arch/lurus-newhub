package common

// Covers printHelp (0% — never invoked outside the --help CLI flag path,
// which InitEnv's own tests avoid because it calls os.Exit) and SendEmail's
// configuration-guard / dial-failure branches (15.2% — the existing email
// tests only exercise generateMessageID and LoginAuth directly, never
// SendEmail itself). No real SMTP server is reachable in this sandbox, so
// the network branches are exercised against a guaranteed-closed local port
// to force a deterministic dial error rather than a real send.

import (
	"io"
	"net"
	"os"
	"strings"
	"testing"
)

func r5coreSnapshotSMTP(t *testing.T) {
	t.Helper()
	prevFrom, prevAccount, prevServer := SMTPFrom, SMTPAccount, SMTPServer
	prevPort, prevSSL, prevToken := SMTPPort, SMTPSSLEnabled, SMTPToken
	prevSystemName := SystemName
	t.Cleanup(func() {
		SMTPFrom, SMTPAccount, SMTPServer = prevFrom, prevAccount, prevServer
		SMTPPort, SMTPSSLEnabled, SMTPToken = prevPort, prevSSL, prevToken
		SystemName = prevSystemName
	})
}

// r5coreClosedPort returns a TCP address on loopback that is guaranteed to
// refuse connections: bind, learn the OS-assigned port, then close it
// immediately before anything else can rebind it in this short window.
func r5coreClosedPort(t *testing.T) (host string, port int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	addr := l.Addr().(*net.TCPAddr)
	if err := l.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return "127.0.0.1", addr.Port
}

func TestPrintHelp_WritesUsageToStdout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	printHelp()
	os.Stdout = origStdout
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}

	got := string(out)
	for _, want := range []string{"Usage: newapi", "--port", "--help", "QuantumNous"} {
		if !strings.Contains(got, want) {
			t.Errorf("printHelp() output = %q, want it to contain %q", got, want)
		}
	}
}

func TestSendEmail_InvalidFromAccountErrorsBeforeNetwork(t *testing.T) {
	r5coreSnapshotSMTP(t)
	SMTPFrom = ""
	SMTPAccount = "not-an-email-address" // no '@' -> generateMessageID fails
	SMTPServer = "smtp.example.com"

	err := SendEmail("subject", "someone@example.com", "body")
	if err == nil {
		t.Fatal("expected an error when SMTPFrom/SMTPAccount has no '@'")
	}
	if !strings.Contains(err.Error(), "invalid SMTP account") {
		t.Errorf("err = %v, want it to mention an invalid SMTP account", err)
	}
	// Confirms the "for compatibility" fallback in SendEmail actually ran.
	if SMTPFrom != "not-an-email-address" {
		t.Errorf("SMTPFrom = %q, want it backfilled from SMTPAccount", SMTPFrom)
	}
}

func TestSendEmail_UnconfiguredServerErrors(t *testing.T) {
	r5coreSnapshotSMTP(t)
	SMTPFrom = "sender@example.com"
	SMTPAccount = ""
	SMTPServer = ""

	err := SendEmail("subject", "someone@example.com", "body")
	if err == nil {
		t.Fatal("expected an error when neither SMTPServer nor SMTPAccount is configured")
	}
	if !strings.Contains(err.Error(), "SMTP 服务器未配置") {
		t.Errorf("err = %v, want the unconfigured-server message", err)
	}
}

func TestSendEmail_TLSPortDialFailurePropagates(t *testing.T) {
	r5coreSnapshotSMTP(t)
	// SMTPPort==465 alone selects the TLS branch (regardless of SMTPSSLEnabled);
	// nothing listens on the SMTPS port in this sandbox, so the tls.Dial fails
	// fast and deterministically.
	SMTPFrom = "sender@example.com"
	SMTPAccount = "sender@example.com"
	SMTPToken = "token"
	SMTPServer = "127.0.0.1"
	SMTPPort = 465
	SMTPSSLEnabled = false
	SystemName = "TestSystem"

	err := SendEmail("subject", "someone@example.com", "body")
	if err == nil {
		t.Fatal("expected a dial error against a closed local port on the 465/TLS branch")
	}
}

func TestSendEmail_PlainSMTPDialFailurePropagates(t *testing.T) {
	r5coreSnapshotSMTP(t)
	host, port := r5coreClosedPort(t)
	SMTPFrom = "sender@example.com"
	SMTPAccount = "sender@example.com"
	SMTPToken = "token"
	SMTPServer = host
	SMTPPort = port
	SMTPSSLEnabled = false
	SystemName = "TestSystem"

	err := SendEmail("subject", "someone@example.com", "body")
	if err == nil {
		t.Fatal("expected a dial error against a closed local port on the plain smtp.SendMail branch")
	}
}
