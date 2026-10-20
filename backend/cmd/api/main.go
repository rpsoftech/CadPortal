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
	return err
}

// configureStorage selects the object store: an explicit STORAGE wins; otherwise R2
// when a bucket is configured; otherwise uploads are disabled.
func configureStorage(svc *service.Service, jwtSecret string) (*storage.Local, error) {
	switch mode, err := storageMode(os.Getenv("STORAGE"), os.Getenv("S3_BUCKET")); {
	case err != nil:
		return nil, err
	case mode == "r2":
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
