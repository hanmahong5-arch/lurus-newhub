package common

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/smtp"
	"slices"
	"strings"
	"time"
)

// SMTP transport budgets (cycle 12 L7). Email is the DEFAULT notification
// channel — internal/app/user_notify.go NotifyUser falls back to it whenever the
// user set no NotifyType — and it was the one outbound dependency here with no
// bound whatsoever: tls.Dial and smtp.SendMail both block on the socket with no
// deadline, so an SMTP host that accepts the connection and then goes quiet
// parked the notifying goroutine (which runs inside AsyncGo, after the relay has
// answered) for the life of the process.
//
// smtpSendBudget is a deadline on the connection, so it covers the whole
// conversation — greeting, EHLO, STARTTLS, AUTH, MAIL/RCPT/DATA — not one round
// trip. net/smtp has no context-aware entry point; a connection deadline is the
// only instrument it leaves. Vars, not consts, so email_timeout_test.go can
// shorten them; nothing in production writes them.
var (
	smtpDialTimeout = 10 * time.Second
	smtpSendBudget  = 30 * time.Second
)

// dialSMTP opens a bounded connection and returns a client speaking on it.
// implicitTLS is the port-465 shape (TLS from the first byte); the STARTTLS
// shape is handled by the caller after the greeting.
func dialSMTP(addr, serverName string, implicitTLS bool, tlsConfig *tls.Config) (*smtp.Client, error) {
	conn, err := net.DialTimeout("tcp", addr, smtpDialTimeout)
	if err != nil {
		return nil, err
	}
	if derr := conn.SetDeadline(time.Now().Add(smtpSendBudget)); derr != nil {
		_ = conn.Close()
		return nil, derr
	}
	if implicitTLS {
		tlsConn := tls.Client(conn, tlsConfig)
		if herr := tlsConn.Handshake(); herr != nil {
			_ = conn.Close()
			return nil, herr
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, serverName)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return client, nil
}

// sendMailBounded is net/smtp's SendMail with a bounded dial and a deadline on
// the connection. The sequence — EHLO, opportunistic STARTTLS, AUTH when the
// server advertises it, MAIL/RCPT/DATA, QUIT — is the one smtp.SendMail runs;
// it is spelled out here only because the library offers no way to hand it a
// connection that someone else dialled.
func sendMailBounded(addr, serverName string, auth smtp.Auth, from string, to []string, msg []byte) error {
	client, err := dialSMTP(addr, serverName, false, nil)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if terr := client.StartTLS(&tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}); terr != nil {
			return terr
		}
	}
	if auth != nil {
		if ok, _ := client.Extension("AUTH"); ok {
			if aerr := client.Auth(auth); aerr != nil {
				return aerr
			}
		}
	}
	if err = client.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err = client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(msg); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// sendMailImplicitTLS is the port-465 / SMTP_SSL_ENABLED path. It deliberately
// does not send QUIT: the message is already committed when the DATA writer
// closes, and the pre-cycle-12 code ended the same way, with Close() only.
func sendMailImplicitTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, // #nosec G402 — SMTP TLS verify is disabled for operator-controlled internal relay; cert verification can be re-enabled via SMTP_TLS_VERIFY env once infra provides a valid cert.
		ServerName:         SMTPServer,
	}
	client, err := dialSMTP(addr, SMTPServer, true, tlsConfig)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if err = client.Auth(auth); err != nil {
		return err
	}
	if err = client.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err = client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

func generateMessageID() (string, error) {
	split := strings.Split(SMTPFrom, "@")
	if len(split) < 2 {
		return "", fmt.Errorf("invalid SMTP account")
	}
	domain := strings.Split(SMTPFrom, "@")[1]
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), GetRandomString(12), domain), nil
}

func SendEmail(subject string, receiver string, content string) error {
	if SMTPFrom == "" { // for compatibility
		SMTPFrom = SMTPAccount
	}
	id, err2 := generateMessageID()
	if err2 != nil {
		return err2
	}
	if SMTPServer == "" && SMTPAccount == "" {
		return fmt.Errorf("SMTP 服务器未配置")
	}
	encodedSubject := fmt.Sprintf("=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(subject)))
	mail := []byte(fmt.Sprintf("To: %s\r\n"+
		"From: %s <%s>\r\n"+
		"Subject: %s\r\n"+
		"Date: %s\r\n"+
		"Message-ID: %s\r\n"+ // 添加 Message-ID 头
		"Content-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n",
		receiver, SystemName, SMTPFrom, encodedSubject, time.Now().Format(time.RFC1123Z), id, content))
	auth := smtp.PlainAuth("", SMTPAccount, SMTPToken, SMTPServer)
	addr := fmt.Sprintf("%s:%d", SMTPServer, SMTPPort)
	to := strings.Split(receiver, ";")
	var err error
	if SMTPPort == 465 || SMTPSSLEnabled {
		err = sendMailImplicitTLS(addr, auth, SMTPFrom, to, mail)
	} else if isOutlookServer(SMTPAccount) || slices.Contains(EmailLoginAuthServerList, SMTPServer) {
		auth = LoginAuth(SMTPAccount, SMTPToken)
		err = sendMailBounded(addr, SMTPServer, auth, SMTPFrom, to, mail)
	} else {
		err = sendMailBounded(addr, SMTPServer, auth, SMTPFrom, to, mail)
	}
	if err != nil {
		// Now reached for the implicit-TLS branch too: its errors used to land in
		// a shadowed err and never be logged.
		SysError(fmt.Sprintf("failed to send email to %s: %v", receiver, err))
	}
	return err
}
