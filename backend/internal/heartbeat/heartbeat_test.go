package heartbeat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	for _, ok := range []string{"https://hc-ping.com/abc-123", "http://uptime.local:3001/api/push/xyz"} {
		if err := Validate(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "hc-ping.com/abc", "ftp://x/y", "https://", "not a url"} {
		if Validate(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPingSucceedsOn2xxAndNeverLeaksTheSecretPath(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.Contains(r.URL.Path, "down") {
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	if err := Ping(context.Background(), srv.URL+"/secret-check-id"); err != nil || hits.Load() != 1 {
		t.Fatalf("ping: %v hits=%d", err, hits.Load())
	}
	err := Ping(context.Background(), srv.URL+"/secret-down-id")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("a 500 is a failure and must not echo the path: %v", err)
	}
	srv.Close()
	err = Ping(context.Background(), srv.URL+"/secret-check-id")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("an unreachable host is a failure and must not echo the path: %v", err)
	}
}

func TestRunPingsWhileHealthyAndGoesQuietWhenNot(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()

	var unhealthy atomic.Bool
	var logs atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, srv.URL+"/x", 20*time.Millisecond, func(context.Context) error {
			if unhealthy.Load() {
				return errors.New("database unreachable")
			}
			return nil
		}, func(string, ...any) { logs.Add(1) })
	}()

	wait := func(cond func() bool) {
		t.Helper()
		for i := 0; i < 200 && !cond(); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		if !cond() {
			t.Fatal("timed out")
		}
	}
	wait(func() bool { return hits.Load() >= 3 }) // the first ping is immediate, then one per interval

	unhealthy.Store(true)
	time.Sleep(60 * time.Millisecond) // let an in-flight round finish
	frozen := hits.Load()
	time.Sleep(150 * time.Millisecond)
	if hits.Load() != frozen {
		t.Errorf("pings must stop while unhealthy: %d -> %d", frozen, hits.Load())
	}
	if logs.Load() != 1 {
		t.Errorf("one log line for the state change, got %d", logs.Load())
	}

	unhealthy.Store(false)
	wait(func() bool { return hits.Load() > frozen })
	cancel()
	<-done
	if logs.Load() != 2 {
		t.Errorf("one more line for the recovery, got %d", logs.Load())
	}
}
