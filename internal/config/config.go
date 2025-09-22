package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	// Server Configuration
	Port int

	// Database Configuration
	DatabaseURL string

	// Redis Configuration
	RedisURL string

	// IPFS Configuration
	IPFSApi     string
	IPFSCluster string

	// IOTA Configuration
	IOTANodeURL string

	// FFmpeg Configuration
	FFMPEGPath string

	// JWT Configuration
	JWTSecret string

	// Feature Flags
	EnableIOTA bool
	EnableIPFS bool
	EnableS3   bool

	// Storage Configuration
	StorageDir string

	// S3-Compatible Storage Configuration
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3Region    string

	// Upload Configuration
	MaxUploadSize        int64
	ChunkSize            int64
	MaxConcurrentUploads int

	// Processing Configuration
	MaxProcessingWorkers int
	ProcessingTimeout    int

	// Rate Limiting Configuration
	RateLimitRequests int
	RateLimitWindow   int

	// CORS Configuration
	CORSAllowedOrigins string
	CORSAllowedMethods string
	CORSAllowedHeaders string

	// Logging Configuration
	LogLevel  string
	LogFormat string

	// Health Check Configuration
	HealthCheckTimeout int
	DBPingTimeout      int
	RedisPingTimeout   int
	IPFSPingTimeout    int
	RequireIPFS        bool

	// Video Processing Configuration
	VideoQualities     []string
	HLSSegmentDuration int
	ThumbnailCount     int

	// HLS Signing Configuration
	HLSSecret   string
	HLSTokenTTL int

	// Storage Tiers Configuration
	HotStorageLimit    string
	WarmStorageLimit   string
	ColdStorageEnabled bool

	// Pinning Strategy Configuration
	PinningReplicationFactor int
	PinningScoreThreshold    float64
	PinningBackupEnabled     bool

	// Session Configuration
	SessionTimeout      int
	RefreshTokenTimeout int

	// API Configuration
	APITimeout                int
	APIMaxRequestSize         string
	APIPaginationDefaultLimit int
	APIPaginationMaxLimit     int

	// Validation Configuration
	ValidationStrictMode          bool
	ValidationAllowedAlgorithms   []string
	ValidationTestMode            bool
	ValidationEnableIntegrityJobs bool
	ValidationLogEvents           bool

	// Encoding Scheduler
	EnableEncodingScheduler          bool
	EncodingSchedulerIntervalSeconds int
	EncodingSchedulerBurst           int

	// Image/Avatar Encoding
	WebPQuality int

	// Encoding Worker Configuration
	EnableEncoding  bool
	EncodingWorkers int
	MetricsAddr     string

	// ATProto Integration
	EnableATProto                 bool
	ATProtoPDSURL                 string
	ATProtoAuthToken              string
	ATProtoHandle                 string
	ATProtoAppPassword            string
	ATProtoTokenKey               string
	ATProtoRefreshIntervalSeconds int
	ATProtoUseImageEmbed          bool
	ATProtoImageAltField          string

	// Public URL for embeds/links
	PublicBaseURL string

	// Federation Scheduler
	EnableFederationScheduler          bool
	FederationSchedulerIntervalSeconds int
	FederationSchedulerBurst           int
	FederationIngestIntervalSeconds    int
	FederationIngestMaxItems           int
	FederationIngestMaxPages           int

	// ATProto Social Features
	EnableATProtoLabeler bool
}

func Load() (*Config, error) {
	// Load .env file if it exists
	if err := godotenv.Load(); err != nil {
		// It's okay if .env file doesn't exist, we'll use environment variables
		// or defaults
		_ = err // Suppress linting error for empty branch
	}

	cfg := &Config{}

	port := flag.Int("port", 8080, "Server port")
	flag.Parse()

	cfg.Port = *port
	if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			cfg.Port = p
		}
	}

	cfg.DatabaseURL = getEnvOrDefault("DATABASE_URL", "")
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	cfg.RedisURL = getEnvOrDefault("REDIS_URL", "")
	if cfg.RedisURL == "" {
		return nil, fmt.Errorf("REDIS_URL is required")
	}

	cfg.IPFSApi = getEnvOrDefault("IPFS_API", "")
	if cfg.IPFSApi == "" {
		return nil, fmt.Errorf("IPFS_API is required")
	}

	cfg.IPFSCluster = getEnvOrDefault("IPFS_CLUSTER_API", "")
	cfg.IOTANodeURL = getEnvOrDefault("IOTA_NODE_URL", "")
	cfg.FFMPEGPath = getEnvOrDefault("FFMPEG_PATH", "ffmpeg")

	cfg.JWTSecret = getEnvOrDefault("JWT_SECRET", "")
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}

	cfg.EnableIOTA = getBoolEnv("ENABLE_IOTA", false)
	cfg.EnableIPFS = getBoolEnv("ENABLE_IPFS_CLUSTER", true)
	cfg.EnableS3 = getBoolEnv("ENABLE_S3", false)

	// Storage Configuration
	cfg.StorageDir = getEnvOrDefault("STORAGE_DIR", "./storage")

	cfg.S3Endpoint = getEnvOrDefault("S3_ENDPOINT", "")
	cfg.S3Bucket = getEnvOrDefault("S3_BUCKET", "")
	cfg.S3AccessKey = getEnvOrDefault("S3_ACCESS_KEY", "")
	cfg.S3SecretKey = getEnvOrDefault("S3_SECRET_KEY", "")
	cfg.S3Region = getEnvOrDefault("S3_REGION", "us-east-1")

	// Upload Configuration
	cfg.MaxUploadSize = getInt64Env("MAX_UPLOAD_SIZE", 5*1024*1024*1024) // 5GB
	cfg.ChunkSize = getInt64Env("CHUNK_SIZE", 32*1024*1024)              // 32MB
	cfg.MaxConcurrentUploads = getIntEnv("MAX_CONCURRENT_UPLOADS", 10)

	// Processing Configuration
	cfg.MaxProcessingWorkers = getIntEnv("MAX_PROCESSING_WORKERS", 4)
	cfg.ProcessingTimeout = getIntEnv("PROCESSING_TIMEOUT", 3600) // 1 hour

	// Rate Limiting Configuration
	cfg.RateLimitRequests = getIntEnv("RATE_LIMIT_REQUESTS", 100)
	cfg.RateLimitWindow = getIntEnv("RATE_LIMIT_WINDOW", 60) // 1 minute

	// CORS Configuration
	cfg.CORSAllowedOrigins = getEnvOrDefault("CORS_ALLOWED_ORIGINS", "*")
	cfg.CORSAllowedMethods = getEnvOrDefault("CORS_ALLOWED_METHODS", "GET,POST,PUT,DELETE,OPTIONS,PATCH")
	cfg.CORSAllowedHeaders = getEnvOrDefault("CORS_ALLOWED_HEADERS", "Accept,Authorization,Content-Type,X-CSRF-Token,X-Requested-With,Idempotency-Key")

	// Logging Configuration
	cfg.LogLevel = getEnvOrDefault("LOG_LEVEL", "info")
	cfg.LogFormat = getEnvOrDefault("LOG_FORMAT", "json")

	// Health Check Configuration
	cfg.HealthCheckTimeout = getIntEnv("HEALTH_CHECK_TIMEOUT", 30)
	cfg.DBPingTimeout = getIntEnv("DB_PING_TIMEOUT", 5)
	cfg.RedisPingTimeout = getIntEnv("REDIS_PING_TIMEOUT", 3)
	cfg.IPFSPingTimeout = getIntEnv("IPFS_PING_TIMEOUT", 10)
	cfg.RequireIPFS = getBoolEnv("REQUIRE_IPFS", true)

	// Video Processing Configuration
	cfg.VideoQualities = getStringSliceEnv("VIDEO_QUALITIES", []string{"360p", "480p", "720p", "1080p"})
	cfg.HLSSegmentDuration = getIntEnv("HLS_SEGMENT_DURATION", 4)
	cfg.ThumbnailCount = getIntEnv("THUMBNAIL_COUNT", 3)

	// HLS Signing
	cfg.HLSSecret = getEnvOrDefault("HLS_SIGNING_SECRET", "")
	cfg.HLSTokenTTL = getIntEnv("HLS_TOKEN_TTL", 600)

	// Storage Tiers Configuration
	cfg.HotStorageLimit = getEnvOrDefault("HOT_STORAGE_LIMIT", "100GB")
	cfg.WarmStorageLimit = getEnvOrDefault("WARM_STORAGE_LIMIT", "1TB")
	cfg.ColdStorageEnabled = getBoolEnv("COLD_STORAGE_ENABLED", true)

	// Pinning Strategy Configuration
	cfg.PinningReplicationFactor = getIntEnv("PINNING_REPLICATION_FACTOR", 3)
	cfg.PinningScoreThreshold = getFloat64Env("PINNING_SCORE_THRESHOLD", 0.3)
	cfg.PinningBackupEnabled = getBoolEnv("PINNING_BACKUP_ENABLED", true)

	// Session Configuration
	cfg.SessionTimeout = getIntEnv("SESSION_TIMEOUT", 24*60*60)              // 24 hours
	cfg.RefreshTokenTimeout = getIntEnv("REFRESH_TOKEN_TIMEOUT", 7*24*60*60) // 7 days

	// API Configuration
	cfg.APITimeout = getIntEnv("API_TIMEOUT", 60)
	cfg.APIMaxRequestSize = getEnvOrDefault("API_MAX_REQUEST_SIZE", "10MB")
	cfg.APIPaginationDefaultLimit = getIntEnv("API_PAGINATION_DEFAULT_LIMIT", 20)
	cfg.APIPaginationMaxLimit = getIntEnv("API_PAGINATION_MAX_LIMIT", 100)

	// Validation Configuration
	cfg.ValidationStrictMode = getBoolEnv("VALIDATION_STRICT_MODE", false) // Default to permissive for backward compatibility
	cfg.ValidationAllowedAlgorithms = getStringSliceEnv("VALIDATION_ALLOWED_ALGORITHMS", []string{"sha256"})
	cfg.ValidationTestMode = getBoolEnv("VALIDATION_TEST_MODE", false)
	cfg.ValidationEnableIntegrityJobs = getBoolEnv("VALIDATION_ENABLE_INTEGRITY_JOBS", true)
	cfg.ValidationLogEvents = getBoolEnv("VALIDATION_LOG_EVENTS", true)

	// Encoding Scheduler (API-driven background processing)
	// Defaults: enabled with 5s interval and burst=3 to ensure progress
	// even without the standalone encoder worker.
	cfg.EnableEncodingScheduler = getBoolEnv("ENABLE_ENCODING_SCHEDULER", true)
	cfg.EncodingSchedulerIntervalSeconds = getIntEnv("ENCODING_SCHEDULER_INTERVAL_SECONDS", 5)
	cfg.EncodingSchedulerBurst = getIntEnv("ENCODING_SCHEDULER_BURST", 3)

	// Image/Avatar Encoding
	cfg.WebPQuality = getIntEnv("WEBP_QUALITY", 0)

	// Encoding Worker Configuration
	cfg.EnableEncoding = getBoolEnv("ENABLE_ENCODING", false)
	cfg.EncodingWorkers = getIntEnv("ENCODING_WORKERS", 2)
	cfg.MetricsAddr = getEnvOrDefault("METRICS_ADDR", ":9090")

	// ATProto Integration
	cfg.EnableATProto = getBoolEnv("ENABLE_ATPROTO", false)
	cfg.ATProtoPDSURL = getEnvOrDefault("ATPROTO_PDS_URL", "")
	cfg.ATProtoAuthToken = getEnvOrDefault("ATPROTO_AUTH_TOKEN", "")
	cfg.ATProtoHandle = getEnvOrDefault("ATPROTO_HANDLE", "")
	cfg.ATProtoAppPassword = getEnvOrDefault("ATPROTO_APP_PASSWORD", "")
	cfg.ATProtoTokenKey = getEnvOrDefault("ATPROTO_TOKEN_KEY", "")
	cfg.ATProtoRefreshIntervalSeconds = getIntEnv("ATPROTO_REFRESH_INTERVAL_SECONDS", 2700) // 45 minutes
	cfg.ATProtoUseImageEmbed = getBoolEnv("ATPROTO_USE_IMAGE_EMBED", false)
	cfg.ATProtoImageAltField = getEnvOrDefault("ATPROTO_IMAGE_ALT_FIELD", "description") // or "title"

	// Public URL
	cfg.PublicBaseURL = getEnvOrDefault("PUBLIC_BASE_URL", "")

	// Federation Scheduler
	cfg.EnableFederationScheduler = getBoolEnv("ENABLE_FEDERATION_SCHEDULER", true)
	cfg.FederationSchedulerIntervalSeconds = getIntEnv("FEDERATION_SCHEDULER_INTERVAL_SECONDS", 15)
	cfg.FederationSchedulerBurst = getIntEnv("FEDERATION_SCHEDULER_BURST", 1)
	cfg.FederationIngestIntervalSeconds = getIntEnv("FEDERATION_INGEST_INTERVAL_SECONDS", 60)
	cfg.FederationIngestMaxItems = getIntEnv("FEDERATION_INGEST_MAX_ITEMS", 40)
	cfg.FederationIngestMaxPages = getIntEnv("FEDERATION_INGEST_MAX_PAGES", 2)

	return cfg, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getBoolEnv(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return strings.ToLower(value) == "true" || value == "1"
}

func getIntEnv(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed
	}
	return defaultValue
}

func getInt64Env(key string, defaultValue int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		return parsed
	}
	return defaultValue
}

func getFloat64Env(key string, defaultValue float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	if parsed, err := strconv.ParseFloat(value, 64); err == nil {
		return parsed
	}
	return defaultValue
}

func getStringSliceEnv(key string, defaultValue []string) []string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return strings.Split(value, ",")
}
