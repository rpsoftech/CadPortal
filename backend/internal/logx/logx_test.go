package logx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type line map[string]any

func lines(t *testing.T, b *bytes.Buffer) []line {
	t.Helper()
	var out []line
	for _, s := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if s == "" {
			continue
		}
		var l line
		if err := json.Unmarshal([]byte(s), &l); err != nil {
			t.Fatalf("not JSON: %q", s)
		}
		out = append(out, l)
	}
	return out
}

func newEngine(t *testing.T, level string) (*gin.Engine, *bytes.Buffer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	l, err := New(&buf, "json", level)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(Middleware(l), Recovery(l))
	r.GET("/ok/:id", func(c *gin.Context) { c.String(200, "fine") })
	r.GET("/bad", func(c *gin.Context) { c.JSON(400, gin.H{"error": "no"}) })
	r.GET("/boom", func(c *gin.Context) { _ = c.Error(http.ErrAbortHandler); c.Status(500) })
	r.GET("/panic", func(c *gin.Context) { panic("kaboom") })
	r.GET("/healthz", func(c *gin.Context) { c.String(200, "ok") })
	return r, &buf
}

func get(r *gin.Engine, target string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestEveryRequestGetsAnIdAndOneLine(t *testing.T) {
	r, buf := newEngine(t, "info")
	w := get(r, "/ok/42?token=SECRET-SIGNED-LINK")
	id := w.Header().Get("X-Request-Id")
	if len(id) < 8 {
		t.Fatalf("no request id header: %q", id)
	}
	ls := lines(t, buf)
	if len(ls) != 1 {
		t.Fatalf("want one line, got %d: %s", len(ls), buf)
	}
	l := ls[0]
	if l["request_id"] != id || l["route"] != "/ok/:id" || l["path"] != "/ok/42" || l["status"] != float64(200) || l["level"] != "INFO" || l["msg"] != "request" {
		t.Errorf("line: %v", l)
	}
	if strings.Contains(buf.String(), "SECRET") || strings.Contains(buf.String(), "token") {
		t.Errorf("the query string must never be logged: %s", buf)
	}
}

func TestAProxyIdIsKeptOnlyIfItIsSane(t *testing.T) {
	r, _ := newEngine(t, "info")
	if got := get(r, "/ok/1", "X-Request-Id", "abc123def456").Header().Get("X-Request-Id"); got != "abc123def456" {
		t.Errorf("a good id from the proxy must be kept, got %q", got)
	}
	for _, bad := range []string{"short", "has spaces in it ok", "x\"; DROP", strings.Repeat("a", 200)} {
		got := get(r, "/ok/1", "X-Request-Id", bad).Header().Get("X-Request-Id")
		if got == bad || !validID.MatchString(got) {
			t.Errorf("id %q must be replaced, got %q", bad, got)
		}
	}
}

func TestLevelsFollowTheOutcome(t *testing.T) {
	r, buf := newEngine(t, "debug")
	get(r, "/bad")
	get(r, "/boom")
	get(r, "/nothing-here")
	get(r, "/healthz")
	by := map[string]line{}
	for _, l := range lines(t, buf) {
		by[l["path"].(string)] = l
	}
	if by["/bad"]["level"] != "WARN" || by["/boom"]["level"] != "ERROR" || by["/nothing-here"]["level"] != "WARN" || by["/healthz"]["level"] != "DEBUG" {
		t.Errorf("levels: %v", by)
	}
	if !strings.Contains(by["/boom"]["error"].(string), "abort") {
		t.Errorf("an internal error must be in the line: %v", by["/boom"])
	}
	if by["/nothing-here"]["route"] != "(no route)" {
		t.Errorf("unmatched route: %v", by["/nothing-here"])
	}
	// At the default level a health probe leaves no line at all.
	r2, buf2 := newEngine(t, "info")
	get(r2, "/healthz")
	if buf2.Len() != 0 {
		t.Errorf("health probes must be silent at info: %s", buf2)
	}
}

func TestAPanicIsLoggedWithTheIdAndAnswered(t *testing.T) {
	r, buf := newEngine(t, "info")
	w := get(r, "/panic")
	if w.Code != 500 || !strings.Contains(w.Body.String(), w.Header().Get("X-Request-Id")) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !strings.Contains(buf.String(), `"msg":"panic"`) || !strings.Contains(buf.String(), "kaboom") {
		t.Errorf("panic not logged: %s", buf)
	}
}

func TestNewRefusesUnknownSettings(t *testing.T) {
	var b bytes.Buffer
	if _, err := New(&b, "xml", ""); err == nil {
		t.Error("format")
	}
	if _, err := New(&b, "", "loud"); err == nil {
		t.Error("level")
	}
	if _, err := New(&b, "", ""); err != nil {
		t.Errorf("defaults: %v", err)
	}
}
