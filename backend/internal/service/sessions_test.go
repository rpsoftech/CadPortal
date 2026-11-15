package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
)

func sessionClaims(userID int64, sid string) auth.Claims {
	return auth.Claims{Role: auth.Designer, SessionID: sid, RegisteredClaims: jwt.RegisteredClaims{Subject: strconv.FormatInt(userID, 10)}}
}

// setSeen moves a session's last-seen time to `ago` before the database's now.
func setSeen(t *testing.T, s *Service, sid string, ago time.Duration) {
	t.Helper()
	if _, err := s.Pool.Exec(context.Background(),
		`UPDATE sessions SET last_seen_at = now() - ($2::float8 * interval '1 second') WHERE id = $1`, sid, ago.Seconds()); err != nil {
		t.Fatal(err)
	}
}

func seenAgo(t *testing.T, s *Service, sid string) time.Duration {
	t.Helper()
	var secs float64
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT extract(epoch FROM now() - last_seen_at)::float8 FROM sessions WHERE id = $1`, sid).Scan(&secs); err != nil {
		t.Fatal(err)
	}
	return time.Duration(secs * float64(time.Second))
}

func TestSessionIdleLimit(t *testing.T) {
	for _, tc := range []struct {
		minutes int
		want    time.Duration
	}{
		{30, 32 * time.Minute}, // the web timeout plus two minutes of grace
		{1, 3 * time.Minute},
		{0, 0},  // off
		{-5, 0}, // never negative
	} {
		if got := SessionIdleLimit(tc.minutes); got != tc.want {
			t.Errorf("SessionIdleLimit(%d) = %v, want %v", tc.minutes, got, tc.want)
		}
	}
}

func TestCheckSession(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	a := mkUser(t, s, "a@example.com", "designer")
	b := mkUser(t, s, "b@example.com", "designer")
	s.SessionIdle = 10 * time.Minute
	open := func(u int64) string {
		t.Helper()
		sid, err := s.CreateSession(ctx, u, time.Now().Add(8*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		return sid
	}

	tests := []struct {
		name  string
		claim func() auth.Claims
		want  auth.SessionState
	}{
		{"a new login is active", func() auth.Claims { return sessionClaims(a.ID, open(a.ID)) }, auth.SessionActive},
		{"a token with no session", func() auth.Claims { return sessionClaims(a.ID, "") }, auth.SessionEnded},
		{"an unknown session", func() auth.Claims { return sessionClaims(a.ID, "00000000000000000000000000000000") }, auth.SessionEnded},
		{"another user's session id", func() auth.Claims { return sessionClaims(b.ID, open(a.ID)) }, auth.SessionEnded},
		{"a subject that is not a user id", func() auth.Claims {
			c := sessionClaims(a.ID, open(a.ID))
			c.Subject = "abc"
			return c
		}, auth.SessionEnded},
		{"used 9 minutes ago is still active", func() auth.Claims {
			sid := open(a.ID)
			setSeen(t, s, sid, 9*time.Minute)
			return sessionClaims(a.ID, sid)
		}, auth.SessionActive},
		{"idle for 11 minutes", func() auth.Claims {
			sid := open(a.ID)
			setSeen(t, s, sid, 11*time.Minute)
			return sessionClaims(a.ID, sid)
		}, auth.SessionIdle},
		{"signed out", func() auth.Claims {
			sid := open(a.ID)
			if err := s.EndSession(ctx, a.ID, sid); err != nil {
				t.Fatal(err)
			}
			return sessionClaims(a.ID, sid)
		}, auth.SessionEnded},
		{"past its own expiry", func() auth.Claims {
			sid := open(a.ID)
			if _, err := s.Pool.Exec(ctx, `UPDATE sessions SET expires_at = now() - interval '1 second' WHERE id = $1`, sid); err != nil {
				t.Fatal(err)
			}
			return sessionClaims(a.ID, sid)
		}, auth.SessionEnded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.CheckSession(ctx, tc.claim())
			if err != nil || got != tc.want {
				t.Errorf("CheckSession = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestIdleLoginStaysIdle(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	u := mkUser(t, s, "a@example.com", "designer")
	s.SessionIdle = 10 * time.Minute
	sid, _ := s.CreateSession(ctx, u.ID, time.Now().Add(8*time.Hour))
	setSeen(t, s, sid, 11*time.Minute)

	for i := 0; i < 3; i++ { // asking again must not count as activity and bring it back
		if got, err := s.CheckSession(ctx, sessionClaims(u.ID, sid)); err != nil || got != auth.SessionIdle {
			t.Fatalf("check %d: %v, %v", i, got, err)
		}
	}
	if seenAgo(t, s, sid) < 11*time.Minute {
		t.Errorf("an idle login's last-seen time was refreshed")
	}
}

func TestActivityIsWrittenAtMostEveryThirtySeconds(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	u := mkUser(t, s, "a@example.com", "designer")
	s.SessionIdle = 10 * time.Minute
	sid, _ := s.CreateSession(ctx, u.ID, time.Now().Add(8*time.Hour))

	setSeen(t, s, sid, 10*time.Second) // recent: no write
	if got, _ := s.CheckSession(ctx, sessionClaims(u.ID, sid)); got != auth.SessionActive {
		t.Fatalf("got %v", got)
	}
	if ago := seenAgo(t, s, sid); ago < 9*time.Second {
		t.Errorf("a request 10 s after the last one rewrote the row (last seen %v ago)", ago)
	}

	setSeen(t, s, sid, 5*time.Minute) // older than 30 s: refreshed
	if got, _ := s.CheckSession(ctx, sessionClaims(u.ID, sid)); got != auth.SessionActive {
		t.Fatalf("got %v", got)
	}
	if ago := seenAgo(t, s, sid); ago > 5*time.Second {
		t.Errorf("a request after 5 minutes did not refresh the last-seen time (%v ago)", ago)
	}
}

func TestNoIdleLimitMeansNeverIdle(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	u := mkUser(t, s, "a@example.com", "designer")
	s.SessionIdle = 0
	sid, _ := s.CreateSession(ctx, u.ID, time.Now().Add(8*time.Hour))
	setSeen(t, s, sid, 10*24*time.Hour)
	if got, err := s.CheckSession(ctx, sessionClaims(u.ID, sid)); err != nil || got != auth.SessionActive {
		t.Errorf("with the idle limit off a quiet login is still active, got %v, %v", got, err)
	}
	// Signing out still works with the limit off.
	if err := s.EndSession(ctx, u.ID, sid); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.CheckSession(ctx, sessionClaims(u.ID, sid)); got != auth.SessionEnded {
		t.Errorf("signed out, got %v", got)
	}
}

func TestEndSession(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	a := mkUser(t, s, "a@example.com", "designer")
	b := mkUser(t, s, "b@example.com", "designer")
	s.SessionIdle = 10 * time.Minute
	sid, _ := s.CreateSession(ctx, a.ID, time.Now().Add(8*time.Hour))

	if err := s.EndSession(ctx, b.ID, sid); err != nil { // someone else's login: nothing happens
		t.Fatal(err)
	}
	if got, _ := s.CheckSession(ctx, sessionClaims(a.ID, sid)); got != auth.SessionActive {
		t.Errorf("another user ended this login, got %v", got)
	}
	for i := 0; i < 2; i++ { // twice is fine
		if err := s.EndSession(ctx, a.ID, sid); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.CheckSession(ctx, sessionClaims(a.ID, sid)); got != auth.SessionEnded {
		t.Errorf("got %v after sign-out", got)
	}
	if err := s.EndSession(ctx, a.ID, ""); err != nil {
		t.Errorf("a token with no session: %v", err)
	}
}

func TestCreateSessionDropsLongExpiredRows(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	u := mkUser(t, s, "a@example.com", "designer")
	old, _ := s.CreateSession(ctx, u.ID, time.Now().Add(8*time.Hour))
	recent, _ := s.CreateSession(ctx, u.ID, time.Now().Add(8*time.Hour))
	if _, err := s.Pool.Exec(ctx, `UPDATE sessions SET expires_at = now() - interval '2 days' WHERE id = $1`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE sessions SET expires_at = now() - interval '1 hour' WHERE id = $1`, recent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(ctx, u.ID, time.Now().Add(8*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id IN ($1, $2)`, old, recent).Scan(&n); err != nil || n != 1 {
		t.Errorf("want only the session that expired an hour ago to remain, %d remain (%v)", n, err)
	}
}
