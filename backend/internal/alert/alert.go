// Package alert sends a short message to a person's phone: to a Telegram bot, to a webhook (n8n, Make, a
// WhatsApp gateway of your own...) or to both. The server only makes outgoing requests.
//
// A message has a level. Info is good news (a backup finished, something recovered), Warning is something to
// look at soon, Critical is something that needs a person now. Messages below the configured minimum level
// are dropped. Secrets never appear in an error or a log line: only the name of the channel and a status.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Level says how urgent a message is.
type Level int

const (
	Info Level = iota
	Warning
	Critical
)

func (l Level) String() string {
	switch l {
	case Warning:
		return "warning"
	case Critical:
		return "critical"
	}
	return "info"
}

func (l Level) icon() string {
	switch l {
	case Warning:
		return "⚠️"
	case Critical:
		return "\U0001f6a8"
	}
	return "✅"
}

// ParseLevel reads info, warning or critical (any case). Empty is info.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return Info, nil
	case "warning", "warn":
		return Warning, nil
	case "critical", "crit":
		return Critical, nil
	}
	return Info, fmt.Errorf("level must be info, warning or critical, got %q", s)
}

const telegramAPI = "https://api.telegram.org"

// Notifier delivers messages. The zero value and a nil *Notifier are valid and send nothing.
type Notifier struct {
	Server string // shown in every message so several servers can share one chat: "staging", "production"
	Min    Level

	token, chat, webhook string
	telegramBase         string // tests only; empty means Telegram itself
	client               *http.Client
}

// Config holds the settings, normally read from the environment by FromEnv.
type Config struct {
	Server         string
	TelegramToken  string
	TelegramChatID string
	WebhookURL     string
	MinLevel       string
}

// New checks the settings. Nothing configured gives a notifier that is off; a half-filled Telegram setup or a
// URL that cannot be a webhook is an error, so a typo fails at start-up and not on the night it matters.
func New(c Config) (*Notifier, error) {
	n := &Notifier{
		Server: strings.TrimSpace(c.Server), token: strings.TrimSpace(c.TelegramToken),
		chat: strings.TrimSpace(c.TelegramChatID), webhook: strings.TrimSpace(c.WebhookURL),
		client: &http.Client{Timeout: 10 * time.Second},
	}
	if n.Server == "" {
		n.Server = "portal"
	}
	var err error
	if n.Min, err = ParseLevel(c.MinLevel); err != nil {
		return nil, fmt.Errorf("ALERT_MIN_LEVEL: %w", err)
	}
	if (n.token == "") != (n.chat == "") {
		return nil, errors.New("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID must be set together")
	}
	if n.webhook != "" {
		u, err := url.Parse(n.webhook)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, errors.New("ALERT_WEBHOOK_URL must be a full http(s) URL")
		}
	}
	return n, nil
}

// FromEnv reads ALERT_SERVER_NAME, TELEGRAM_BOT_TOKEN, TELEGRAM_CHAT_ID, ALERT_WEBHOOK_URL and ALERT_MIN_LEVEL.
func FromEnv(getenv func(string) string) (*Notifier, error) {
	return New(Config{
		Server: getenv("ALERT_SERVER_NAME"), TelegramToken: getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChatID: getenv("TELEGRAM_CHAT_ID"), WebhookURL: getenv("ALERT_WEBHOOK_URL"),
		MinLevel: getenv("ALERT_MIN_LEVEL"),
	})
}

// Enabled reports whether any channel is configured.
func (n *Notifier) Enabled() bool { return n != nil && (n.token != "" || n.webhook != "") }

// Channels names the configured channels, for a start-up log line.
func (n *Notifier) Channels() []string {
	var out []string
	if n != nil && n.token != "" {
		out = append(out, "telegram")
	}
	if n != nil && n.webhook != "" {
		out = append(out, "webhook")
	}
	return out
}

// Text is the message as a person reads it.
func (n *Notifier) Text(l Level, title, body string) string {
	t := fmt.Sprintf("%s [%s] %s", l.icon(), n.Server, title)
	if body = strings.TrimSpace(body); body != "" {
		t += "\n" + body
	}
	const max = 3500 // Telegram allows 4096
	if r := []rune(t); len(r) > max {
		t = string(r[:max]) + "..."
	}
	return t
}

// Send delivers one message to every channel. It returns nil when the notifier is off or the level is below
// the minimum; otherwise an error naming the channels that failed (never their URL or token).
func (n *Notifier) Send(ctx context.Context, l Level, title, body string) error {
	if !n.Enabled() || l < n.Min {
		return nil
	}
	text := n.Text(l, title, body)
	var errs []error
	if n.token != "" {
		if err := n.telegram(ctx, text); err != nil {
			errs = append(errs, err)
		}
	}
	if n.webhook != "" {
		if err := n.postWebhook(ctx, l, title, body, text); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (n *Notifier) telegram(ctx context.Context, text string) error {
	base := n.telegramBase
	if base == "" {
		base = telegramAPI
	}
	form := url.Values{"chat_id": {n.chat}, "text": {text}, "disable_web_page_preview": {"true"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+n.token+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("alert to telegram failed")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return n.do(req, "telegram")
}

func (n *Notifier) postWebhook(ctx context.Context, l Level, title, body, text string) error {
	payload, _ := json.Marshal(map[string]string{
		"level": l.String(), "server": n.Server, "title": title, "message": strings.TrimSpace(body),
		"text": text, "time": time.Now().UTC().Format(time.RFC3339),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhook, bytes.NewReader(payload))
	if err != nil {
		return errors.New("alert to webhook failed")
	}
	req.Header.Set("Content-Type", "application/json")
	return n.do(req, "webhook")
}

// do sends the request. The error text of net/http holds the URL, which holds the bot token or a secret path,
// so only the channel and the HTTP status are reported.
func (n *Notifier) do(req *http.Request, channel string) error {
	req.Header.Set("User-Agent", "cadportal-alert")
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("alert to %s failed", channel)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("alert to %s answered %d", channel, resp.StatusCode)
	}
	return nil
}

// Cooldown lets a key through at most once per interval, so a problem that lasts does not message you every
// minute. It is safe for concurrent use.
type Cooldown struct {
	Every time.Duration
	mu    sync.Mutex
	last  map[string]time.Time
}

// Allow reports whether key may send now, and if so starts its cooldown.
func (c *Cooldown) Allow(key string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		c.last = map[string]time.Time{}
	}
	if t, ok := c.last[key]; ok && now.Sub(t) < c.Every {
		return false
	}
	c.last[key] = now
	return true
}

// Reset forgets key, so its next message goes out at once (after a recovery).
func (c *Cooldown) Reset(key string) {
	c.mu.Lock()
	delete(c.last, key)
	c.mu.Unlock()
}

// Bytes writes a size for people: 1.5 MB, 320 KB.
func Bytes(n int64) string {
	const k = 1024.0
	f := float64(n)
	switch {
	case f >= k*k*k:
		return fmt.Sprintf("%.1f GB", f/(k*k*k))
	case f >= k*k:
		return fmt.Sprintf("%.1f MB", f/(k*k))
	case f >= k:
		return fmt.Sprintf("%.0f KB", f/k)
	}
	return fmt.Sprintf("%d B", n)
}
