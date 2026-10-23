package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func TestStorageMode(t *testing.T) {
	tests := []struct {
		explicit, bucket, want string
		wantErr                bool
	}{
		{"", "", "", false},
		{"", "cad-vault", "r2", false},
		{"local", "", "local", false},
		{"local", "cad-vault", "local", false}, // explicit wins over a configured bucket
		{"r2", "", "r2", false},
		{"s3", "", "", true},
		{"LOCAL", "", "", true}, // case matters: reject rather than guess
	}
	for _, tc := range tests {
		got, err := storageMode(tc.explicit, tc.bucket)
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("storageMode(%q,%q) = %q, %v; want %q, err=%v", tc.explicit, tc.bucket, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestListenAddr(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	if got := listenAddr(); got != ":8080" {
		t.Errorf("default = %q", got)
	}
	t.Setenv("HTTP_ADDR", "0.0.0.0:9000")
	if got := listenAddr(); got != "0.0.0.0:9000" {
		t.Errorf("configured = %q", got)
	}
}

func TestHealthcheckProbe(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("probed %s, want /healthz", r.URL.Path)
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	addr := ":" + u.Port() // the probe only uses the port, like the container's HTTP_ADDR

	if err := healthcheck(addr); err != nil {
		t.Errorf("healthy server: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := healthcheck(addr); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("unhealthy server: %v", err)
	}
	srv.Close()
	if err := healthcheck(addr); err == nil {
		t.Error("a stopped server must be unhealthy")
	}
	if err := healthcheck("not-an-address"); err == nil {
		t.Error("a malformed address must be an error")
	}
}

func TestAuditRetentionDays(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"", 0, false}, {"0", 0, false}, {"90", 90, false}, {"36500", 36500, false},
		{"-1", 0, true}, {"abc", 0, true}, {"1.5", 0, true}, {"36501", 0, true}, {" 30", 0, true},
	}
	for _, tc := range tests {
		got, err := auditRetentionDays(tc.in)
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("auditRetentionDays(%q) = %d, %v; want %d, err=%v", tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestCheckR2EnvNamesWhatIsWrong(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	full := map[string]string{
		"S3_ENDPOINT": "https://abc123.r2.cloudflarestorage.com", "S3_BUCKET": "cad-vault",
		"S3_ACCESS_KEY_ID": "id", "S3_SECRET_ACCESS_KEY": "secret",
	}
	if err := checkR2Env(env(full)); err != nil {
		t.Errorf("a complete setup: %v", err)
	}
	// What a copied .env.example looks like: a bucket and a placeholder endpoint, no keys.
	copied := map[string]string{"S3_ENDPOINT": "https://<account_id>.r2.cloudflarestorage.com", "S3_BUCKET": "cad-vault"}
	err := checkR2Env(env(copied))
	if err == nil || !strings.Contains(err.Error(), "S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY are empty") ||
		!strings.Contains(err.Error(), "STORAGE=local") {
		t.Errorf("missing keys: %v", err)
	}
	one := map[string]string{"S3_ENDPOINT": "https://x", "S3_BUCKET": "b", "S3_ACCESS_KEY_ID": "i"}
	if err := checkR2Env(env(one)); err == nil || !strings.Contains(err.Error(), "S3_SECRET_ACCESS_KEY is empty") {
		t.Errorf("one missing: %v", err)
	}
	placeholder := map[string]string{
		"S3_ENDPOINT": "https://<account_id>.r2.cloudflarestorage.com", "S3_BUCKET": "b",
		"S3_ACCESS_KEY_ID": "i", "S3_SECRET_ACCESS_KEY": "s",
	}
	if err := checkR2Env(env(placeholder)); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Errorf("placeholder endpoint: %v", err)
	}
	if err := checkR2Env(env(map[string]string{"S3_ENDPOINT": "  "})); err == nil {
		t.Error("whitespace counts as empty")
	}
}

func TestFileRetention(t *testing.T) {
	for in, want := range map[string]time.Duration{"": service.DefaultRetention, "0": 0, "7": 7 * 24 * time.Hour, "3650": 3650 * 24 * time.Hour} {
		if got, err := fileRetention(in); err != nil || got != want {
			t.Errorf("fileRetention(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"-1", "abc", "1.5", "3651", " "} {
		if _, err := fileRetention(in); err == nil {
			t.Errorf("fileRetention(%q) accepted", in)
		}
	}
}

func TestBackupMaxAge(t *testing.T) {
	cases := []struct {
		v, storage, bucket string
		want               time.Duration
	}{
		{"", "", "cad-vault", 36 * time.Hour}, // R2 is configured: a real installation
		{"", "r2", "", 36 * time.Hour},
		{"", "local", "", 0}, // development has no backup service
		{"", "", "", 0},
		{"48", "", "cad-vault", 48 * time.Hour},
		{"0", "", "cad-vault", 0}, // switched off
		{"12", "local", "", 12 * time.Hour},
	}
	for _, c := range cases {
		if got, err := backupMaxAge(c.v, c.storage, c.bucket); err != nil || got != c.want {
			t.Errorf("backupMaxAge(%q,%q,%q) = %v, %v; want %v", c.v, c.storage, c.bucket, got, err, c.want)
		}
	}
	for _, bad := range []string{"-1", "x", "1.5", "9000"} {
		if _, err := backupMaxAge(bad, "", ""); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestBackupBucketView(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if b, err := backupBucket(env(nil)); b != nil || err != nil {
		t.Errorf("nothing set must mean not configured, got %v %v", b, err)
	}
	full := map[string]string{
		"S3_ENDPOINT": "https://acct.r2.cloudflarestorage.com", "BACKUP_S3_BUCKET": "bk",
		"BACKUP_VIEW_ACCESS_KEY_ID": "view", "BACKUP_VIEW_SECRET_ACCESS_KEY": "s",
	}
	if b, err := backupBucket(env(full)); b == nil || err != nil {
		t.Errorf("complete setup (endpoint inherited from S3_ENDPOINT): %v %v", b, err)
	}
	// Half a setup is an error that names what is missing, not a silently dead screen.
	half := map[string]string{"BACKUP_S3_BUCKET": "bk"}
	if _, err := backupBucket(env(half)); err == nil || !strings.Contains(err.Error(), "BACKUP_VIEW_ACCESS_KEY_ID") {
		t.Errorf("half configured: %v", err)
	}
	noEndpoint := map[string]string{"BACKUP_S3_BUCKET": "bk", "BACKUP_VIEW_ACCESS_KEY_ID": "v", "BACKUP_VIEW_SECRET_ACCESS_KEY": "s"}
	if _, err := backupBucket(env(noEndpoint)); err == nil || !strings.Contains(err.Error(), "BACKUP_S3_ENDPOINT") {
		t.Errorf("no endpoint: %v", err)
	}
	// The portal must not be handed the token that can write backups.
	same := map[string]string{
		"BACKUP_S3_ENDPOINT": "https://x", "BACKUP_S3_BUCKET": "bk", "BACKUP_S3_ACCESS_KEY_ID": "write",
		"BACKUP_VIEW_ACCESS_KEY_ID": "write", "BACKUP_VIEW_SECRET_ACCESS_KEY": "s",
	}
	if _, err := backupBucket(env(same)); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("write token as view token: %v", err)
	}
}

func TestMailConfig(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if s, base, err := mailConfig(env(nil)); s != nil || base != "" || err != nil {
		t.Errorf("nothing set means no email: %v %q %v", s, base, err)
	}
	ok := map[string]string{"SMTP_HOST": "smtp.example.com", "SMTP_FROM": "CadPortal <portal@example.com>", "APP_BASE_URL": "https://cad.example.com/"}
	s, base, err := mailConfig(env(ok))
	if s == nil || err != nil || base != "https://cad.example.com" {
		t.Errorf("complete setup: %v %q %v", s, base, err)
	}
	cases := map[string]map[string]string{
		"APP_BASE_URL": {"SMTP_HOST": "h", "SMTP_FROM": "p@example.com"},
		"SMTP_FROM":    {"SMTP_HOST": "h", "APP_BASE_URL": "https://x.example.com"},
		"SMTP_PORT":    {"SMTP_HOST": "h", "SMTP_FROM": "p@example.com", "APP_BASE_URL": "https://x.example.com", "SMTP_PORT": "abc"},
		"SMTP_TLS":     {"SMTP_HOST": "h", "SMTP_FROM": "p@example.com", "APP_BASE_URL": "https://x.example.com", "SMTP_TLS": "ssl"},
		"together":     {"SMTP_HOST": "h", "SMTP_FROM": "p@example.com", "APP_BASE_URL": "https://x.example.com", "SMTP_USERNAME": "u"},
	}
	for want, m := range cases {
		if _, _, err := mailConfig(env(m)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
	// A path in the base address would produce broken links.
	bad := map[string]string{"SMTP_HOST": "h", "SMTP_FROM": "p@example.com", "APP_BASE_URL": "https://x.example.com/portal"}
	if _, _, err := mailConfig(env(bad)); err == nil {
		t.Error("APP_BASE_URL with a path accepted")
	}
}

func TestStartHeartbeatChecksItsSettings(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Off by default: the returned channel is already closed.
	done, err := startHeartbeat(ctx, nil, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	default:
		t.Error("with no HEARTBEAT_URL there is nothing to wait for")
	}
	if _, err := startHeartbeat(ctx, nil, env(map[string]string{"HEARTBEAT_URL": "hc-ping.com/abc"})); err == nil || !strings.Contains(err.Error(), "HEARTBEAT_URL") {
		t.Errorf("a URL without a scheme: %v", err)
	}
	for _, bad := range []string{"0", "-5", "x", "2000"} {
		_, err := startHeartbeat(ctx, nil, env(map[string]string{"HEARTBEAT_URL": "https://hc-ping.com/abc", "HEARTBEAT_INTERVAL_MINUTES": bad}))
		if err == nil || !strings.Contains(err.Error(), "HEARTBEAT_INTERVAL_MINUTES") {
			t.Errorf("interval %q accepted: %v", bad, err)
		}
	}
}

func TestAppBaseURL(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	for in, want := range map[string]string{"": "", "  ": "", "https://cad.example.com/": "https://cad.example.com", "http://localhost:4200": "http://localhost:4200"} {
		if got, err := appBaseURL(env(in)); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"cad.example.com", "ftp://cad.example.com", "https://cad.example.com/portal", "https://cad.example.com/?x=1", "https://"} {
		if _, err := appBaseURL(env(in)); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}
