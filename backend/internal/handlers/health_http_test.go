package handlers

import (
	"strings"
	"testing"
)

func TestHealthEndpoints(t *testing.T) {
	r, svc := newAuthEnv(t)

	// Both are public: container runtimes probe them without credentials.
	if w := do(r, "GET", "/healthz", "", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok"`) {
		t.Errorf("healthz: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", "/readyz", "", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"ready"`) {
		t.Errorf("readyz: %d %s", w.Code, w.Body)
	}

	// With the database gone: still alive, no longer ready, and no internals leak.
	svc.Pool.Close()
	if w := do(r, "GET", "/healthz", "", "", nil); w.Code != 200 {
		t.Errorf("healthz must not depend on the database: %d", w.Code)
	}
	w := do(r, "GET", "/readyz", "", "", nil)
	if w.Code != 503 {
		t.Fatalf("readyz without a database: %d", w.Code)
	}
	if body := strings.ToLower(w.Body.String()); strings.Contains(body, "pool") || strings.Contains(body, "closed") || strings.Contains(body, "postgres") {
		t.Errorf("readyz leaks internals: %s", w.Body)
	}
}
