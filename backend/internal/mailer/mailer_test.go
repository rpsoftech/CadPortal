package mailer

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a tiny SMTP server that records one message. It offers no STARTTLS, like a local mail catcher.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	cmds []string
	data string
	auth string
}

func newFake(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(s string) { c.Write([]byte(s + "\r\n")) }
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.mu.Lock()
		f.cmds = append(f.cmds, line)
		f.mu.Unlock()
		switch u := strings.ToUpper(line); {
		case strings.HasPrefix(u, "EHLO"):
			say("250-fake")
			say("250 AUTH PLAIN")
		case strings.HasPrefix(u, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			f.mu.Lock()
			f.auth = strings.ReplaceAll(string(raw), "\x00", "|")
			f.mu.Unlock()
			say("235 ok")
		case strings.HasPrefix(u, "MAIL FROM"), strings.HasPrefix(u, "RCPT TO"):
			say("250 ok")
		case u == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			say("250 queued")
		case u == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (f *fakeSMTP) got() (cmds []string, data, auth string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cmds...), f.data, f.auth
}

func cfg(port int) Config {
	return Config{Host: "127.0.0.1", Port: port, From: "CadPortal <portal@example.com>", TLS: None}
}

func TestSendDeliversAWellFormedMessage(t *testing.T) {
	f := newFake(t)
	c := cfg(f.port())
	c.Username, c.Password = "mailer", "pw"
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), "asha@example.com", "Reset your password", "Hello\nline two"); err != nil {
		t.Fatal(err)
	}
	cmds, data, auth := f.got()
	joined := strings.Join(cmds, "\n")
	if !strings.Contains(joined, "MAIL FROM:<portal@example.com>") || !strings.Contains(joined, "RCPT TO:<asha@example.com>") {
		t.Errorf("envelope: %s", joined)
	}
	if auth != "|mailer|pw" {
		t.Errorf("auth = %q", auth)
	}
	for _, want := range []string{"From: \"CadPortal\" <portal@example.com>", "To: asha@example.com", "Subject: Reset your password", "Content-Type: text/plain; charset=utf-8", "Message-ID: <", "\r\n\r\nHello\r\nline two"} {
		if !strings.Contains(data, want) {
			t.Errorf("message lacks %q:\n%s", want, data)
		}
	}
}

func TestSubjectWithAccentsIsEncoded(t *testing.T) {
	f := newFake(t)
	s, _ := New(cfg(f.port()))
	if err := s.Send(context.Background(), "a@example.com", "Réinitialiser", "x"); err != nil {
		t.Fatal(err)
	}
	_, data, _ := f.got()
	if !strings.Contains(data, "Subject: =?UTF-8?B?") {
		t.Errorf("subject not encoded:\n%s", data)
	}
}

func TestSendRefusesAnythingThatCouldInjectHeadersOrCommands(t *testing.T) {
	f := newFake(t)
	s, _ := New(cfg(f.port()))
	for _, to := range []string{"a@example.com\r\nBcc: evil@example.com", "a@example.com>\r\nRCPT TO:<x@y", "Name <a@example.com>", ""} {
		if err := s.Send(context.Background(), to, "s", "b"); err == nil {
			t.Errorf("recipient %q accepted", to)
		}
	}
	if err := s.Send(context.Background(), "a@example.com", "hi\r\nBcc: evil@example.com", "b"); err == nil {
		t.Error("a subject with a line break accepted")
	}
	if cmds, _, _ := f.got(); len(cmds) != 0 {
		t.Errorf("nothing may reach the server for a refused message: %v", cmds)
	}
}

func TestSTARTTLSIsRequiredUnlessTurnedOff(t *testing.T) {
	f := newFake(t) // offers no STARTTLS
	c := cfg(f.port())
	c.TLS = STARTTLS
	s, _ := New(c)
	err := s.Send(context.Background(), "a@example.com", "s", "b")
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("a server without STARTTLS must be refused: %v", err)
	}
	if _, data, _ := f.got(); data != "" {
		t.Error("the message must not be sent in the clear")
	}
}

func TestSendFailsClearlyWhenTheServerIsDown(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	s, _ := New(cfg(port))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Send(ctx, "a@example.com", "s", "b"); err == nil || !strings.Contains(err.Error(), "connecting") {
		t.Errorf("got %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	ok := Config{Host: "smtp.example.com", Port: 587, From: "portal@example.com", TLS: STARTTLS}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(c *Config){
		"SMTP_HOST":            func(c *Config) { c.Host = "" },
		"SMTP_FROM":            func(c *Config) { c.From = "" },
		"not a valid address":  func(c *Config) { c.From = "nonsense" },
		"SMTP_PORT":            func(c *Config) { c.Port = 70000 },
		"SMTP_TLS":             func(c *Config) { c.TLS = "ssl" },
		"must be set together": func(c *Config) { c.Username = "u" },
	}
	for want, mutate := range cases {
		c := ok
		mutate(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
	_ = strconv.Itoa
}
