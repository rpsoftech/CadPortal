package service

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// Field limits keep one noisy or hostile value (a failed-login email, say) from bloating
// the table or the screen.
const (
	maxAuditAction = 64
	maxAuditEntity = 64
	maxAuditDetail = 200
	maxAuditEmail  = 254
)

// AuditEntry is one thing that happened. UserID 0 means no signed-in user (for example a
// failed login). Email is only needed when there is no UserID; otherwise it is looked up.
type AuditEntry struct {
	UserID     int64
	Email      string
	Action     string
	EntityType string
	EntityID   string
	Status     int
	Detail     string
}

// AuditRecord is the stored form, as returned to the admin screen.
type AuditRecord struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	UserID     *int64    `json:"user_id"`
	UserEmail  string    `json:"user_email"`
	Action     string    `json:"action"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	Status     int       `json:"status"`
	Detail     string    `json:"detail"`
}

// cleanAudit strips control characters (no log forging or terminal tricks), trims and
// caps the length in characters.
func cleanAudit(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

// RecordAudit appends an entry. Callers must treat a failure as non-fatal: losing an audit
// line must never turn a successful request into an error.
func (s *Service) RecordAudit(ctx context.Context, e AuditEntry) error {
	email := cleanAudit(e.Email, maxAuditEmail)
	var userID pgtype.Int8
	if e.UserID != 0 {
		userID = pgtype.Int8{Int64: e.UserID, Valid: true}
		if u, err := s.Q.GetUserByID(ctx, e.UserID); err == nil {
			email = u.Email
		}
	}
	return s.Q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{
		UserID:     userID,
		UserEmail:  email,
		Action:     cleanAudit(e.Action, maxAuditAction),
		EntityType: cleanAudit(e.EntityType, maxAuditEntity),
		EntityID:   cleanAudit(e.EntityID, maxAuditEntity),
		Status:     int32(e.Status),
		Detail:     cleanAudit(e.Detail, maxAuditDetail),
	})
}

type AuditFilter struct {
	UserID *int64
	// ActionPrefix matches the start of the action, e.g. "metal." or "auth.login".
	ActionPrefix string
	From, To     *time.Time
	Limit        int32
	Offset       int32
}

// ListAudit returns newest first, with the total match count before paging.
func (s *Service) ListAudit(ctx context.Context, f AuditFilter) ([]AuditRecord, int64, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	p := db.ListAuditParams{
		ActionPrefix: pgText(likeEscaper.Replace(strings.TrimSpace(f.ActionPrefix))),
		Lim:          f.Limit,
		Off:          f.Offset,
	}
	if f.UserID != nil {
		p.UserID = pgtype.Int8{Int64: *f.UserID, Valid: true}
	}
	if f.From != nil {
		p.FromAt = pgtype.Timestamptz{Time: *f.From, Valid: true}
	}
	if f.To != nil {
		p.ToAt = pgtype.Timestamptz{Time: *f.To, Valid: true}
	}
	rows, err := s.Q.ListAudit(ctx, p)
	if err != nil {
		return nil, 0, err
	}
	out := make([]AuditRecord, len(rows))
	var total int64
	for i, r := range rows {
		rec := AuditRecord{
			ID: r.ID, At: r.At.Time, UserEmail: r.UserEmail, Action: r.Action,
			EntityType: r.EntityType, EntityID: r.EntityID, Status: int(r.Status), Detail: r.Detail,
		}
		if r.UserID.Valid {
			id := r.UserID.Int64
			rec.UserID = &id
		}
		out[i] = rec
		total = r.TotalCount
	}
	return out, total, nil
}

// PruneAudit deletes entries older than the cutoff (the retention job).
func (s *Service) PruneAudit(ctx context.Context, olderThan time.Time) (int64, error) {
	return s.Q.PruneAudit(ctx, pgtype.Timestamptz{Time: olderThan, Valid: true})
}
