package common

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTPServer speaks just enough SMTP on the plain (non-TLS) path for
// SendEmail to complete: greeting, EHLO, optional AUTH, MAIL, RCPT, DATA, QUIT.
// It records every command line it saw and the DATA body it received, so a
// test can assert what was actually transmitted rather than only that
// SendEmail returned nil.
type fakeSMTPServer struct {
	advertiseAuth bool

	mu   sync.Mutex
	cmds []string
	body string
}

func (f *fakeSMTPServer) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cmds...)
}

func (f *fakeSMTPServer) sawCommand(prefix string) bool {
	for _, c := range f.commands() {
		if strings.HasPrefix(strings.ToUpper(c), prefix) {
			return true
		}
	}
	return false
}

func (f *fakeSMTPServer) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	w := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	w("220 fake ESMTP ready")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.mu.Lock()
		f.cmds = append(f.cmds, line)
		f.mu.Unlock()
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			if f.advertiseAuth {
				w("250-fake")
				w("250-AUTH PLAIN LOGIN")
				w("250 8BITMIME")
			} else {
				w("250-fake")
				w("250 8BITMIME")
			}
		case strings.HasPrefix(upper, "AUTH"):
			w("235 2.7.0 authentication successful")
		case strings.HasPrefix(upper, "MAIL"), strings.HasPrefix(upper, "RCPT"):
			w("250 ok")
		case strings.HasPrefix(upper, "DATA"):
			w("354 end data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				l, derr := r.ReadString('\n')
				if derr != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				body.WriteString(l)
			}
			f.mu.Lock()
			f.body = body.String()
			f.mu.Unlock()
			w("250 queued")
		case strings.HasPrefix(upper, "QUIT"):
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

// withFakeSMTP points the package SMTP globals at the fake server on the plain
// path (port != 465, SSL off) and restores them on cleanup.
func withFakeSMTP(t *testing.T, advertiseAuth bool) *fakeSMTPServer {
	t.Helper()
	srv := &fakeSMTPServer{advertiseAuth: advertiseAuth}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go srv.serve(c)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)

	prevServer, prevPort, prevSSL := SMTPServer, SMTPPort, SMTPSSLEnabled
	prevAccount, prevFrom, prevToken := SMTPAccount, SMTPFrom, SMTPToken
	prevDial, prevSend := smtpDialTimeout, smtpSendBudget
	SMTPServer = addr.IP.String()
	SMTPPort = addr.Port
	SMTPSSLEnabled = false
	SMTPAccount = "relay@example.invalid"
	SMTPFrom = "relay@example.invalid"
	SMTPToken = "secret-token"
	smtpDialTimeout = 2 * time.Second
	smtpSendBudget = 5 * time.Second
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
		SMTPServer, SMTPPort, SMTPSSLEnabled = prevServer, prevPort, prevSSL
		SMTPAccount, SMTPFrom, SMTPToken = prevAccount, prevFrom, prevToken
		smtpDialTimeout, smtpSendBudget = prevDial, prevSend
	})
	return srv
}

// TestSendEmail_DeliversThroughTheBoundedTransport is the happy-path oracle
// for the hand-rolled transport in sendMailBounded: the message reaches the
// server with the headers SendEmail builds, after an AUTH exchange.
func TestSendEmail_DeliversThroughTheBoundedTransport(t *testing.T) {
	srv := withFakeSMTP(t, true)

	if err := SendEmail("quota low", "customer@example.invalid", "<p>body</p>"); err != nil {
		t.Fatalf("SendEmail against a cooperative server: %v", err)
	}
	if !srv.sawCommand("AUTH") {
		t.Errorf("server never saw AUTH; commands: %v", srv.commands())
	}
	if !srv.sawCommand("QUIT") {
		t.Errorf("server never saw QUIT on the plain path; commands: %v", srv.commands())
	}
	srv.mu.Lock()
	body := srv.body
	srv.mu.Unlock()
	for _, needle := range []string{"To: customer@example.invalid", "Subject: ", "Content-Type: text/html", "<p>body</p>"} {
		if !strings.Contains(body, needle) {
			t.Errorf("delivered body lacks %q:\n%s", needle, body)
		}
	}
}

// TestSendEmail_RefusesToSendUnauthenticatedWhenAuthIsConfigured pins
// net/smtp.SendMail's rule that the bounded transport inherited: credentials
// are configured, the server advertises no AUTH, so nothing is sent. Mutation
// that turns this red: make sendMailBounded skip AUTH when the extension is
// absent (the message then goes out unauthenticated and the test sees MAIL).
func TestSendEmail_RefusesToSendUnauthenticatedWhenAuthIsConfigured(t *testing.T) {
	srv := withFakeSMTP(t, false)

	err := SendEmail("quota low", "customer@example.invalid", "body")
	if err == nil {
		t.Fatal("SendEmail returned nil against a server with no AUTH while credentials are configured")
	}
	if !strings.Contains(err.Error(), "AUTH") {
		t.Errorf("error %q does not name the missing AUTH extension", err.Error())
	}
	if srv.sawCommand("MAIL") {
		t.Errorf("MAIL FROM was sent without authentication; commands: %v", srv.commands())
	}
}
