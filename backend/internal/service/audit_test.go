package service

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRecordAuditSnapshotsEmailAndSanitizes(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	u := mkUser(t, s, "ann@example.com", "designer")

	// A known user: the email is looked up and stored as a snapshot.
	if err := s.RecordAudit(ctx, AuditEntry{UserID: u.ID, Action: "metal.update", EntityType: "metal", EntityID: "3", Status: 200, Detail: "18KR sg=15.25"}); err != nil {
		t.Fatal(err)
	}
	// Anonymous: the supplied email is kept, cleaned of control characters and capped.
	hostile := "evil@example.com\n[FAKE] admin logged in\x1b[31m" + strings.Repeat("x", 500)
	if err := s.RecordAudit(ctx, AuditEntry{Email: hostile, Action: "auth.login_failed", Status: 401, Detail: strings.Repeat("d", 1000)}); err != nil {
		t.Fatal(err)
	}

	rows, total, err := s.ListAudit(ctx, AuditFilter{})
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("rows=%d total=%d err=%v", len(rows), total, err)
	}
	anon, known := rows[0], rows[1] // newest first
	if known.UserEmail != "ann@example.com" || known.UserID == nil || *known.UserID != u.ID || known.Action != "metal.update" || known.Status != 200 {
		t.Errorf("known user entry = %+v", known)
	}
	if anon.UserID != nil {
		t.Errorf("anonymous entry has a user id: %+v", anon)
	}
	if strings.ContainsAny(anon.UserEmail, "\n\x1b") || len([]rune(anon.UserEmail)) > 254 || !strings.HasPrefix(anon.UserEmail, "evil@example.com") {
		t.Errorf("email not sanitised: %q", anon.UserEmail)
	}
	if len([]rune(anon.Detail)) != 200 {
		t.Errorf("detail length = %d, want capped at 200", len([]rune(anon.Detail)))
	}
	// The snapshot survives a later rename of the account.
	if _, err := s.Pool.Exec(ctx, "UPDATE users SET email='renamed@example.com' WHERE id=$1", u.ID); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = s.ListAudit(ctx, AuditFilter{})
	if rows[1].UserEmail != "ann@example.com" {
		t.Errorf("snapshot changed: %q", rows[1].UserEmail)
	}
}

func TestAuditIsAppendOnly(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.RecordAudit(ctx, AuditEntry{Action: "design.create", Status: 201}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, "UPDATE audit_log SET action='tampered'"); err == nil || !strings.Contains(err.Error(), "cannot be modified") {
		t.Errorf("UPDATE must be rejected by the trigger, got %v", err)
	}
	rows, _, _ := s.ListAudit(ctx, AuditFilter{})
	if len(rows) != 1 || rows[0].Action != "design.create" {
		t.Errorf("row changed: %+v", rows)
	}
	if err := s.RecordAudit(ctx, AuditEntry{Action: "", Status: 200}); err == nil {
		t.Error("an entry without an action must be rejected")
	}
}

func TestListAuditFiltersAndPaging(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	a := mkUser(t, s, "a@example.com", "admin")
	b := mkUser(t, s, "b@example.com", "designer")
	for _, e := range []AuditEntry{
		{UserID: a.ID, Action: "metal.update", Status: 200},
		{UserID: a.ID, Action: "metal.create", Status: 201},
		{UserID: b.ID, Action: "design.create", Status: 201},
		{UserID: b.ID, Action: "design.update", Status: 200},
		{UserID: b.ID, Action: "stone_matrix.update", Status: 200}, // underscore must match literally
		{UserID: b.ID, Action: "stoneXmatrix.update", Status: 200},
		{Email: "x@example.com", Action: "auth.login_failed", Status: 401},
	} {
		if err := s.RecordAudit(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	list := func(f AuditFilter) (string, int64) {
		rows, total, err := s.ListAudit(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		acts := make([]string, len(rows))
		for i, r := range rows {
			acts[i] = r.Action
		}
		return strings.Join(acts, ","), total
	}

	if got, total := list(AuditFilter{}); total != 7 || !strings.HasPrefix(got, "auth.login_failed,") {
		t.Errorf("all (newest first): %s total=%d", got, total)
	}
	if got, total := list(AuditFilter{UserID: &a.ID}); got != "metal.create,metal.update" || total != 2 {
		t.Errorf("by user: %s total=%d", got, total)
	}
	if got, total := list(AuditFilter{ActionPrefix: "design."}); got != "design.update,design.create" || total != 2 {
		t.Errorf("by prefix: %s total=%d", got, total)
	}
	if got, _ := list(AuditFilter{ActionPrefix: "stone_matrix"}); got != "stone_matrix.update" {
		t.Errorf("underscore must be literal: %s", got)
	}
	if got, total := list(AuditFilter{ActionPrefix: "metal.", UserID: &b.ID}); got != "" || total != 0 {
		t.Errorf("combined filters: %s total=%d", got, total)
	}
	if got, total := list(AuditFilter{Limit: 3, Offset: 2}); len(strings.Split(got, ",")) != 3 || total != 7 {
		t.Errorf("paging: %s total=%d", got, total)
	}
	// Date bounds.
	future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)
	if _, total := list(AuditFilter{From: &future}); total != 0 {
		t.Errorf("from in the future: %d", total)
	}
	if _, total := list(AuditFilter{To: &past}); total != 0 {
		t.Errorf("to in the past: %d", total)
	}
	if _, total := list(AuditFilter{From: &past, To: &future}); total != 7 {
		t.Errorf("window around now: %d", total)
	}
}

func TestPruneAudit(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := s.RecordAudit(ctx, AuditEntry{Action: "design.create", Status: 201}); err != nil {
			t.Fatal(err)
		}
	}
	// Age two of them.
	if _, err := s.Pool.Exec(ctx, "UPDATE audit_log SET at = now() - interval '100 days' WHERE id IN (SELECT id FROM audit_log ORDER BY id LIMIT 2)"); err == nil {
		t.Fatal("expected the append-only trigger to block aging rows by UPDATE")
	}
	// Insert old rows directly instead (INSERT is allowed).
	for i := 0; i < 2; i++ {
		if _, err := s.Pool.Exec(ctx, "INSERT INTO audit_log (at, action, status) VALUES (now() - interval '100 days', 'old.entry', 200)"); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.PruneAudit(ctx, time.Now().Add(-30*24*time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("pruned %d rows, err=%v", n, err)
	}
	if _, total, _ := s.ListAudit(ctx, AuditFilter{}); total != 3 {
		t.Errorf("recent entries left = %d, want 3", total)
	}
}
