package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/mailer"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func TestResetLinkFromAnAdminLetsTheUserChooseAPassword(t *testing.T) {
	r, svc, _ := newHTTPEnv(t)
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, service.CreateUserInput{Email: "asha@example.com", Name: "Asha", Password: "correct horse battery", Role: "designer"})
	if err != nil {
		t.Fatal(err)
	}
	url := "/api/v1/users/" + idStr(u.ID) + "/reset-link"

	// Admin only.
	if w := do(r, "POST", url, "", "", nil); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
	for _, role := range []auth.Role{auth.Designer, auth.Production} {
		if w := do(r, "POST", url, token(t, role), "", nil); w.Code != 403 {
			t.Errorf("%s: %d", role, w.Code)
		}
	}
	w := do(r, "POST", url, token(t, auth.Admin), "", nil)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("admin: %d %s", w.Code, w.Header())
	}
	var link struct {
		Path      string `json:"path"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &link); err != nil || !strings.HasPrefix(link.Path, "/reset-password?token=") || link.ExpiresAt == "" {
		t.Fatalf("link: %s", w.Body)
	}
	tok := strings.TrimPrefix(link.Path, "/reset-password?token=")

	// The public reset endpoint: no sign-in needed, a weak password is a 400 that keeps the link alive.
	reset := func(token, pw string) int {
		return do(r, "POST", "/api/v1/auth/reset-password", "", "application/json",
			jsonBody(map[string]string{"token": token, "password": pw})).Code
	}
	if c := reset(tok, "short"); c != 400 {
		t.Errorf("weak password: %d", c)
	}
	if c := reset("not-a-real-token-at-all-0000", "a brand new passphrase"); c != 400 {
		t.Errorf("bad token: %d", c)
	}
	if c := reset(tok, "a brand new passphrase"); c != 204 {
		t.Fatalf("reset: %d", c)
	}
	if c := reset(tok, "another new passphrase!"); c != 400 {
		t.Errorf("the link is single use: %d", c)
	}
	login := do(r, "POST", "/api/v1/auth/login", "", "application/json",
		jsonBody(map[string]string{"email": "asha@example.com", "password": "a brand new passphrase"}))
	if login.Code != 200 {
		t.Errorf("sign in with the new password: %d %s", login.Code, login.Body)
	}

	// Recorded in the audit log, without the token.
	rows, _ := fetchAudit(t, r, token(t, auth.Admin), "")
	if findAction(rows, "user.reset_link") == nil || findAction(rows, "auth.password_reset") == nil {
		t.Errorf("reset actions missing from the audit log: %+v", rows)
	}
	for _, row := range rows {
		if strings.Contains(row.Detail, tok) {
			t.Errorf("the token must never be in the audit log: %+v", row)
		}
	}
}

func TestForgotPasswordAnswersTheSameWhoeverAsks(t *testing.T) {
	r, svc, _ := newHTTPEnv(t)
	ctx := context.Background()
	if _, err := svc.CreateUser(ctx, service.CreateUserInput{Email: "asha@example.com", Name: "Asha", Password: "correct horse battery", Role: "designer"}); err != nil {
		t.Fatal(err)
	}
	post := func(email string) (int, string) {
		w := do(r, "POST", "/api/v1/auth/forgot-password", "", "application/json", jsonBody(map[string]string{"email": email}))
		return w.Code, w.Body.String()
	}

	// Without a mail server: still the same neutral answer, and /auth/config says why nothing arrives.
	cfg := do(r, "GET", "/api/v1/auth/config", "", "", nil)
	if cfg.Code != 200 || !strings.Contains(cfg.Body.String(), `"password_reset_email":false`) {
		t.Errorf("config without mail: %d %s", cfg.Code, cfg.Body)
	}
	codeA, bodyA := post("asha@example.com")

	mem := &mailer.Memory{}
	svc.Mail, svc.AppBaseURL = mem, "https://cad.example.com"
	if cfg := do(r, "GET", "/api/v1/auth/config", "", "", nil); !strings.Contains(cfg.Body.String(), `"password_reset_email":true`) {
		t.Errorf("config with mail: %s", cfg.Body)
	}
	codeB, bodyB := post("asha@example.com")
	codeC, bodyC := post("nobody@example.com")
	codeD, bodyD := post("this is not an email")
	svc.WaitMail()
	for _, c := range []struct {
		code int
		body string
	}{{codeA, bodyA}, {codeB, bodyB}, {codeC, bodyC}, {codeD, bodyD}} {
		if c.code != 202 || c.body != bodyB {
			t.Errorf("every answer must be identical: %d %s vs %s", c.code, c.body, bodyB)
		}
	}
	if mails := mem.Mails(); len(mails) != 1 || mails[0].To != "asha@example.com" {
		t.Errorf("only the real account gets mail: %+v", mails)
	}
	if w := do(r, "POST", "/api/v1/auth/forgot-password", "", "application/json", []byte("{nope")); w.Code != 400 {
		t.Errorf("malformed body: %d", w.Code)
	}
	rows, _ := fetchAudit(t, r, token(t, auth.Admin), "?action=auth.password_reset_request")
	if len(rows) == 0 {
		t.Error("requests are audited")
	}
}
