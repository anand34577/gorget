// Package mailer sends notification email over SMTP (implicit TLS, STARTTLS or,
// for a relay on the same machine, plain).
package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Security modes.
const (
	SecurityTLS      = "tls"      // implicit TLS, usually port 465
	SecuritySTARTTLS = "starttls" // upgrade after connecting, usually port 587
	SecurityNone     = "none"     // only for a relay on localhost or a trusted private network
)

// Config is an SMTP account.
type Config struct {
	Host     string
	Port     int
	Security string
	Username string
	Password string
	From     string // address
	FromName string
	// SkipVerify accepts any server certificate (self-signed relays only).
	SkipVerify bool
	// HeloName is the name this server introduces itself with (its public host name).
	HeloName string
}

// Message is one email. Text is required; HTML is optional.
type Message struct {
	To      []string
	Subject string
	Text    string
	HTML    string
}

// Validate checks the account settings.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Host) == "" || strings.ContainsAny(c.Host, " /\r\n") {
		return errors.New("SMTP server name is required (for example smtp.example.com)")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("SMTP port must be between 1 and 65535 (usually 587 or 465)")
	}
	switch c.Security {
	case SecurityTLS, SecuritySTARTTLS, SecurityNone:
	default:
		return errors.New("security must be tls, starttls or none")
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		return fmt.Errorf("sender address %q is not a valid email address", c.From)
	}
	if strings.ContainsAny(c.FromName, "\r\n") {
		return errors.New("sender name must be on one line")
	}
	return nil
}

// Send delivers msg. It gives up after timeout.
func Send(ctx context.Context, c Config, msg Message) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if len(msg.To) == 0 {
		return errors.New("no recipients")
	}
	var rcpts []string
	for _, to := range msg.To {
		a, err := mail.ParseAddress(to)
		if err != nil {
			return fmt.Errorf("recipient %q is not a valid email address", to)
		}
		rcpts = append(rcpts, a.Address)
	}
	body, err := build(c, rcpts, msg)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	dialer := &net.Dialer{Deadline: deadline}
	tlsCfg := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.SkipVerify} //nolint:gosec // explicit opt-in for self-signed relays

	var conn net.Conn
	if c.Security == SecurityTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	_ = conn.SetDeadline(deadline)
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP greeting from %s: %w", addr, err)
	}
	defer cl.Close()
	if err := cl.Hello(helloName(c.HeloName)); err != nil {
		return err
	}
	if c.Security == SecuritySTARTTLS {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return errors.New("the server doesn't offer STARTTLS; choose TLS (port 465) or check the port")
		}
		if err := cl.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if c.Username != "" {
		if c.Security == SecurityNone && !isLocal(c.Host) {
			return errors.New("refusing to send the SMTP password without encryption; choose TLS or STARTTLS")
		}
		if err := cl.Auth(plainAuth{c.Username, c.Password, c.Host}); err != nil {
			return fmt.Errorf("sign-in to the SMTP server failed: %w", err)
		}
	}
	from, _ := mail.ParseAddress(c.From)
	if err := cl.Mail(from.Address); err != nil {
		return fmt.Errorf("sender rejected: %w", err)
	}
	for _, r := range rcpts {
		if err := cl.Rcpt(r); err != nil {
			return fmt.Errorf("recipient %s rejected: %w", r, err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

func helloName(n string) string {
	if n == "" || strings.ContainsAny(n, " \r\n") {
		return "localhost"
	}
	return n
}

func isLocal(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// plainAuth is PLAIN authentication. Unlike smtp.PlainAuth it also works after
// implicit TLS (smtp.PlainAuth only trusts STARTTLS); Send never reaches it on an
// unencrypted connection to a remote host.
type plainAuth struct{ user, pass, host string }

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}

func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
}

func build(c Config, to []string, msg Message) ([]byte, error) {
	if strings.ContainsAny(msg.Subject, "\r\n") {
		return nil, errors.New("subject must be on one line")
	}
	var b bytes.Buffer
	from := (&mail.Address{Name: c.FromName, Address: mustAddr(c.From)}).String()
	hdr := textproto.MIMEHeader{}
	hdr.Set("From", from)
	hdr.Set("To", strings.Join(to, ", "))
	hdr.Set("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	hdr.Set("Date", time.Now().Format(time.RFC1123Z))
	hdr.Set("Message-ID", messageID(c.From))
	hdr.Set("MIME-Version", "1.0")
	hdr.Set("Auto-Submitted", "auto-generated")

	mw := multipart.NewWriter(&b)
	hdr.Set("Content-Type", "multipart/alternative; boundary="+mw.Boundary())
	for _, k := range []string{"From", "To", "Subject", "Date", "Message-ID", "MIME-Version", "Auto-Submitted", "Content-Type"} {
		fmt.Fprintf(&b, "%s: %s\r\n", k, hdr.Get(k))
	}
	b.WriteString("\r\n")
	if err := part(mw, "text/plain; charset=utf-8", msg.Text); err != nil {
		return nil, err
	}
	if msg.HTML != "" {
		if err := part(mw, "text/html; charset=utf-8", msg.HTML); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func part(mw *multipart.Writer, ctype, content string) error {
	w, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {ctype}, "Content-Transfer-Encoding": {"quoted-printable"}})
	if err != nil {
		return err
	}
	qw := quotedprintable.NewWriter(w)
	if _, err := qw.Write([]byte(content)); err != nil {
		return err
	}
	return qw.Close()
}

func mustAddr(s string) string {
	if a, err := mail.ParseAddress(s); err == nil {
		return a.Address
	}
	return s
}

func messageID(from string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	domain := "gorget.local"
	if i := strings.LastIndex(mustAddr(from), "@"); i >= 0 {
		domain = mustAddr(from)[i+1:]
	}
	return "<" + hex.EncodeToString(b) + "@" + domain + ">"
}
