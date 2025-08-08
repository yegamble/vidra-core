package main

import (
    "log"
    "net/http"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "github.com/joho/godotenv"

    "github.com/yegamble/athena/internal/cache"
    "github.com/yegamble/athena/internal/config"
    "github.com/yegamble/athena/internal/db"
    "github.com/yegamble/athena/internal/httpapi"
    "github.com/yegamble/athena/internal/ipfs"
    "github.com/yegamble/athena/internal/storage"
)

func main() {
    // Load environment variables from a `.env` file if present.  It's safe to ignore
    // any error here since missing env files are not fatal.
    _ = godotenv.Load()
    cfg := config.Load()

    // Initialize database connection.  Abort on failure because the application
    // cannot proceed without a working database.
    sqlxDB, err := db.Open(cfg)
    if err != nil {
        log.Fatalf("db open: %v", err)
    }
    defer sqlxDB.Close()

    // Initialize Redis client.  Redis is used for caching and may be optional in
    // some deployments, but here we treat connection failures as fatal.
    rdb := cache.New(cfg)
    defer rdb.Close()

    // Initialize IPFS client.  By default this connects to a local Kubo daemon
    // but can be configured via the `IPFS_PATH` environment variable to point
    // at a remote API (e.g. http://kubo:5001 when using docker compose).
    ipfsClient, err := ipfs.NewClient(cfg)
    if err != nil {
        log.Fatalf("ipfs client: %v", err)
    }

    // Initialize S3-compatible object storage using MinIO SDK.  The
    // configuration determines the endpoint, access keys and bucket name.
    s3, err := storage.NewS3Client(cfg)
    if err != nil {
        log.Fatalf("s3 client: %v", err)
    }

    // Configure the HTTP router with sensible middleware for request IDs,
    // real IP headers, panic recovery and request timeouts.
    r := chi.NewRouter()
    r.Use(middleware.RequestID)
    r.Use(middleware.RealIP)
    r.Use(middleware.Recoverer)
    r.Use(middleware.Timeout(60 * time.Second))

    // Health endpoint for readiness probes.
    r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    })

    // Mount all API routes defined in the internal httpapi package.
    httpapi.Mount(r, sqlxDB, rdb, ipfsClient, s3, cfg)

    // Start the HTTP server using the configured port.  We explicitly set
    // ReadHeaderTimeout to protect against slowloris attacks.
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