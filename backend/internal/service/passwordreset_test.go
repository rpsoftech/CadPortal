package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rpsoftech/cad-portal/backend/internal/mailer"
)

const newPW = "a brand new passphrase"

func tokenFromMail(t *testing.T, m mailer.Mail) string {
	t.Helper()
	const marker = "/reset-password?token="
	i := strings.Index(m.Body, marker)
	if i < 0 {
		t.Fatalf("no link in the email: %s", m.Body)
	}
	rest := m.Body[i+len(marker):]
	if j := strings.IndexAny(rest, " \n\r"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func TestAdminResetLinkSetsAPasswordOnceAndEndsOldSessions(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	admin := mkUser(t, s, "admin@example.com", "admin")
	u := mkUser(t, s, "asha@example.com", "designer")

	tok, exp, err := s.AdminResetLink(ctx, admin.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) < 40 || !exp.After(time.Now().Add(50*time.Minute)) || !exp.Before(time.Now().Add(61*time.Minute)) {
		t.Fatalf("token %q expires %v", tok, exp)
	}
	// Only a hash is stored.
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM password_resets WHERE token_hash = $1`, tok).Scan(&n); err != nil || n != 0 {
		t.Errorf("the token itself must not be stored: %d %v", n, err)
	}

	time.Sleep(1100 * time.Millisecond) // token issue time has one-second precision
	got, err := s.ResetPassword(ctx, tok, newPW)
	if err != nil || got.ID != u.ID {
		t.Fatalf("reset: %+v %v", got, err)
	}
	if _, err := s.Authenticate(ctx, "asha@example.com", newPW); err != nil {
		t.Errorf("the new password must work: %v", err)
	}
	if _, err := s.Authenticate(ctx, "asha@example.com", testPW); err == nil {
		t.Error("the old password must stop working")
	}
	// Single use.
	if _, err := s.ResetPassword(ctx, tok, "yet another passphrase"); !errors.Is(err, ErrBadResetLink) {
		t.Errorf("a used link: %v", err)
	}
}

func TestResetEndsExistingSessionsAndUnlocksTheAccount(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	u := mkUser(t, s, "asha@example.com", "designer")
	for i := 0; i < maxLoginAttempts; i++ {
		_, _ = s.Authenticate(ctx, "asha@example.com", "wrong password here")
	}
	if _, err := s.Authenticate(ctx, "asha@example.com", testPW); err == nil {
		t.Fatal("setup: the account should be locked")
	}
	tok, _, err := s.AdminResetLink(ctx, 0, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetPassword(ctx, tok, newPW); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "asha@example.com", newPW); err != nil {
		t.Errorf("a reset must unlock the account: %v", err)
	}
	var changed time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT pwd_changed_at FROM users WHERE id = $1`, u.ID).Scan(&changed); err != nil || time.Since(changed) > time.Minute {
		t.Errorf("pwd_changed_at must move so older tokens die: %v %v", changed, err)
	}
}

func TestAWeakPasswordDoesNotUseUpTheLink(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	u := mkUser(t, s, "asha@example.com", "designer")
	tok, _, _ := s.AdminResetLink(ctx, 0, u.ID)
	if _, err := s.ResetPassword(ctx, tok, "short"); !errors.Is(err, ErrInvalid) || errors.Is(err, ErrBadResetLink) {
		t.Fatalf("a short password is a normal validation error, got %v", err)
	}
	if _, err := s.ResetPassword(ctx, tok, "asha@example.com"); !errors.Is(err, ErrInvalid) {
		t.Errorf("the email as a password: %v", err)
	}
	if _, err := s.ResetPassword(ctx, tok, newPW); err != nil {
		t.Errorf("the same link must still work with a good password: %v", err)
	}
}

func TestOnlyTheNewestLinkWorksAndExpiredOnesDont(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	u := mkUser(t, s, "asha@example.com", "designer")
	first, _, _ := s.AdminResetLink(ctx, 0, u.ID)
	second, _, _ := s.AdminResetLink(ctx, 0, u.ID)
	if _, err := s.ResetPassword(ctx, first, newPW); !errors.Is(err, ErrBadResetLink) {
		t.Errorf("an older link must be replaced: %v", err)
	}
	// Expire the second one.
	if _, err := s.Pool.Exec(ctx, `UPDATE password_resets SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetPassword(ctx, second, newPW); !errors.Is(err, ErrBadResetLink) {
		t.Errorf("an expired link: %v", err)
	}
	for _, junk := range []string{"", "short", strings.Repeat("x", 200), strings.Repeat("A", 43)} {
		if _, err := s.ResetPassword(ctx, junk, newPW); !errors.Is(err, ErrBadResetLink) {
			t.Errorf("token %q: %v", junk, err)
		}
	}
}

func TestADisabledAccountGetsNoLinkAndAnOldLinkStopsWorking(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	admin := mkUser(t, s, "admin@example.com", "admin")
	u := mkUser(t, s, "asha@example.com", "designer")
	tok, _, _ := s.AdminResetLink(ctx, admin.ID, u.ID)
	if _, err := s.UpdateUser(ctx, admin.ID, u.ID, "Test", "designer", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetPassword(ctx, tok, newPW); !errors.Is(err, ErrBadResetLink) {
		t.Errorf("a link for a disabled account: %v", err)
	}
	if _, _, err := s.AdminResetLink(ctx, admin.ID, u.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("no link for a disabled account: %v", err)
	}
	if _, _, err := s.AdminResetLink(ctx, admin.ID, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
}

func emailService(t *testing.T) (*Service, *mailer.Memory) {
	t.Helper()
	s := newTestService(t)
	m := &mailer.Memory{}
	s.Mail, s.AppBaseURL = m, "https://cad.example.com"
	return s, m
}

func TestForgotPasswordMailsAWorkingLinkOnlyToARealAccount(t *testing.T) {
	s, m := emailService(t)
	ctx := context.Background()
	mkUser(t, s, "asha@example.com", "designer")

	s.RequestPasswordReset(ctx, "nobody@example.com")
	s.RequestPasswordReset(ctx, "not an email")
	s.RequestPasswordReset(ctx, "")
	s.WaitMail()
	if len(m.Mails()) != 0 {
		t.Fatalf("an unknown address must send nothing: %v", m.Mails())
	}

	s.RequestPasswordReset(ctx, "  Asha@Example.com ")
	s.WaitMail()
	mails := m.Mails()
	if len(mails) != 1 || mails[0].To != "asha@example.com" || !strings.HasPrefix(mails[0].Subject, "Reset your") {
		t.Fatalf("mails = %+v", mails)
	}
	if !strings.Contains(mails[0].Body, "https://cad.example.com/reset-password?token=") {
		t.Errorf("link: %s", mails[0].Body)
	}
	if _, err := s.ResetPassword(ctx, tokenFromMail(t, mails[0]), newPW); err != nil {
		t.Errorf("the emailed link must work: %v", err)
	}
}

func TestForgotPasswordIsThrottledAndIgnoresDisabledAccounts(t *testing.T) {
	s, m := emailService(t)
	ctx := context.Background()
	admin := mkUser(t, s, "admin@example.com", "admin")
	u := mkUser(t, s, "asha@example.com", "designer")
	for i := 0; i < 6; i++ {
		s.RequestPasswordReset(ctx, "asha@example.com")
	}
	s.WaitMail()
	if n := len(m.Mails()); n != maxEmailedResets {
		t.Errorf("sent %d emails, the cap is %d an hour", n, maxEmailedResets)
	}
	if _, err := s.UpdateUser(ctx, admin.ID, u.ID, "Test", "designer", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM password_resets`); err != nil {
		t.Fatal(err)
	}
	before := len(m.Mails())
	s.RequestPasswordReset(ctx, "asha@example.com")
	s.WaitMail()
	if len(m.Mails()) != before {
		t.Error("a disabled account must get no email")
	}
}

func TestForgotPasswordIsOffWithoutMail(t *testing.T) {
	s := newTestService(t)
	mkUser(t, s, "asha@example.com", "designer")
	if s.ResetEmailEnabled() {
		t.Error("no mail configured")
	}
	s.RequestPasswordReset(context.Background(), "asha@example.com")
	var n int
	_ = s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM password_resets`).Scan(&n)
	if n != 0 {
		t.Errorf("no link may be created when it cannot be mailed: %d", n)
	}
}

func TestAFailedSendDoesNotBreakTheRequest(t *testing.T) {
	s, m := emailService(t)
	m.Err = errors.New("smtp down")
	mkUser(t, s, "asha@example.com", "designer")
	s.RequestPasswordReset(context.Background(), "asha@example.com") // must not panic or return an error
	s.WaitMail()
}
