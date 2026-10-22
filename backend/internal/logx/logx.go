// Package logx sets up structured logging (one JSON object per line, for log tools) and the HTTP
// middleware that gives every request an id and one access-log line.
package logx

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
)

// RequestIDKey is where the request id is kept in the gin context; the response header is X-Request-Id.
const RequestIDKey = "request_id"

// New builds the logger. format is "json" or "text" (empty means text, which is easier to read in a
// terminal); level is debug, info, warn or error (empty means info).
func New(w io.Writer, format, level string) (*slog.Logger, error) {
	var lv slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		lv = slog.LevelInfo
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		return nil, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error, got %q", level)
	}
	opts := &slog.HandlerOptions{Level: lv}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "", "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	}
	return nil, fmt.Errorf("LOG_FORMAT must be json or text, got %q", format)
}

// Setup makes l the default logger. The standard log package is routed through it too, so every
// existing log.Printf line comes out in the same format instead of as loose text.
func Setup(l *slog.Logger) {
	slog.SetDefault(l)
	log.SetFlags(0)
}

var validID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// slowRequest is the duration after which a request is logged as a warning (uploads and downloads of
// large files are expected to be slow, so streaming routes are exempt by status alone, not by path).
const slowRequest = 3 * time.Second

// Middleware assigns a request id (kept from X-Request-Id when the proxy set a sane one) and writes one
// line per request after it ran: method, route, status, duration, size, caller and any internal error.
// The query string is never logged (signed links carry their token there) and neither are bodies.
// Health probes are logged at debug level only, so they do not drown the log.
func Middleware(l *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-Id")
		if !validID.MatchString(id) {
			id = newID()
		}
		c.Set(RequestIDKey, id)
		c.Header("X-Request-Id", id)
		start := time.Now()

		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "(no route)"
		}
		status := c.Writer.Status()
		took := time.Since(start)
		level := slog.LevelInfo
		switch {
		case route == "/healthz" || route == "/readyz":
			level = slog.LevelDebug
		case status >= 500:
			level = slog.LevelError
		case status >= 400 || took > slowRequest:
			level = slog.LevelWarn
		}
		attrs := []slog.Attr{
			slog.String("request_id", id),
			slog.String("method", c.Request.Method),
			slog.String("route", route),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", status),
			slog.Int64("ms", took.Milliseconds()),
			slog.Int("bytes", max(0, c.Writer.Size())),
			slog.String("ip", c.ClientIP()),
		}
		if uid := auth.UserIDFrom(c); uid != "" {
			attrs = append(attrs, slog.String("user", uid), slog.String("role", string(auth.RoleFrom(c))))
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, slog.String("error", c.Errors.String()))
		}
		l.LogAttrs(c.Request.Context(), level, "request", attrs...)
	}
}

// Recovery turns a panic into a logged error with the request id and stack, and a plain 500.
func Recovery(l *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, recovered any) {
		l.Error("panic", "request_id", c.GetString(RequestIDKey), "route", c.FullPath(), "panic", fmt.Sprint(recovered))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error", "request_id": c.GetString(RequestIDKey)})
	})
}
