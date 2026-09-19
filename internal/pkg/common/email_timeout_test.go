package common

// email_timeout_test.go — cycle 12 L7 oracle for the SMTP path.
//
// Email is the DEFAULT notification channel: internal/app/user_notify.go's
// NotifyUser falls back to it whenever the user set no NotifyType, so it is the
// branch most deployments actually use. It was also the only outbound dependency
// in the notify family with no bound at all — tls.Dial and smtp.SendMail block
// on the socket with no deadline. An SMTP host that accepts the connection and
// then says nothing (a firewalled relay is exactly this shape) therefore parked
// the goroutine SendEmail runs on, and that goroutine is an AsyncGo one started
// after a relay has already answered the customer.
//
// Both entry shapes are driven: the STARTTLS/plain path (sendMailBounded) and
// the implicit-TLS port-465 path (sendMailImplicitTLS).

import (
	"net"
	"sync"
	"testing"
	"time"
)

// silentSMTPListener accepts TCP connections and never writes the SMTP greeting.
// Accepted connections are parked so the kernel does not reset them, and closed
// on cleanup.
func silentSMTPListener(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
	})
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

// withSilentSMTP points the package SMTP globals at a listener that never
// answers, and shortens the budgets for the duration of the test.
func withSilentSMTP(t *testing.T, ssl bool) {
	t.Helper()
	host, port := silentSMTPListener(t)

	prevServer, prevPort, prevSSL := SMTPServer, SMTPPort, SMTPSSLEnabled
	prevAccount, prevFrom, prevToken := SMTPAccount, SMTPFrom, SMTPToken
	prevDial, prevSend := smtpDialTimeout, smtpSendBudget

	SMTPServer = host
	SMTPPort = port
	SMTPSSLEnabled = ssl
	SMTPAccount = "relay@example.invalid"
	SMTPFrom = "relay@example.invalid"
	SMTPToken = "t"
	smtpDialTimeout = 300 * time.Millisecond
	smtpSendBudget = 300 * time.Millisecond

	t.Cleanup(func() {
		SMTPServer, SMTPPort, SMTPSSLEnabled = prevServer, prevPort, prevSSL
		SMTPAccount, SMTPFrom, SMTPToken = prevAccount, prevFrom, prevToken
		smtpDialTimeout, smtpSendBudget = prevDial, prevSend
	})
}

func assertSendEmailReturns(t *testing.T, label string) {
	t.Helper()
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- SendEmail("quota low", "customer@example.invalid", "body") }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("%s: an SMTP server that never answers must produce an error", label)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("%s: SendEmail returned after %v with a 300ms budget", label, elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("%s: SendEmail did not return within 3s against an SMTP host that accepts and "+
			"stays silent — the connection carries no deadline, so this goroutine would never "+
			"come back", label)
	}
}

// TestSendEmail_SilentServerIsBoundedOnThePlainPath covers the default
// deployment shape: port 587, STARTTLS offered or not, no implicit TLS.
func TestSendEmail_SilentServerIsBoundedOnThePlainPath(t *testing.T) {
	withSilentSMTP(t, false)
	assertSendEmailReturns(t, "plain/STARTTLS")
}

// TestSendEmail_SilentServerIsBoundedOnTheImplicitTLSPath covers the
// SMTP_SSL_ENABLED / port 465 shape, where the block used to be inside tls.Dial.
func TestSendEmail_SilentServerIsBoundedOnTheImplicitTLSPath(t *testing.T) {
	withSilentSMTP(t, true)
	assertSendEmailReturns(t, "implicit TLS")
}

// TestSMTPBudgetDefaults pins the production numbers so shortening them in a
// test cannot quietly become the shipped value.
func TestSMTPBudgetDefaults(t *testing.T) {
	if smtpDialTimeout != 10*time.Second {
		t.Errorf("smtpDialTimeout = %v, want 10s", smtpDialTimeout)
	}
	if smtpSendBudget != 30*time.Second {
		t.Errorf("smtpSendBudget = %v, want 30s", smtpSendBudget)
	}
}
