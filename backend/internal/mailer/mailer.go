// Package mailer sends the few plain-text emails the portal needs (password reset links) through an SMTP
// server of the operator's choice. Nothing is sent unless SMTP is configured.
package mailer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// Sender is what the rest of the portal sees.
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// TLSMode says how the connection to the mail server is protected.
type TLSMode string

const (
	// STARTTLS upgrades a plain connection (port 587) and refuses to continue if the server cannot.
	STARTTLS TLSMode = "starttls"
	// Implicit is TLS from the first byte (port 465).
	Implicit TLSMode = "implicit"
	// None sends in the clear. Only for a mail catcher on the same machine; never for a real server.
	None TLSMode = "none"
)

type Config struct {
	Host, Username, Password, From string
	Port                           int
	TLS                            TLSMode
}

type SMTP struct {
	cfg  Config
	from mail.Address
}

// Validate says what is wrong with a configuration, in words an operator can act on.
func (c Config) Validate() error {
	var miss []string
	if strings.TrimSpace(c.Host) == "" {
		miss = append(miss, "SMTP_HOST")
	}
	if strings.TrimSpace(c.From) == "" {
		miss = append(miss, "SMTP_FROM")
	}
	if len(miss) > 0 {
		return fmt.Errorf("%s must be set to send email", strings.Join(miss, " and "))
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		return fmt.Errorf("SMTP_FROM is not a valid address: %w", err)
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("SMTP_PORT must be 1 to 65535, got %d", c.Port)
	}
	switch c.TLS {
	case STARTTLS, Implicit, None:
	default:
		return fmt.Errorf("SMTP_TLS must be starttls, implicit or none, got %q", c.TLS)
	}
	if (c.Username == "") != (c.Password == "") {
		return errors.New("SMTP_USERNAME and SMTP_PASSWORD must be set together")
	}
	return nil
}

func New(c Config) (*SMTP, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	from, _ := mail.ParseAddress(c.From)
	return &SMTP{cfg: c, from: *from}, nil
}

// clean refuses anything that could add headers or commands: line breaks in an address or subject.
func clean(s string) error {
	if strings.ContainsAny(s, "\r\n\x00") {
		return errors.New("line breaks are not allowed here")
	}
	return nil
}

// Message builds the RFC 5322 text of a plain UTF-8 email.
func (s *SMTP) message(to, subject, body string) string {
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := "localhost"
	if i := strings.LastIndex(s.from.Address, "@"); i >= 0 {
		domain = s.from.Address[i+1:]
	}
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", s.from.String())
	h("To", to)
	h("Subject", mimeWord(subject))
	h("Date", time.Now().UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("Content-Transfer-Encoding", "8bit")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	return b.String()
}

func mimeWord(s string) string {
	for _, r := range s {
		if r > 126 {
			return mimeEncode(s)
		}
	}
	return s
}

func (s *SMTP) Send(ctx context.Context, to, subject, body string) error {
	addr, err := mail.ParseAddress(to)
	if err != nil || addr.Address != to {
		return fmt.Errorf("mailer: %q is not a plain email address", to)
	}
	if err := clean(to); err != nil {
		return fmt.Errorf("mailer: address: %w", err)
	}
	if err := clean(subject); err != nil {
		return fmt.Errorf("mailer: subject: %w", err)
	}

	hostport := net.JoinHostPort(s.cfg.Host, fmt.Sprint(s.cfg.Port))
	d := net.Dialer{Timeout: 10 * time.Second}
	tlsCfg := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	if s.cfg.TLS == Implicit {
		conn, err = tls.DialWithDialer(&d, "tcp", hostport, tlsCfg)
	} else {
		conn, err = d.DialContext(ctx, "tcp", hostport)
	}
	if err != nil {
		return fmt.Errorf("mailer: connecting to %s: %w", s.cfg.Host, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mailer: %w", err)
	}
	defer c.Close()

	if s.cfg.TLS == STARTTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("mailer: %s does not offer STARTTLS; refusing to send in the clear (SMTP_TLS=none allows it for a local mail catcher)", s.cfg.Host)
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("mailer: starting TLS: %w", err)
		}
	}
	if s.cfg.Username != "" {
		// net/smtp refuses PLAIN auth over an unencrypted connection except to localhost.
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("mailer: signing in to %s: %w", s.cfg.Host, err)
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return fmt.Errorf("mailer: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("mailer: recipient refused: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mailer: %w", err)
	}
	if _, err := w.Write([]byte(s.message(to, subject, body))); err != nil {
		return fmt.Errorf("mailer: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer: %w", err)
	}
	return c.Quit()
}

// Memory is a Sender for tests that records what would have been sent.
type Memory struct {
	mu   sync.Mutex
	Sent []Mail
	Err  error
}

// Mails returns what was sent so far.
func (m *Memory) Mails() []Mail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Mail(nil), m.Sent...)
}

type Mail struct{ To, Subject, Body string }

func (m *Memory) Send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	m.Sent = append(m.Sent, Mail{to, subject, body})
	return nil
}
