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
