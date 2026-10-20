package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
)

const testPW = "correct horse battery"

func mkUser(t *testing.T, s *Service, email, role string) UserView {
	t.Helper()
	u, err := s.CreateUser(context.Background(), CreateUserInput{Email: email, Name: "Test", Password: testPW, Role: role})
	if err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	return u
}

func TestCreateUserValidation(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, CreateUserInput{Email: "  Alice@Example.COM ", Name: "  Alice  ", Password: testPW, Role: "designer"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "alice@example.com" || u.Name != "Alice" || !u.Active || u.Role != "designer" {
		t.Errorf("user = %+v", u)
	}
	b, _ := json.Marshal(u)
	if strings.Contains(strings.ToLower(string(b)), "password") || strings.Contains(string(b), "argon2") {
		t.Errorf("a view must never contain the hash: %s", b)
	}

	bad := map[string]CreateUserInput{
		"no email":       {Email: "", Password: testPW, Role: "admin"},
		"no at":          {Email: "alice.example.com", Password: testPW, Role: "admin"},
		"display name":   {Email: "Bob <bob@example.com>", Password: testPW, Role: "admin"},
		"bad role":       {Email: "c@example.com", Password: testPW, Role: "root"},
		"empty role":     {Email: "c@example.com", Password: testPW, Role: ""},
		"short password": {Email: "c@example.com", Password: "short", Role: "admin"},
		"password=email": {Email: "longemail@example.com", Password: "longemail@example.com", Role: "admin"},
		"name too long":  {Email: "c@example.com", Name: strings.Repeat("x", 101), Password: testPW, Role: "admin"},
	}
	for name, in := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := s.CreateUser(ctx, in); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
	// Email is unique regardless of case.
	if _, err := s.CreateUser(ctx, CreateUserInput{Email: "ALICE@example.com", Password: testPW, Role: "admin"}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate: %v", err)
	}
}

func TestAuthenticateAndLockout(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	u := mkUser(t, s, "dana@example.com", "designer")

	got, err := s.Authenticate(ctx, " DANA@example.com ", testPW)
	if err != nil || got.ID != u.ID {
		t.Fatalf("login: %v %+v", err, got)
	}
	if got, _ := s.GetUser(ctx, u.ID); got.LastLoginAt == nil {
		t.Error("last_login_at not recorded")
	}

	// Unknown email and wrong password are indistinguishable.
	_, e1 := s.Authenticate(ctx, "nobody@example.com", testPW)
	_, e2 := s.Authenticate(ctx, "dana@example.com", "wrong password!!")
	if !errors.Is(e1, ErrInvalidCredentials) || !errors.Is(e2, ErrInvalidCredentials) || e1.Error() != e2.Error() {
		t.Fatalf("errors differ: %v / %v", e1, e2)
	}

	// Four failures in total (one above, three here) do not lock; a success resets the counter.
	for i := 0; i < 3; i++ {
		s.Authenticate(ctx, "dana@example.com", "wrong password!!") //nolint:errcheck
	}
	if _, err := s.Authenticate(ctx, "dana@example.com", testPW); err != nil {
		t.Fatalf("4 failures must not lock: %v", err)
	}
	var attempts int
	s.Pool.QueryRow(ctx, "SELECT failed_attempts FROM users WHERE id=$1", u.ID).Scan(&attempts) //nolint:errcheck
	if attempts != 0 {
		t.Errorf("counter not reset: %d", attempts)
	}

	// Five in a row lock the account, even for the right password.
	for i := 0; i < 5; i++ {
		s.Authenticate(ctx, "dana@example.com", "wrong password!!") //nolint:errcheck
	}
	if _, err := s.Authenticate(ctx, "dana@example.com", testPW); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("locked account accepted the right password: %v", err)
	}
	// Expiry of the lock restores access.
	if _, err := s.Pool.Exec(ctx, "UPDATE users SET locked_until = now() - interval '1 second' WHERE id=$1", u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "dana@example.com", testPW); err != nil {
		t.Fatalf("after lock expiry: %v", err)
	}
}

func TestAuthenticateDisabledUser(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	admin := mkUser(t, s, "root@example.com", "admin")
	u := mkUser(t, s, "eve@example.com", "production")
	if _, err := s.UpdateUser(ctx, admin.ID, u.ID, "Eve", "production", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "eve@example.com", testPW); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled user logged in: %v", err)
	}
	// A disabled account does not accumulate failures.
	var attempts int
	s.Authenticate(ctx, "eve@example.com", "wrong password!!")                                  //nolint:errcheck
	s.Pool.QueryRow(ctx, "SELECT failed_attempts FROM users WHERE id=$1", u.ID).Scan(&attempts) //nolint:errcheck
	if attempts != 0 {
		t.Errorf("attempts = %d", attempts)
	}
}

func claimsFor(u UserView, role string, issuedAt time.Time) auth.Claims {
	return auth.Claims{Role: auth.Role(role), RegisteredClaims: jwt.RegisteredClaims{
		Subject: itoa(u.ID), IssuedAt: jwt.NewNumericDate(issuedAt),
	}}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func TestValidateToken(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	admin := mkUser(t, s, "root@example.com", "admin")
	u := mkUser(t, s, "fay@example.com", "designer")
	now := time.Now()

	if !s.ValidateToken(ctx, claimsFor(u, "designer", now)) {
		t.Fatal("fresh token rejected")
	}
	if s.ValidateToken(ctx, claimsFor(u, "admin", now)) {
		t.Error("token claiming a different role accepted")
	}
	if s.ValidateToken(ctx, auth.Claims{Role: "designer", RegisteredClaims: jwt.RegisteredClaims{Subject: itoa(u.ID)}}) {
		t.Error("token without issued-at accepted")
	}
	if s.ValidateToken(ctx, claimsFor(UserView{ID: 999999}, "designer", now)) {
		t.Error("unknown user accepted")
	}
	if s.ValidateToken(ctx, auth.Claims{Role: "designer", RegisteredClaims: jwt.RegisteredClaims{Subject: "abc", IssuedAt: jwt.NewNumericDate(now)}}) {
		t.Error("non-numeric subject accepted")
	}

	// A role change invalidates old tokens.
	if _, err := s.UpdateUser(ctx, admin.ID, u.ID, "Fay", "production", true); err != nil {
		t.Fatal(err)
	}
	if s.ValidateToken(ctx, claimsFor(u, "designer", now)) {
		t.Error("token for the old role accepted")
	}
	if !s.ValidateToken(ctx, claimsFor(u, "production", now)) {
		t.Error("token for the new role rejected")
	}

	// A password reset invalidates tokens issued before it, but not ones issued after.
	if err := s.SetPassword(ctx, u.ID, "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if s.ValidateToken(ctx, claimsFor(u, "production", now.Add(-10*time.Second))) {
		t.Error("token issued before the password change accepted")
	}
	if !s.ValidateToken(ctx, claimsFor(u, "production", time.Now())) {
		t.Error("token issued after the password change rejected")
	}

	// Disabling invalidates everything.
	if _, err := s.UpdateUser(ctx, admin.ID, u.ID, "Fay", "production", false); err != nil {
		t.Fatal(err)
	}
	if s.ValidateToken(ctx, claimsFor(u, "production", time.Now())) {
		t.Error("disabled user's token accepted")
	}
}

func TestUpdateUserGuards(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	a1 := mkUser(t, s, "a1@example.com", "admin")
	d := mkUser(t, s, "d@example.com", "designer")

	// The only active admin cannot be demoted or disabled.
	if _, err := s.UpdateUser(ctx, a1.ID, a1.ID, "A1", "designer", true); !errors.Is(err, ErrInvalid) {
		t.Errorf("demote last admin: %v", err)
	}
	if _, err := s.UpdateUser(ctx, d.ID, a1.ID, "A1", "admin", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("disable last admin: %v", err)
	}
	// With a second admin, demotion is fine, and an admin cannot disable themselves.
	a2 := mkUser(t, s, "a2@example.com", "admin")
	if _, err := s.UpdateUser(ctx, a2.ID, a2.ID, "A2", "admin", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("self-disable: %v", err)
	}
	if _, err := s.UpdateUser(ctx, a2.ID, a1.ID, "A1", "designer", true); err != nil {
		t.Errorf("demote with another admin present: %v", err)
	}
	// ...and now a2 is the last one again.
	if _, err := s.UpdateUser(ctx, d.ID, a2.ID, "A2", "designer", true); !errors.Is(err, ErrInvalid) {
		t.Errorf("demote the new last admin: %v", err)
	}
	if _, err := s.UpdateUser(ctx, a2.ID, d.ID, "D", "root", true); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad role: %v", err)
	}
	if _, err := s.UpdateUser(ctx, a2.ID, 424242, "X", "designer", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing user: %v", err)
	}
	users, err := s.ListUsers(ctx)
	if err != nil || len(users) != 3 || users[0].Email != "a1@example.com" {
		t.Errorf("list = %v %v", users, err)
	}
}

func TestChangePassword(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	u := mkUser(t, s, "gus@example.com", "designer")

	if err := s.ChangePassword(ctx, u.ID, "not my password!", "another long password"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong current: %v", err)
	}
	if err := s.ChangePassword(ctx, u.ID, testPW, "short"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("weak new password: %v", err)
	}
	if err := s.ChangePassword(ctx, u.ID, testPW, "another long password"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "gus@example.com", testPW); !errors.Is(err, ErrInvalidCredentials) {
		t.Error("old password still works")
	}
	if _, err := s.Authenticate(ctx, "gus@example.com", "another long password"); err != nil {
		t.Errorf("new password: %v", err)
	}
	// Guessing the current password through this endpoint also locks the account.
	for i := 0; i < 5; i++ {
		s.ChangePassword(ctx, u.ID, "guess guess guess", "yet another password") //nolint:errcheck
	}
	if _, err := s.Authenticate(ctx, "gus@example.com", "another long password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Error("account should be locked after repeated wrong current passwords")
	}
}
