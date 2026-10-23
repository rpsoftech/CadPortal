// Package heartbeat pings a URL on a schedule so an outside service (Healthchecks.io, Uptime Kuma,
// Better Stack and the like) can tell when the portal or its backups have gone quiet. The server only
// makes outgoing requests; nothing needs to be reachable from outside.
package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Validate refuses a URL that cannot be a heartbeat target, so a typo fails at start-up, not silently.
func Validate(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("must be a full http(s) URL")
	}
	return nil
}

// Host is the only part of the URL that is safe to log: the path usually holds the secret check id.
func Host(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return "?"
}

var client = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

// Ping sends one GET to raw. Any 2xx counts as delivered.
func Ping(ctx context.Context, raw string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "cadportal-heartbeat")
	resp, err := client.Do(req)
	if err != nil {
		// The error text of net/http holds the URL, which holds the secret: report the host only.
		return fmt.Errorf("heartbeat to %s failed", Host(raw))
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("heartbeat to %s answered %d", Host(raw), resp.StatusCode)
	}
	return nil
}

// Run pings raw every interval for as long as check says the service is healthy; while it is not, it
// sends nothing, so the monitor raises the alarm by itself. The first ping is sent at once. logf gets
// every state change (first failure, recovery), not every ping.
func Run(ctx context.Context, raw string, interval time.Duration, check func(context.Context) error, logf func(format string, a ...any)) {
	RunGated(ctx, raw, interval, check, nil, logf)
}

// RunGated is Run for several instances that share one monitor. Each tick it first asks gate whether this
// instance is the one that pings now (false: another instance has it, so nothing is sent and the state
// does not change; an error counts as unhealthy, like a failed check). A nil gate always pings.
func RunGated(ctx context.Context, raw string, interval time.Duration, check func(context.Context) error,
	gate func(context.Context) (bool, error), logf func(format string, a ...any)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	healthy := true
	for {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := check(cctx)
		mine := true
		if err == nil && gate != nil {
			mine, err = gate(cctx)
		}
		if err == nil && mine {
			err = Ping(cctx, raw)
		}
		cancel()
		switch {
		case err == nil && !mine:
			// another instance pings this round
		case err != nil && healthy:
			healthy = false
			logf("heartbeat stopped: %v", err)
		case err == nil && !healthy:
			healthy = true
			logf("heartbeat resumed")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
