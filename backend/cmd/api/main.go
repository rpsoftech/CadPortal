package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rpsoftech/cad-portal/backend/internal/handlers"
	"github.com/rpsoftech/cad-portal/backend/internal/migrate"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if os.Getenv("AUTO_MIGRATE") == "true" {
		if err := migrate.Up(dbURL); err != nil {
			log.Fatal(err)
		}
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	jwtSecret := os.Getenv("JWT_SECRET")
	if len(jwtSecret) < 32 {
		log.Fatal("JWT_SECRET is required and must be at least 32 characters")
	}

	svc := service.New(pool)
	var localStore *storage.Local
	switch mode := storageMode(os.Getenv("STORAGE"), os.Getenv("S3_BUCKET")); mode {
	case "r2":
		store, err := storage.NewR2(storage.Config{
			Endpoint: os.Getenv("S3_ENDPOINT"), Region: os.Getenv("S3_REGION"),
			Bucket:      os.Getenv("S3_BUCKET"),
			AccessKeyID: os.Getenv("S3_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		})
		if err != nil {
			log.Fatal(err)
		}
		svc.Store = store
	case "local":
		dir := os.Getenv("LOCAL_STORAGE_DIR")
		if dir == "" {
			dir = "data/uploads"
		}
		var err error
		if localStore, err = storage.NewLocal(dir, []byte(jwtSecret)); err != nil {
			log.Fatal(err)
		}
		defer localStore.Close()
		svc.Store = localStore
		log.Printf("WARNING: CAD files are stored on local disk (%s). This is for development only; use R2 in production.", dir)
	default:
		log.Print("No storage configured (set S3_BUCKET for R2, or STORAGE=local for development): CAD upload/download disabled")
	}
	go service.RunRecalcWorker(ctx, pool, func(err error) { log.Printf("recalc worker: %v", err) })

	r := gin.Default()
	(&handlers.H{S: svc, JWTSecret: []byte(jwtSecret), LocalFiles: localStore}).Register(r)
	go func() {
		<-ctx.Done()
		os.Exit(0)
	}()
	log.Fatal(r.Run(addr))
}

// storageMode picks the object store: an explicit STORAGE wins; otherwise R2 when
// a bucket is configured; otherwise none.
func storageMode(explicit, bucket string) string {
	switch explicit {
	case "local", "r2":
		return explicit
	case "":
		if bucket != "" {
			return "r2"
		}
		return ""
	default:
		log.Fatalf("STORAGE must be %q or %q, got %q", "local", "r2", explicit)
		return ""
	}
}
