package main

import (
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
