package main

import (
    "context"
    "database/sql"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/go-redis/redis/v8"
    "github.com/jmoiron/sqlx"

    _ "github.com/go-sql-driver/mysql" // MySQL driver

    "gotube/internal/api"
    "gotube/internal/config"
    "gotube/internal/chunk"
    "gotube/internal/jobs"
    "gotube/internal/repository"
    "gotube/internal/service"
    "gotube/internal/usecase"
)

func main() {
    // Load configuration
    cfg, err := config.Load()
    if err != nil {
        log.Fatalf("config error: %v", err)
    }

    // Set up DB connection
    db, err := sqlx.Open("mysql", cfg.DB.DSN)
    if err != nil {
        log.Fatalf("db connection: %v", err)
    }
    // Ensure connection works
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := db.PingContext(ctx); err != nil {
        log.Fatalf("db ping: %v", err)
    }
    // Set reasonable pool sizes
    db.SetMaxOpenConns(25)
    db.SetMaxIdleConns(25)
    db.SetConnMaxLifetime(5 * time.Minute)

    // Set up Redis client
    rdb := redis.NewClient(&redis.Options{
        Addr:     cfg.Redis.Addr,
        Password: cfg.Redis.Password,
        DB:       cfg.Redis.DB,
    })
    if err := rdb.Ping(ctx).Err(); err != nil {
        log.Fatalf("redis ping: %v", err)
    }

    // Initialize external services
    ipfsSvc := service.NewIPFSService(cfg.IPFS.APIURL)
    iotaSvc := service.NewIOTAService(cfg.IOTA.NodeURL, cfg.IOTA.Seed)
    mailer := &service.Mailer{
        Host:     cfg.SMTP.Host,
        Port:     cfg.SMTP.Port,
        Username: cfg.SMTP.Username,
        Password: cfg.SMTP.Password,
        From:     cfg.SMTP.From,
    }

    // Repositories
    userRepo := repository.NewMySQLUserRepository(db)
    videoRepo := repository.NewMySQLVideoRepository(db)

    // Usecases
    authUC := &usecase.AuthUsecase{
        Users:       userRepo,
        Mailer:      mailer,
        IOTA:        iotaSvc,
        JWTSecret:   cfg.JWTSecret,
        TokenExpiry: 24 * time.Hour,
        RedisClient: rdb,
    }
    videoUC := &usecase.VideoUsecase{
        Videos:   videoRepo,
        IPFS:     ipfsSvc,
        IOTA:     iotaSvc,
        Redis:    rdb,
        UploadDir: "./uploads",
    }

    // Handlers
    authHandler := &api.AuthHandler{Usecase: authUC}
    videoHandler := &api.VideoHandler{Usecase: videoUC}
    // Chunk manager and handler for large uploads
    chunkManager := chunk.NewChunkedUploadManager(rdb, "./tmp", 32*1024*1024, 64*1024*1024)
    chunkHandler := &api.ChunkHandler{Manager: chunkManager}

    // Router
    router := api.NewRouter(authHandler, videoHandler, chunkHandler, cfg.JWTSecret, rdb)

    // Start transcoder worker
    transcoder := jobs.NewTranscoder(videoRepo, ipfsSvc, iotaSvc, rdb, "./videos")
    workerCtx, workerCancel := context.WithCancel(context.Background())
    transcoder.Start(workerCtx)

    // Start HTTP server
    srv := &http.Server{
        Addr:    ":" + cfg.ServerPort,
        Handler: router,
    }

    // Graceful shutdown handling
    shutdown := make(chan os.Signal, 1)
    signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
    go func() {
        <-shutdown
        log.Println("shutting down...")
        // Stop worker
        workerCancel()
        // Shutdown HTTP server
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        if err := srv.Shutdown(ctx); err != nil {
            log.Printf("server shutdown: %v", err)
        }
        // Close DB and Redis
        db.Close()
        rdb.Close()
    }()

    log.Printf("GoTube server starting on port %s", cfg.ServerPort)
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        log.Fatalf("server error: %v", err)
    }
}