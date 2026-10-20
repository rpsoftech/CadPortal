package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/db"
	"github.com/rpsoftech/cad-portal/backend/internal/password"
)

// ErrInvalidCredentials is returned for every failed login (unknown email, wrong
// password, disabled or locked account) so a response never reveals which.
var ErrInvalidCredentials = errors.New("invalid email or password, or the account is temporarily locked")

const (
	maxLoginAttempts = 5
	lockSeconds      = 15 * 60
	maxNameLen       = 100
)

// UserView is the only shape of a user that leaves the service: it has no password hash.
type UserView struct {
	ID          int64      `json:"id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Role        string     `json:"role"`
	Active      bool       `json:"active"`
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

func viewOf(u db.User) UserView {
	v := UserView{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role, Active: u.Active, CreatedAt: u.CreatedAt.Time}
	if u.LastLoginAt.Valid {
		t := u.LastLoginAt.Time
		v.LastLoginAt = &t
	}
	return v
}

// normEmail lower-cases and validates a bare address ("Name <a@b>" is rejected).
func normEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || len(s) > 254 {
		return "", invalid("a valid email address is required")
	}
	return s, nil
}

func validRole(r string) error {
	if !auth.Role(r).Valid() {
		return invalid("role must be admin, designer or production")
	}
	return nil
}

func cleanName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if len([]rune(n)) > maxNameLen {
		return "", invalid("name must be at most %d characters", maxNameLen)
	}
	return n, nil
}

type CreateUserInput struct {
	Email    string
	Name     string
	Password string
	Role     string
}

func (s *Service) CreateUser(ctx context.Context, in CreateUserInput) (UserView, error) {
	email, err := normEmail(in.Email)
	if err != nil {
		return UserView{}, err
	}
	if err := validRole(in.Role); err != nil {
		return UserView{}, err
	}
	name, err := cleanName(in.Name)
	if err != nil {
		return UserView{}, err
	}
	if err := password.Check(in.Password, email); err != nil {
		return UserView{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	hash, err := password.Hash(in.Password)
	if err != nil {
		return UserView{}, err
	}
	u, err := s.Q.CreateUser(ctx, db.CreateUserParams{Email: email, Name: name, PasswordHash: hash, Role: in.Role})
	if err != nil {
		if e := mapErr(err); errors.Is(e, ErrConflict) {
			return UserView{}, fmt.Errorf("%w: that email is already registered", ErrConflict)
		}
		return UserView{}, err
	}
	return viewOf(u), nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// burn spends the same time as a real verification so a login for an unknown or
// unusable account cannot be told apart by response time.
func burn(pw string) {
	dummyOnce.Do(func() { dummyHash, _ = password.Hash("timing-equaliser-not-a-real-password") })
	password.Verify(pw, dummyHash)
}

// Authenticate checks a login. Failures count toward a 15-minute lock after 5 in
// a row; a locked or disabled account fails even with the right password.
func (s *Service) Authenticate(ctx context.Context, email, pw string) (UserView, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(pw) > password.MaxLength*4 { // bytes; refuse absurd input before hashing it
		burn(pw[:password.MaxLength])
		return UserView{}, ErrInvalidCredentials
	}
	u, err := s.Q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		burn(pw)
		return UserView{}, ErrInvalidCredentials
	}
	if err != nil {
		return UserView{}, err
	}
	locked := u.LockedUntil.Valid && u.LockedUntil.Time.After(time.Now())
	if locked || !u.Active {
		burn(pw)
		return UserView{}, ErrInvalidCredentials
	}
	if !password.Verify(pw, u.PasswordHash) {
		if _, err := s.Q.RecordLoginFailure(ctx, db.RecordLoginFailureParams{
			ID: u.ID, MaxAttempts: maxLoginAttempts, LockSeconds: lockSeconds,
		}); err != nil {
			return UserView{}, err
		}
		return UserView{}, ErrInvalidCredentials
	}
	if err := s.Q.RecordLoginSuccess(ctx, u.ID); err != nil {
		return UserView{}, err
	}
	return viewOf(u), nil
}

// ValidateToken is the per-request check behind auth.Authenticator: the user must
// still exist, be active, hold the role in the token, and not have changed their
// password since the token was issued (compared at one-second precision).
//
// A missing user is an invalid token (false, nil). A database failure is neither valid
// nor invalid, so it is returned as an error and the caller answers 503, not 401.
func (s *Service) ValidateToken(ctx context.Context, c auth.Claims) (bool, error) {
	id, err := strconv.ParseInt(c.Subject, 10, 64)
	if err != nil || c.IssuedAt == nil {
		return false, nil
	}
	u, err := s.Q.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !u.Active || u.Role != string(c.Role) {
		return false, nil
	}
	return !c.IssuedAt.Time.Before(u.PwdChangedAt.Time.Truncate(time.Second)), nil
}

func (s *Service) GetUser(ctx context.Context, id int64) (UserView, error) {
	u, err := s.Q.GetUserByID(ctx, id)
	return viewOf(u), mapErr(err)
}

func (s *Service) ListUsers(ctx context.Context) ([]UserView, error) {
	us, err := s.Q.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserView, len(us))
	for i, u := range us {
		out[i] = viewOf(u)
	}
	return out, nil
}

// UpdateUser changes name, role and active state. It refuses to leave the system
// without an active admin, and refuses to let an admin disable their own account.
func (s *Service) UpdateUser(ctx context.Context, actorID, id int64, name, role string, active bool) (UserView, error) {
	if err := validRole(role); err != nil {
		return UserView{}, err
	}
	name, err := cleanName(name)
	if err != nil {
		return UserView{}, err
	}
	if actorID == id && !active {
		return UserView{}, invalid("you cannot disable your own account")
	}
	var out UserView
	err = s.inTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetUserByIDForUpdate(ctx, id)
		if err != nil {
			return err
		}
		losesAdmin := cur.Role == string(auth.Admin) && cur.Active && (role != string(auth.Admin) || !active)
		if losesAdmin {
			n, err := q.CountActiveAdmins(ctx)
			if err != nil {
				return err
			}
			if n <= 1 {
				return invalid("at least one active admin is required")
			}
		}
		u, err := q.UpdateUser(ctx, db.UpdateUserParams{ID: id, Name: name, Role: role, Active: active})
		out = viewOf(u)
		return err
	})
	return out, err
}

// SetPassword resets a user's password (admin action). Existing tokens stop working.
func (s *Service) SetPassword(ctx context.Context, id int64, newPW string) error {
	u, err := s.Q.GetUserByID(ctx, id)
	if err != nil {
		return mapErr(err)
	}
	return s.storePassword(ctx, u, newPW)
}

// ChangePassword lets a user change their own password. The current password is
// checked with the same lockout accounting as login.
func (s *Service) ChangePassword(ctx context.Context, id int64, current, newPW string) error {
	u, err := s.Q.GetUserByID(ctx, id)
	if err != nil {
		return mapErr(err)
	}
	locked := u.LockedUntil.Valid && u.LockedUntil.Time.After(time.Now())
	if locked || !u.Active || !password.Verify(current, u.PasswordHash) {
		if !locked && u.Active {
			if _, err := s.Q.RecordLoginFailure(ctx, db.RecordLoginFailureParams{
				ID: u.ID, MaxAttempts: maxLoginAttempts, LockSeconds: lockSeconds,
			}); err != nil {
				return err
			}
		}
		// A signed-in user mistyping their current password is bad input (400), not an
		// expired session (401), so clients do not sign them out.
		return fmt.Errorf("%w: current password is incorrect, or the account is temporarily locked", ErrInvalid)
	}
	return s.storePassword(ctx, u, newPW)
}

func (s *Service) storePassword(ctx context.Context, u db.User, newPW string) error {
	if err := password.Check(newPW, u.Email); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	hash, err := password.Hash(newPW)
	if err != nil {
		return err
	}
	return s.Q.SetUserPassword(ctx, db.SetUserPasswordParams{ID: u.ID, PasswordHash: hash})
}
