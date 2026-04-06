package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/repository"
	"vidra-core/internal/storage"
	"vidra-core/internal/usecase/migration"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

func main() {
	var (
		videoID     string
		batchSize   int
		deleteLocal bool
		testOnly    bool
		dryRun      bool
	)

	flag.StringVar(&videoID, "video-id", "", "Migrate a specific video by ID")
	flag.IntVar(&batchSize, "batch", 10, "Number of videos to migrate in batch mode")
	flag.BoolVar(&deleteLocal, "delete-local", false, "Delete local files after successful migration")
	flag.BoolVar(&testOnly, "test", false, "Test S3 connection only")
	flag.BoolVar(&dryRun, "dry-run", false, "Show what would be migrated without actually migrating")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Setup logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if cfg.LogLevel == "debug" {
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	// Check if S3 is enabled
	if !cfg.EnableS3 {
		logger.Error("S3 is not enabled. Set ENABLE_S3=true in your .env file")
		os.Exit(1)
	}

	// Validate S3 configuration
	if cfg.S3Endpoint == "" || cfg.S3Bucket == "" || cfg.S3AccessKey == "" || cfg.S3SecretKey == "" {
		logger.Error("S3 configuration incomplete. Please set S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY, and S3_SECRET_KEY")
		os.Exit(1)
	}

	logger.Info("S3 Configuration:",
		"endpoint", cfg.S3Endpoint,
		"bucket", cfg.S3Bucket,
		"region", cfg.S3Region,
		"delete_local", deleteLocal,
	)

	// Create S3 backend
	s3Backend, err := storage.NewS3Backend(storage.S3Config{
		Endpoint:  cfg.S3Endpoint,
		Bucket:    cfg.S3Bucket,
		AccessKey: cfg.S3AccessKey,
		SecretKey: cfg.S3SecretKey,
		Region:    cfg.S3Region,
		PathStyle: true, // Required for Backblaze B2
	})
	if err != nil {
		logger.Error("Failed to create S3 backend: %v", "args", err)
	}

	logger.Info("✓ S3 backend created successfully")

	// Test S3 connection if requested
	if testOnly {
		if err := testS3Connection(context.Background(), s3Backend, logger); err != nil {
			logger.Error("S3 connection test failed: %v", "args", err)
		}
		logger.Info("✓ S3 connection test successful!")
		return
	}

	// Connect to database
	db, err := sqlx.Connect("postgres", cfg.DatabaseURL)
	if err != nil {
		logger.Error("Failed to connect to database: %v", "args", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			logger.Error(fmt.Sprintf("Failed to close database connection: %v", err))
		}
	}()

	logger.Info("✓ Database connected")

	// Create repositories
	videoRepo := repository.NewVideoRepository(db)

	// Create storage paths
	storagePaths := storage.NewPaths(cfg.StorageDir)

	// Create migration service
	migrationService := migration.NewS3MigrationService(migration.Config{
		S3Backend:   s3Backend,
		VideoRepo:   videoRepo,
		StoragePath: storagePaths,
		Logger:      logger,
		DeleteLocal: deleteLocal,
	})

	ctx := context.Background()

	// Handle dry-run mode
	if dryRun {
		logger.Info("DRY RUN MODE - No actual migration will occur")
		videos, err := videoRepo.GetVideosForMigration(ctx, batchSize)
		if err != nil {
			logger.Error("Failed to get videos for migration: %v", "args", err)
		}

		if len(videos) == 0 {
			logger.Info("No videos need migration")
			return
		}

		logger.Info(fmt.Sprintf("Found %d videos that would be migrated:", len(videos)))
		for _, v := range videos {
			logger.Info(fmt.Sprintf("  - %s: %s (Status: %s, Tier: %s)", v.ID, v.Title, v.Status, v.StorageTier))
		}
		return
	}

	// Migrate specific video
	if videoID != "" {
		logger.Info(fmt.Sprintf("Migrating video: %s", videoID))
		start := time.Now()

		if err := migrationService.MigrateVideo(ctx, videoID); err != nil {
			logger.Error("Migration failed: %v", "args", err)
		}

		duration := time.Since(start)
		logger.Info(fmt.Sprintf("✓ Migration completed successfully in %v", duration))
		return
	}

	// Migrate batch
	logger.Info(fmt.Sprintf("Migrating batch of %d videos...", batchSize))
	start := time.Now()

	migrated, err := migrationService.MigrateBatch(ctx, batchSize)
	if err != nil {
		logger.Error("Batch migration failed: %v", "args", err)
	}

	duration := time.Since(start)
	logger.Info(fmt.Sprintf("✓ Batch migration completed: %d videos migrated in %v", migrated, duration))

	if migrated == 0 {
		logger.Info("No videos needed migration")
	}
}

// testS3Connection tests the S3 connection by uploading and downloading a test file
func testS3Connection(ctx context.Context, s3Backend *storage.S3Backend, logger *slog.Logger) error {
	testKey := fmt.Sprintf("test/connection-test-%d.txt", time.Now().Unix())
	testContent := "This is a test file to verify S3 connectivity"

	logger.Info(fmt.Sprintf("Testing S3 connection with key: %s", testKey))

	// Test upload
	logger.Info("  1. Testing upload...")
	reader := strings.NewReader(testContent)
	if err := s3Backend.Upload(ctx, testKey, reader, "text/plain"); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	logger.Info("  ✓ Upload successful")

	// Test exists
	logger.Info("  2. Testing file existence check...")
	exists, err := s3Backend.Exists(ctx, testKey)
	if err != nil {
		return fmt.Errorf("exists check failed: %w", err)
	}
	if !exists {
		return fmt.Errorf("file should exist but doesn't")
	}
	logger.Info("  ✓ File exists check successful")

	// Test download
	logger.Info("  3. Testing download...")
	downloadReader, err := s3Backend.Download(ctx, testKey)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	if err := downloadReader.Close(); err != nil {
		logger.Warn(fmt.Sprintf("Failed to close download reader: %v", err))
	}
	logger.Info("  ✓ Download successful")

	// Test metadata
	logger.Info("  4. Testing metadata retrieval...")
	metadata, err := s3Backend.GetMetadata(ctx, testKey)
	if err != nil {
		return fmt.Errorf("metadata retrieval failed: %w", err)
	}
	logger.Info(fmt.Sprintf("  ✓ Metadata retrieved: Size=%d, ContentType=%s", metadata.Size, metadata.ContentType))

	// Test URL generation
	logger.Info("  5. Testing URL generation...")
	url := s3Backend.GetURL(testKey)
	if url == "" {
		return fmt.Errorf("URL generation failed")
	}
	logger.Info(fmt.Sprintf("  ✓ URL generated: %s", url))

	// Test signed URL generation
	logger.Info("  6. Testing signed URL generation...")
	signedURL, err := s3Backend.GetSignedURL(ctx, testKey, 1*time.Hour)
	if err != nil {
		return fmt.Errorf("signed URL generation failed: %w", err)
	}
	logger.Info(fmt.Sprintf("  ✓ Signed URL generated: %s", signedURL[:50]+"..."))

	// Cleanup
	logger.Info("  7. Testing deletion...")
	if err := s3Backend.Delete(ctx, testKey); err != nil {
		return fmt.Errorf("delete failed: %w", err)
	}
	logger.Info("  ✓ Delete successful")

	// Verify deletion
	exists, err = s3Backend.Exists(ctx, testKey)
	if err != nil {
		return fmt.Errorf("exists check after delete failed: %w", err)
	}
	if exists {
		return fmt.Errorf("file should not exist after deletion")
	}
	logger.Info("  ✓ File deletion verified")

	return nil
}
