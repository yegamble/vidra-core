package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vidra-core/internal/config"
)

func TestNew(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup test config
	cfg := getTestConfig(t)

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create app: %v", err)
	}

	if app == nil {
		t.Fatal("Expected app to be non-nil")
	}

	if app.Config == nil {
		t.Error("Expected config to be set")
	}

	if app.DB == nil {
		t.Error("Expected DB to be initialized")
	}

	if app.Redis == nil {
		t.Error("Expected Redis to be initialized")
	}

	if app.Router == nil {
		t.Error("Expected Router to be initialized")
	}

	if app.Dependencies == nil {
		t.Error("Expected Dependencies to be initialized")
	}

	// Cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = app.Shutdown(ctx)
}

func TestInitializeDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := getTestConfig(t)
	app := &Application{
		Config: cfg,
	}

	err := app.initializeDatabase()
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}

	if app.DB == nil {
		t.Error("Expected DB to be initialized")
	}

	// Test database connection
	err = app.DB.Ping()
	if err != nil {
		t.Errorf("Failed to ping database: %v", err)
	}

	// Cleanup
	if app.DB != nil {
		app.DB.Close()
	}
}

func TestInitializeRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := getTestConfig(t)
	app := &Application{
		Config: cfg,
	}

	err := app.initializeRedis()
	if err != nil {
		t.Fatalf("Failed to initialize Redis: %v", err)
	}

	if app.Redis == nil {
		t.Error("Expected Redis to be initialized")
	}

	// Test Redis connection
	ctx := context.Background()
	err = app.Redis.Ping(ctx).Err()
	if err != nil {
		t.Errorf("Failed to ping Redis: %v", err)
	}

	// Cleanup
	if app.Redis != nil {
		app.Redis.Close()
	}
}

func TestInitializeStorageDirectories(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	cfg := &config.Config{
		StorageDir: tmpDir,
	}

	app := &Application{
		Config: cfg,
	}

	err := app.initializeStorageDirectories()
	if err != nil {
		t.Fatalf("Failed to initialize storage directories: %v", err)
	}

	// Verify directories were created
	expectedDirs := []string{
		"avatars",
		"cache",
		"captions",
		"logs",
		"previews",
		filepath.Join("streaming-playlists", "hls"),
		"thumbnails",
		"torrents",
		"web-videos",
		"storyboards",
	}

	for _, dir := range expectedDirs {
		fullPath := filepath.Join(tmpDir, dir)
		info, err := os.Stat(fullPath)
		if err != nil {
			t.Errorf("Expected directory %s to exist: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("Expected %s to be a directory", dir)
		}
	}
}

func TestStartAndShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := getTestConfig(t)
	cfg.EnableEncodingScheduler = false // Disable scheduler for test
	cfg.EnableEncoding = false

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create app: %v", err)
	}

	ctx := context.Background()

	// Start the app
	err = app.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start app: %v", err)
	}

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	// Shutdown the app
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = app.Shutdown(shutdownCtx)
	if err != nil {
		t.Errorf("Failed to shutdown app: %v", err)
	}
}

func TestGetRouter(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := getTestConfig(t)
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create app: %v", err)
	}

	router := app.GetRouter()
	if router == nil {
		t.Error("Expected router to be non-nil")
	}

	// Cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = app.Shutdown(ctx)
}

func TestInitializeDependencies(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := getTestConfig(t)
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create app: %v", err)
	}

	deps := app.Dependencies

	// Check repositories
	if deps.UserRepo == nil {
		t.Error("Expected UserRepo to be initialized")
	}
	if deps.VideoRepo == nil {
		t.Error("Expected VideoRepo to be initialized")
	}
	if deps.UploadRepo == nil {
		t.Error("Expected UploadRepo to be initialized")
	}
	if deps.EncodingRepo == nil {
		t.Error("Expected EncodingRepo to be initialized")
	}
	if deps.MessageRepo == nil {
		t.Error("Expected MessageRepo to be initialized")
	}
	if deps.AuthRepo == nil {
		t.Error("Expected AuthRepo to be initialized")
	}
	if deps.OAuthRepo == nil {
		t.Error("Expected OAuthRepo to be initialized")
	}
	if deps.SubRepo == nil {
		t.Error("Expected SubRepo to be initialized")
	}
	if deps.ViewsRepo == nil {
		t.Error("Expected ViewsRepo to be initialized")
	}
	if deps.NotificationRepo == nil {
		t.Error("Expected NotificationRepo to be initialized")
	}
	if deps.ChannelRepo == nil {
		t.Error("Expected ChannelRepo to be initialized")
	}
	if deps.CommentRepo == nil {
		t.Error("Expected CommentRepo to be initialized")
	}
	if deps.RatingRepo == nil {
		t.Error("Expected RatingRepo to be initialized")
	}
	if deps.PlaylistRepo == nil {
		t.Error("Expected PlaylistRepo to be initialized")
	}
	if deps.CaptionRepo == nil {
		t.Error("Expected CaptionRepo to be initialized")
	}

	// Check services
	if deps.UploadService == nil {
		t.Error("Expected UploadService to be initialized")
	}
	if deps.MessageService == nil {
		t.Error("Expected MessageService to be initialized")
	}
	if deps.ViewsService == nil {
		t.Error("Expected ViewsService to be initialized")
	}
	if deps.NotificationService == nil {
		t.Error("Expected NotificationService to be initialized")
	}
	if deps.ChannelService == nil {
		t.Error("Expected ChannelService to be initialized")
	}
	if deps.CommentService == nil {
		t.Error("Expected CommentService to be initialized")
	}
	if deps.RatingService == nil {
		t.Error("Expected RatingService to be initialized")
	}
	if deps.PlaylistService == nil {
		t.Error("Expected PlaylistService to be initialized")
	}
	if deps.CaptionService == nil {
		t.Error("Expected CaptionService to be initialized")
	}

	// Cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = app.Shutdown(ctx)
}

// Helper function to get test configuration
func getTestConfig(t *testing.T) *config.Config {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("TEST_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("DATABASE_URL or TEST_DATABASE_URL not set")
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6380/0"
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "test-jwt-secret"
	}

	tmpDir := t.TempDir()

	return &config.Config{
		DatabaseURL:                        databaseURL,
		RedisURL:                           redisURL,
		JWTSecret:                          jwtSecret,
		StorageDir:                         tmpDir,
		IPFSApi:                            "http://localhost:5001",
		IPFSCluster:                        "http://localhost:9094",
		RequireIPFS:                        false,
		RedisPingTimeout:                   5,
		IPFSPingTimeout:                    5,
		EnableEncodingScheduler:            false,
		EnableEncoding:                     false,
		EnableATProto:                      false,
		EnableFederationScheduler:          false,
		EncodingSchedulerIntervalSeconds:   60,
		EncodingSchedulerBurst:             5,
		FederationSchedulerIntervalSeconds: 60,
		FederationSchedulerBurst:           5,
		EncodingWorkers:                    2,
		MetricsAddr:                        "", // Don't start metrics server in tests
	}
}
