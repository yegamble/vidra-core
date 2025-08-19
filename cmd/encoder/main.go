package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"athena/internal/metrics"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"athena/internal/config"
	"athena/internal/repository"
	"athena/internal/usecase"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	db, err := sqlx.Connect("postgres", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer func() { _ = db.Close() }()

	encRepo := repository.NewEncodingRepository(db)
	videoRepo := repository.NewVideoRepository(db)

	uploadsDir := "./uploads"
	if v := os.Getenv("UPLOADS_DIR"); v != "" {
		uploadsDir = v
	}

	svc := usecase.NewEncodingService(encRepo, videoRepo, uploadsDir, cfg)

	// worker count from env (ENCODER_WORKERS)
	workers := 0
	if v := os.Getenv("ENCODER_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			workers = n
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start metrics server
	metricsAddr := os.Getenv("METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = ":9090"
	}
	go func() {
		http.HandleFunc("/metrics", metrics.Handler)
		if err := http.ListenAndServe(metricsAddr, nil); err != nil {
			log.Printf("metrics server error: %v", err)
		}
	}()

	// Kick a quick loop to process existing jobs immediately
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// noop; workers will poll via ProcessNext
			}
		}
	}()

	fmt.Printf("Starting encoder with %d workers...\n", workers)
	if err := svc.Run(ctx, workers); err != nil {
		log.Printf("encoder stopped with error: %v", err)
	}
}
