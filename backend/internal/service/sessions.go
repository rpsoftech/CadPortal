package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// Server-side sessions. A login is a row in `sessions` and its token carries the row's id. The row lets the
// server end a login that went idle (the browser's own idle sign-out is only a courtesy, and a token that
// leaves the browser keeps working until it expires), and end one at once on sign-out.
//
// "Idle" means no authenticated request for SessionIdle. The web app sends a small request while the user is
// really active (typing in a long form makes no other request), so a working user is never idle here.

// SessionIdleGrace is added to the idle timeout shown to the user, so the server never ends a login the
// browser still counts as active: the browser's activity ping can lag the last keystroke by a few minutes.
const SessionIdleGrace = 2 * time.Minute

// noIdleLimit stands in for "never idle" in the query: longer than any login can last (SESSION_HOURS is at most 720).
const noIdleLimit = 24 * time.Hour * 365

// SessionIdleLimit is how long the server lets a login go without a request, for a web idle timeout of
// minutes. Zero minutes means no idle limit.
func SessionIdleLimit(minutes int) time.Duration {
	if minutes <= 0 {
		return 0
	}
	return time.Duration(minutes)*time.Minute + SessionIdleGrace
}

func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// CreateSession opens a login for userID that ends at expires (the token's expiry) and returns its id.
func (s *Service) CreateSession(ctx context.Context, userID int64, expires time.Time) (string, error) {
	id, err := newSessionID()
	if err != nil {
		return "", err
	}
	if err := s.Q.CreateSession(ctx, db.CreateSessionParams{
		ID: id, UserID: userID, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		return "", err
	}
	_ = s.Q.DeleteExpiredSessions(ctx) // housekeeping, like the password-reset table
	return id, nil
}

// CheckSession is the per-request session check behind auth.Authenticator. A login that is open and was used
// within SessionIdle is active, and the request counts as use (written at most every 30 seconds, see
// GetSessionState). An idle one stays idle: its last-seen time is
// not refreshed, so a later request cannot bring it back. A database failure is an error (503, not 401).
func (s *Service) CheckSession(ctx context.Context, c auth.Claims) (auth.SessionState, error) {
	uid, err := strconv.ParseInt(c.Subject, 10, 64)
	if err != nil || c.SessionID == "" {
		return auth.SessionEnded, nil
	}
	idle := s.SessionIdle
	window := idle
	if window <= 0 {
		window = noIdleLimit // any last-seen time is recent enough
	}
	st, err := s.Q.GetSessionState(ctx, db.GetSessionStateParams{
		IdleSecs: window.Seconds(), ID: c.SessionID, UserID: uid,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.SessionEnded, nil
	}
	if err != nil {
		return auth.SessionEnded, err
	}
	if !st.Open.Valid || !st.Open.Bool {
		return auth.SessionEnded, nil
	}
	if idle > 0 && !st.Fresh {
		return auth.SessionIdle, nil
	}
	if st.Stale {
		// Best effort: a failed refresh must not sign anyone out, the next request tries again.
		_ = s.Q.TouchSession(ctx, c.SessionID)
	}
	return auth.SessionActive, nil
}

// EndSession signs a login out. Ending one that is already over, or that is not the caller's, does nothing.
func (s *Service) EndSession(ctx context.Context, userID int64, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return s.Q.RevokeSession(ctx, db.RevokeSessionParams{ID: sessionID, UserID: userID})
}
