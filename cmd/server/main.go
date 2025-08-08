package main

import (
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"github.com/yourname/gotube/internal/cache"
	"github.com/yourname/gotube/internal/config"
	"github.com/yourname/gotube/internal/db"
	"github.com/yourname/gotube/internal/httpapi"
	"github.com/yourname/gotube/internal/ipfs"
	"github.com/yourname/gotube/internal/storage"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()

	// DB
	sqlxDB, err := db.Open(cfg)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer sqlxDB.Close()

	// Redis
	rdb := cache.New(cfg)
	defer rdb.Close()

	// IPFS
	ipfsClient, err := ipfs.NewClient(cfg)
	if err != nil {
		log.Fatalf("ipfs client: %v", err)
	}

	// Object Storage (S3-compatible via MinIO SDK)
	s3, err := storage.NewS3Client(cfg)
	if err != nil {
		log.Fatalf("s3 client: %v", err)
	}

	// HTTP Router
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// Health
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// API routes
	httpapi.Mount(r, sqlxDB, rdb, ipfsClient, s3, cfg)

	srv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("listening on :%s", cfg.HTTPPort)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}
