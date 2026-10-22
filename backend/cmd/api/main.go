package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rpsoftech/cad-portal/backend/internal/handlers"
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
		log.Fatal(err)
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
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{SkipPaths: []string{"/healthz"}}), gin.Recovery())
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
	<-workerDone
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
