package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rpsoftech/cad-portal/backend/internal/backup"
	"github.com/rpsoftech/cad-portal/backend/internal/handlers"
	"github.com/rpsoftech/cad-portal/backend/internal/heartbeat"
	"github.com/rpsoftech/cad-portal/backend/internal/logx"
	"github.com/rpsoftech/cad-portal/backend/internal/mailer"
	"github.com/rpsoftech/cad-portal/backend/internal/migrate"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

// shutdownGrace is how long in-flight requests (uploads included) may finish after SIGTERM.
const shutdownGrace = 30 * time.Second

func main() {
	// `api healthcheck` probes the running server; the container image has no curl.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := healthcheck(listenAddr()); err != nil {
			fmt.Fprintln(os.Stderr, "unhealthy:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func listenAddr() string {
	if a := os.Getenv("HTTP_ADDR"); a != "" {
		return a
	}
	return ":8080"
}

func healthcheck(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func run() error {
	logger, err := logx.New(os.Stderr, os.Getenv("LOG_FORMAT"), os.Getenv("LOG_LEVEL"))
	if err != nil {
		return err
	}
	logx.Setup(logger)

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	jwtSecret := os.Getenv("JWT_SECRET")
	if len(jwtSecret) < 32 {
		return errors.New("JWT_SECRET is required and must be at least 32 characters")
	}

	// SIGTERM is what `docker stop` and Kubernetes send.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if os.Getenv("AUTO_MIGRATE") == "true" {
		if err := migrate.Up(dbURL); err != nil {
			return err
		}
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	svc := service.New(pool)
	localStore, err := configureStorage(svc, jwtSecret)
	if err != nil {
		return err
	}
	if localStore != nil {
		defer localStore.Close()
	}

	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		service.RunRecalcWorker(ctx, pool, func(err error) { log.Printf("recalc worker: %v", err) })
	}()

	retentionDays, err := auditRetentionDays(os.Getenv("AUDIT_RETENTION_DAYS"))
	if err != nil {
		return err
	}
	svc.Retention, err = fileRetention(os.Getenv("DELETED_FILE_RETENTION_DAYS"))
	if err != nil {
		return err
	}
	svc.BackupMaxAge, err = backupMaxAge(os.Getenv("BACKUP_MAX_AGE_HOURS"), os.Getenv("STORAGE"), os.Getenv("S3_BUCKET"))
	if err != nil {
		return err
	}
	sender, baseURL, err := mailConfig(os.Getenv)
	if err != nil {
		return err
	}
	if svc.AppBaseURL, err = appBaseURL(os.Getenv); err != nil {
		return err
	}
	if sender != nil {
		svc.Mail, svc.AppBaseURL = sender, baseURL
		log.Printf("password reset by email is on (SMTP host %s)", os.Getenv("SMTP_HOST"))
	}
	bucket, err := backupBucket(os.Getenv)
	if err != nil {
		return err
	}
	if bucket != nil {
		svc.BackupBucket = bucket
	}
	heartbeatDone, err := startHeartbeat(ctx, pool, os.Getenv)
	if err != nil {
		return err
	}
	purgeDone := make(chan struct{})
	go func() {
		defer close(purgeDone)
		service.RunTrashPurge(ctx, svc)
	}()
	pruneDone := make(chan struct{})
	go func() {
		defer close(pruneDone)
		runAuditRetention(ctx, svc, retentionDays)
	}()

	// Release mode comes from GIN_MODE=release. No proxy is trusted unless configured,
	// so client addresses cannot be spoofed with forwarded headers.
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		return err
	}
	r.Use(logx.Middleware(logger), logx.Recovery(logger))
	(&handlers.H{
		S: svc, JWTSecret: []byte(jwtSecret), Validate: svc.ValidateToken, LocalFiles: localStore,
	}).Register(r)

	srv := &http.Server{
		Addr:              listenAddr(),
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No Read/WriteTimeout: CAD uploads and downloads stream large bodies.
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	log.Printf("listening on %s", srv.Addr)

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	log.Print("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	err = srv.Shutdown(shutCtx)
	svc.WaitMail()
	<-workerDone
	<-heartbeatDone
	<-pruneDone
	<-purgeDone
	return err
}

// configureStorage selects the object store: an explicit STORAGE wins; otherwise R2
// when a bucket is configured; otherwise uploads are disabled.
func configureStorage(svc *service.Service, jwtSecret string) (*storage.Local, error) {
	switch mode, err := storageMode(os.Getenv("STORAGE"), os.Getenv("S3_BUCKET")); {
	case err != nil:
		return nil, err
	case mode == "r2":
		if err := checkR2Env(os.Getenv); err != nil {
			return nil, err
		}
		store, err := storage.NewR2(storage.Config{
			Endpoint: os.Getenv("S3_ENDPOINT"), Region: os.Getenv("S3_REGION"),
			Bucket:      os.Getenv("S3_BUCKET"),
			AccessKeyID: os.Getenv("S3_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		})
		if err != nil {
			return nil, err
		}
		svc.Store = store
	case mode == "local":
		dir := os.Getenv("LOCAL_STORAGE_DIR")
		if dir == "" {
			dir = "data/uploads"
		}
		local, err := storage.NewLocal(dir, []byte(jwtSecret))
		if err != nil {
			return nil, err
		}
		svc.Store = local
		log.Printf("WARNING: CAD files are stored on local disk (%s). This is for development only; use R2 in production.", dir)
		return local, nil
	default:
		log.Print("No storage configured (set S3_BUCKET for R2, or STORAGE=local for development): CAD upload/download disabled")
	}
	return nil, nil
}

func storageMode(explicit, bucket string) (string, error) {
	switch explicit {
	case "local", "r2":
		return explicit, nil
	case "":
		if bucket != "" {
			return "r2", nil
		}
		return "", nil
	default:
		return "", fmt.Errorf("STORAGE must be %q or %q, got %q", "local", "r2", explicit)
	}
}

// auditRetentionDays parses AUDIT_RETENTION_DAYS. Empty or 0 keeps the log forever.
func auditRetentionDays(v string) (int, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 36500 {
		return 0, fmt.Errorf("AUDIT_RETENTION_DAYS must be a whole number of days (0 keeps everything), got %q", v)
	}
	return n, nil
}

// runAuditRetention deletes audit entries older than the retention window, once at start
// and then hourly, until ctx is cancelled. With days == 0 it does nothing.
func runAuditRetention(ctx context.Context, svc *service.Service, days int) {
	if days == 0 {
		return
	}
	prune := func() {
		cutoff := time.Now().AddDate(0, 0, -days)
		if n, err := svc.PruneAudit(ctx, cutoff); err != nil {
			log.Printf("audit retention: %v", err)
		} else if n > 0 {
			log.Printf("audit retention: removed %d entries older than %d days", n, days)
		}
	}
	prune()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			prune()
		}
	}
}

// checkR2Env says exactly what is wrong with an incomplete or placeholder R2 setup, instead of a
// generic "required" that leaves the API restarting in a loop with nobody knowing which of the
// four values to fix.
func checkR2Env(getenv func(string) string) error {
	var missing []string
	for _, name := range []string{"S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY"} {
		if strings.TrimSpace(getenv(name)) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("R2 storage is selected (S3_BUCKET is set) but %s %s empty: fill them in, or clear S3_BUCKET to run without file storage, or set STORAGE=local to try it out on local disk",
			strings.Join(missing, ", "), map[bool]string{true: "is", false: "are"}[len(missing) == 1])
	}
	if strings.ContainsAny(getenv("S3_ENDPOINT"), "<>") {
		return fmt.Errorf("S3_ENDPOINT still holds the placeholder from .env.example (%q): put your Cloudflare account id in it", getenv("S3_ENDPOINT"))
	}
	return nil
}

// appBaseURL reads APP_BASE_URL, the public address of the portal (no path, no trailing slash). It is
// empty when unset. Emailed reset links and the QR codes on job bags are built from it.
func appBaseURL(getenv func(string) string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(getenv("APP_BASE_URL")), "/")
	if base == "" {
		return "", nil
	}
	if u, err := url.Parse(base); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("APP_BASE_URL must be the public address of the portal, like https://cad.example.com")
	}
	return base, nil
}

// mailConfig reads the SMTP settings that make "forgot password" send emails. With SMTP_HOST empty
// nothing is configured and people are told to ask an admin for a reset link. APP_BASE_URL is the public
// address the emailed link points to, so it is required whenever mail is on.
func mailConfig(getenv func(string) string) (mailer.Sender, string, error) {
	val := func(n string) string { return strings.TrimSpace(getenv(n)) }
	if val("SMTP_HOST") == "" {
		return nil, "", nil
	}
	base, err := appBaseURL(getenv)
	if err != nil || base == "" {
		return nil, "", errors.New("APP_BASE_URL must be the public address of the portal, like https://cad.example.com, when SMTP_HOST is set")
	}
	port := 587
	if v := val("SMTP_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, "", fmt.Errorf("SMTP_PORT must be a number, got %q", v)
		}
		port = n
	}
	mode := mailer.TLSMode(strings.ToLower(val("SMTP_TLS")))
	if mode == "" {
		mode = mailer.STARTTLS
		if port == 465 {
			mode = mailer.Implicit
		}
	}
	m, err := mailer.New(mailer.Config{
		Host: val("SMTP_HOST"), Port: port, Username: val("SMTP_USERNAME"), Password: getenv("SMTP_PASSWORD"),
		From: val("SMTP_FROM"), TLS: mode,
	})
	if err != nil {
		return nil, "", err
	}
	return m, base, nil
}

// startHeartbeat pings HEARTBEAT_URL every HEARTBEAT_INTERVAL_MINUTES (default 5) while the API can reach
// its database, so an outside monitor notices when the portal goes quiet. Not set means no heartbeat. The
// returned channel closes when it has stopped.
func startHeartbeat(ctx context.Context, pool *pgxpool.Pool, getenv func(string) string) (<-chan struct{}, error) {
	done := make(chan struct{})
	url := strings.TrimSpace(getenv("HEARTBEAT_URL"))
	if url == "" {
		close(done)
		return done, nil
	}
	if err := heartbeat.Validate(url); err != nil {
		return nil, fmt.Errorf("HEARTBEAT_URL: %w", err)
	}
	mins := 5
	if v := strings.TrimSpace(getenv("HEARTBEAT_INTERVAL_MINUTES")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1440 {
			return nil, fmt.Errorf("HEARTBEAT_INTERVAL_MINUTES must be 1 to 1440, got %q", v)
		}
		mins = n
	}
	slog.Info("heartbeat on", "host", heartbeat.Host(url), "every_minutes", mins)
	go func() {
		defer close(done)
		heartbeat.Run(ctx, url, time.Duration(mins)*time.Minute, pool.Ping,
			func(format string, a ...any) { slog.Warn(fmt.Sprintf(format, a...)) })
	}()
	return done, nil
}

// backupBucket gives the portal a read-only view of the backup bucket, so the Backups screen can list
// and offer the stored dumps. It uses its own token (BACKUP_VIEW_*), never the one the backup service
// writes with. Nothing set means the screen shows run history only; a half-filled setup is an error.
func backupBucket(getenv func(string) string) (*backup.S3, error) {
	val := func(n string) string { return strings.TrimSpace(getenv(n)) }
	required := []string{"BACKUP_S3_BUCKET", "BACKUP_VIEW_ACCESS_KEY_ID", "BACKUP_VIEW_SECRET_ACCESS_KEY"}
	var missing []string
	for _, n := range required {
		if val(n) == "" {
			missing = append(missing, n)
		}
	}
	if len(missing) == len(required) {
		return nil, nil // not configured: the screen shows run history only
	}
	endpoint := val("BACKUP_S3_ENDPOINT")
	if endpoint == "" {
		endpoint = val("S3_ENDPOINT")
	}
	if endpoint == "" {
		missing = append(missing, "BACKUP_S3_ENDPOINT")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("the backup bucket view is half configured: %s empty (set all of them, or none)", strings.Join(missing, ", "))
	}
	if w := val("BACKUP_S3_ACCESS_KEY_ID"); w != "" && w == val("BACKUP_VIEW_ACCESS_KEY_ID") {
		return nil, errors.New("BACKUP_VIEW_ACCESS_KEY_ID is the backup service's write token: give the portal a separate read-only token")
	}
	return backup.NewS3(backup.S3Config{
		Endpoint: endpoint, Region: val("BACKUP_S3_REGION"), Bucket: val("BACKUP_S3_BUCKET"),
		AccessKeyID: val("BACKUP_VIEW_ACCESS_KEY_ID"), SecretAccessKey: val("BACKUP_VIEW_SECRET_ACCESS_KEY"),
	})
}

// backupMaxAge parses BACKUP_MAX_AGE_HOURS: how old the last good backup may be before the setup
// checklist warns. Empty means 36 hours when files live in R2 (a real installation) and no check
// otherwise (local development has no backup service); 0 turns the check off.
func backupMaxAge(v, storageEnv, bucket string) (time.Duration, error) {
	if v == "" {
		if mode, err := storageMode(storageEnv, bucket); err == nil && mode == "r2" {
			return 36 * time.Hour, nil
		}
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 8760 {
		return 0, fmt.Errorf("BACKUP_MAX_AGE_HOURS must be a whole number of hours (0 turns the warning off), got %q", v)
	}
	return time.Duration(n) * time.Hour, nil
}

// fileRetention parses DELETED_FILE_RETENTION_DAYS: how long a removed or replaced CAD file, photo or
// video is kept (and can be restored) before it is deleted for good. Empty means 30 days, and 0
// deletes at once.
func fileRetention(v string) (time.Duration, error) {
	if v == "" {
		return service.DefaultRetention, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 3650 {
		return 0, fmt.Errorf("DELETED_FILE_RETENTION_DAYS must be a whole number of days (0 deletes at once), got %q", v)
	}
	return time.Duration(n) * 24 * time.Hour, nil
}
