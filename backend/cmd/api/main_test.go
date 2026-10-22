package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
