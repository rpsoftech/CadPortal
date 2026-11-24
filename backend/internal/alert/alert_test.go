package alert

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]Level{"": Info, "INFO": Info, "warn": Warning, "Warning": Warning, "critical": Critical} {
		if got, err := ParseLevel(in); err != nil || got != want {
			t.Errorf("%q = %v, %v", in, got, err)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("accepted loud")
	}
}

func TestNewChecksTheSettings(t *testing.T) {
	if n, err := New(Config{}); err != nil || n.Enabled() {
		t.Fatalf("empty config: %v enabled=%v", err, n.Enabled())
	}
	bad := []Config{
		{TelegramToken: "t"}, {TelegramChatID: "1"}, {WebhookURL: "hooks.example/x"}, {WebhookURL: "ftp://x/y"}, {MinLevel: "loud"},
	}
	for _, c := range bad {
		if _, err := New(c); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	var nilN *Notifier
	if nilN.Enabled() || nilN.Send(context.Background(), Critical, "x", "y") != nil {
		t.Error("a nil notifier must be off and silent")
	}
}

func TestSendReachesTelegramAndTheWebhook(t *testing.T) {
	var tgPath, tgChat, tgText string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		q, _ := url.ParseQuery(string(b))
		tgPath, tgChat, tgText = r.URL.Path, q.Get("chat_id"), q.Get("text")
	}))
	defer tg.Close()
	var hook map[string]string
	wh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&hook)
	}))
	defer wh.Close()

	n, err := New(Config{Server: "staging", TelegramToken: "123:ABC", TelegramChatID: "-100", WebhookURL: wh.URL + "/secret-path"})
	if err != nil {
		t.Fatal(err)
	}
	n.telegramBase = tg.URL
	if err := n.Send(context.Background(), Critical, "Backup failed", "disk full"); err != nil {
		t.Fatal(err)
	}
	if tgPath != "/bot123:ABC/sendMessage" || tgChat != "-100" {
		t.Errorf("telegram got path %q chat %q", tgPath, tgChat)
	}
	if !strings.Contains(tgText, "[staging] Backup failed") || !strings.Contains(tgText, "disk full") {
		t.Errorf("telegram text = %q", tgText)
	}
	if hook["level"] != "critical" || hook["server"] != "staging" || hook["title"] != "Backup failed" || hook["message"] != "disk full" || hook["time"] == "" {
		t.Errorf("webhook payload = %v", hook)
	}
}

func TestSendDropsMessagesBelowTheMinimumLevel(t *testing.T) {
	var hits atomic.Int32
	wh := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer wh.Close()
	n, _ := New(Config{WebhookURL: wh.URL, MinLevel: "warning"})
	_ = n.Send(context.Background(), Info, "fine", "")
	_ = n.Send(context.Background(), Warning, "look", "")
	_ = n.Send(context.Background(), Critical, "now", "")
	if hits.Load() != 2 {
		t.Errorf("delivered %d, want 2", hits.Load())
	}
}

func TestErrorsNeverContainTheTokenOrTheWebhookPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	n, _ := New(Config{TelegramToken: "123:SECRETTOKEN", TelegramChatID: "1", WebhookURL: srv.URL + "/hook-SECRETPATH"})
	n.telegramBase = srv.URL
	err := n.Send(context.Background(), Critical, "x", "")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Errorf("secret leaked: %v", err)
	}
	srv.Close()
	err = n.Send(context.Background(), Critical, "x", "")
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("connection error leaked: %v", err)
	}
}

func TestLongMessagesAreCut(t *testing.T) {
	n, _ := New(Config{WebhookURL: "http://x.example/h"})
	if got := n.Text(Info, "t", strings.Repeat("a", 9000)); len([]rune(got)) > 3600 {
		t.Errorf("text has %d runes", len([]rune(got)))
	}
}

func TestCooldown(t *testing.T) {
	c := Cooldown{Every: time.Hour}
	now := time.Now()
	if !c.Allow("a", now) || c.Allow("a", now.Add(time.Minute)) || !c.Allow("b", now) {
		t.Error("first send, repeat and other key misjudged")
	}
	if !c.Allow("a", now.Add(2*time.Hour)) {
		t.Error("not allowed again after the interval")
	}
	c.Reset("a")
	if !c.Allow("a", now.Add(2*time.Hour+time.Second)) {
		t.Error("not allowed after reset")
	}
}

func TestBytes(t *testing.T) {
	for in, want := range map[int64]string{12: "12 B", 2048: "2 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB"} {
		if got := Bytes(in); got != want {
			t.Errorf("%d = %q, want %q", in, got, want)
		}
	}
}

func TestErrorWatchSendsOneMessagePerBurst(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	w := &ErrorWatch{Threshold: 3, Window: 5 * time.Minute, Quiet: 30 * time.Minute, nowFn: func() time.Time { return now }}
	for i := 0; i < 2; i++ {
		if msg := w.Record("GET /x", "r1"); msg != "" {
			t.Fatalf("message before the threshold: %q", msg)
		}
		now = now.Add(time.Second)
	}
	msg := w.Record("GET /x", "r3")
	if !strings.Contains(msg, "3 server errors") || !strings.Contains(msg, "request id r3") {
		t.Fatalf("msg = %q", msg)
	}
	for i := 0; i < 5; i++ {
		now = now.Add(time.Second)
		if w.Record("GET /x", "again") != "" {
			t.Fatal("repeated while quiet")
		}
	}
	now = now.Add(time.Hour) // old errors age out: a new burst needs the threshold again
	if w.Record("GET /x", "late") != "" {
		t.Error("one error after an hour is not a burst")
	}
}

func TestErrorWatchMiddlewareOnlyCountsServerErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var mu sync.Mutex
	var sent []string
	done := make(chan struct{}, 1)
	n, _ := New(Config{WebhookURL: "http://x.example/h"})
	w := &ErrorWatch{N: n, Threshold: 2, sendFn: func(_ context.Context, l Level, title, body string) error {
		mu.Lock()
		sent = append(sent, l.String()+"|"+title)
		mu.Unlock()
		done <- struct{}{}
		return nil
	}}
	r := gin.New()
	r.Use(w.Middleware())
	r.GET("/bad", func(c *gin.Context) { c.Status(500) })
	r.GET("/missing", func(c *gin.Context) { c.Status(404) })
	for _, p := range []string{"/missing", "/missing", "/bad", "/bad"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("no message after two server errors")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 || sent[0] != "warning|The API is returning errors" {
		t.Errorf("sent = %v", sent)
	}
}

func TestWatchDatabaseReportsAnOutageOnceAndTheRecovery(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]string
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		got = append(got, p["level"]+"|"+p["title"])
		mu.Unlock()
	}))
	defer srv.Close()
	n, _ := New(Config{WebhookURL: srv.URL})
	var calls atomic.Int32
	ping := func(context.Context) error {
		switch c := calls.Add(1); {
		case c >= 2 && c <= 5:
			return errors.New("down")
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { WatchDatabase(ctx, n, 5*time.Millisecond, ping); close(done) }()
	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		l := len(got)
		mu.Unlock()
		if l >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("got %v", got)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if got[0] != "critical|The database is not answering" || got[1] != "info|The database is back" || len(got) != 2 {
		t.Errorf("messages = %v", got)
	}
}
