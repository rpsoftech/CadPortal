package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
	"github.com/rpsoftech/cad-portal/backend/internal/mailer"
)

const (
	// ResetLinkTTL is how long a reset link works.
	ResetLinkTTL = time.Hour
	// maxEmailedResets caps self-service links per account per hour, so the form cannot be used to flood
	// someone's inbox.
	maxEmailedResets = 3
)

// ErrBadResetLink is the one answer for a link that is wrong, used, replaced or expired, so a response
// never says which.
var ErrBadResetLink = fmt.Errorf("%w: this reset link is not valid any more; ask for a new one", ErrInvalid)

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ResetEmailEnabled says whether people can ask for a reset link by email (a mail server is configured).
func (s *Service) ResetEmailEnabled() bool { return s.Mail != nil && s.AppBaseURL != "" }

// issueReset makes a link token for the user and replaces every older open one.
func (s *Service) issueReset(ctx context.Context, q *db.Queries, userID, createdBy int64, delivery string) (string, time.Time, error) {
	token, err := newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(ResetLinkTTL)
	if err := q.SupersedePasswordResets(ctx, userID); err != nil {
		return "", time.Time{}, err
	}
	if _, err := q.CreatePasswordReset(ctx, db.CreatePasswordResetParams{
		UserID: userID, TokenHash: hashToken(token), Delivery: delivery,
		CreatedBy: pgtype.Int8{Int64: createdBy, Valid: createdBy > 0}, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// AdminResetLink makes a one-hour reset link for a user. The admin hands it over; the user chooses their own
// password, so nobody but the user ever knows it. The token is returned once and cannot be read again.
func (s *Service) AdminResetLink(ctx context.Context, actorID, userID int64) (token string, expires time.Time, err error) {
	u, err := s.Q.GetUserByID(ctx, userID)
	if err != nil {
		return "", time.Time{}, mapErr(err)
	}
	if !u.Active {
		return "", time.Time{}, invalid("this account is disabled; enable it first")
	}
	err = s.inTx(ctx, func(q *db.Queries) error {
		token, expires, err = s.issueReset(ctx, q, u.ID, actorID, "admin")
		return err
	})
	if err == nil {
		_ = s.Q.DeleteOldPasswordResets(ctx)
	}
	return token, expires, err
}

// RequestPasswordReset is the "forgot password" form. It never says whether the address belongs to an
// account: an unknown, disabled or throttled address does nothing, a known one is mailed a link, and the
// caller shows the same message either way. The mail goes out in the background so the response time does
// not give the answer away.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) {
	if !s.ResetEmailEnabled() {
		return
	}
	email, err := normEmail(email)
	if err != nil {
		return
	}
	u, err := s.Q.GetUserByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("password reset lookup failed", "error", err)
		}
		return
	}
	if !u.Active {
		return
	}
	recent, err := s.Q.CountRecentPasswordResets(ctx, db.CountRecentPasswordResetsParams{
		UserID: u.ID, CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	if err != nil || recent >= maxEmailedResets {
		return
	}
	var token string
	if err := s.inTx(ctx, func(q *db.Queries) (e error) {
		token, _, e = s.issueReset(ctx, q, u.ID, 0, "email")
		return e
	}); err != nil {
		slog.Error("could not make a password reset link", "error", err)
		return
	}
	link := s.AppBaseURL + "/reset-password?token=" + token
	body := fmt.Sprintf("Hello %s,\n\nSomeone asked to reset the password of your CadPortal account. If it was you, open this link within an hour and choose a new password:\n\n%s\n\nThe link works once. If you did not ask for this, ignore this email: your password has not changed.\n",
		firstNonEmpty(u.Name, u.Email), link)
	s.mailWG.Add(1)
	go func() {
		defer s.mailWG.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.Mail.Send(sctx, u.Email, "Reset your CadPortal password", body); err != nil {
			slog.Error("could not send the password reset email", "error", err)
		}
	}()
}

// WaitMail waits for background emails to finish (shutdown, and tests).
func (s *Service) WaitMail() { s.mailWG.Wait() }

// ResetPassword sets a new password with a reset token. The token is used up only if the password is
// accepted, so a too-weak password can be corrected without asking for another link. Every older session
// stops working, as with any password change.
func (s *Service) ResetPassword(ctx context.Context, token, newPW string) (UserView, error) {
	if len(token) < 20 || len(token) > 100 {
		return UserView{}, ErrBadResetLink
	}
	var out UserView
	err := s.inTx(ctx, func(q *db.Queries) error {
		r, err := q.GetPasswordResetForUpdate(ctx, hashToken(token))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBadResetLink
		}
		if err != nil {
			return err
		}
		if r.UsedAt.Valid || !r.ExpiresAt.Time.After(time.Now()) {
			return ErrBadResetLink
		}
		u, err := q.GetUserByIDForUpdate(ctx, r.UserID)
		if err != nil {
			return err
		}
		if !u.Active {
			return ErrBadResetLink
		}
		if err := (&Service{Q: q}).storePassword(ctx, u, newPW); err != nil {
			return err
		}
		if err := q.UsePasswordReset(ctx, r.ID); err != nil {
			return err
		}
		out = viewOf(u)
		return nil
	})
	return out, err
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var _ mailer.Sender = (*mailer.SMTP)(nil)
