package alert

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ErrorWatch turns a burst of server errors (HTTP 5xx) into one warning. It counts the errors of the last
// Window; at Threshold it sends one message, then stays quiet for Quiet so a long outage is one message and
// not hundreds.
type ErrorWatch struct {
	N         *Notifier
	Window    time.Duration // default 5 minutes
	Threshold int           // default 5 errors
	Quiet     time.Duration // default 30 minutes

	mu     sync.Mutex
	times  []time.Time
	cool   Cooldown
	nowFn  func() time.Time
	sendFn func(ctx context.Context, l Level, title, body string) error
}

func (w *ErrorWatch) defaults() (time.Duration, int, time.Duration) {
	win, thr, quiet := w.Window, w.Threshold, w.Quiet
	if win <= 0 {
		win = 5 * time.Minute
	}
	if thr <= 0 {
		thr = 5
	}
	if quiet <= 0 {
		quiet = 30 * time.Minute
	}
	return win, thr, quiet
}

// Record notes one server error and returns the message to send, or "" when no message is due.
func (w *ErrorWatch) Record(route, requestID string) string {
	win, thr, quiet := w.defaults()
	now := time.Now()
	if w.nowFn != nil {
		now = w.nowFn()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	keep := w.times[:0]
	for _, t := range w.times {
		if now.Sub(t) < win {
			keep = append(keep, t)
		}
	}
	w.times = append(keep, now)
	if len(w.times) < thr {
		return ""
	}
	w.cool.Every = quiet
	if !w.cool.Allow("5xx", now) {
		return ""
	}
	return fmt.Sprintf("%d server errors in the last %s. Latest: %s (request id %s). Find it with that id in the API log.",
		len(w.times), win.Round(time.Minute), route, requestID)
}

// Middleware watches the status of every response. The message is sent in the background, so a slow chat
// service never slows a request.
func (w *ErrorWatch) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if w == nil || !w.N.Enabled() || c.Writer.Status() < 500 {
			return
		}
		route := c.FullPath()
		if route == "" {
			route = "(no route)"
		}
		if msg := w.Record(c.Request.Method+" "+route, c.Writer.Header().Get("X-Request-Id")); msg != "" {
			send := w.sendFn
			if send == nil {
				send = w.N.Send
			}
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if err := send(ctx, Warning, "The API is returning errors", msg); err != nil {
					slog.Warn("alert not delivered", "error", err)
				}
			}()
		}
	}
}

// WatchDatabase pings the database every interval. After two failures in a row it sends one critical message;
// when the database answers again it sends one info message. It returns when ctx ends. With several API
// instances each one watches, so a real outage may be reported more than once.
func WatchDatabase(ctx context.Context, n *Notifier, interval time.Duration, ping func(context.Context) error) {
	if !n.Enabled() {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	fails, down := 0, false
	var since time.Time
	send := func(l Level, title, body string) {
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := n.Send(sctx, l, title, body); err != nil {
			slog.Warn("alert not delivered", "error", err)
		}
	}
	for {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := ping(pctx)
		cancel()
		switch {
		case err != nil && ctx.Err() == nil:
			fails++
			if fails == 2 && !down {
				down, since = true, time.Now()
				send(Critical, "The database is not answering", "The API cannot reach its database. The portal is not usable until it is back.")
			}
		case err == nil:
			if down {
				send(Info, "The database is back", fmt.Sprintf("It was unreachable for about %s.", time.Since(since).Round(time.Minute)))
			}
			fails, down = 0, false
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
