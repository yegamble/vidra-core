package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

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
	RateLimitDuration time.Duration

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

	// Multi-Codec Configuration
	VideoCodecs   []string // e.g., ["h264", "vp9", "av1"]
	EnableVP9     bool
	VP9Quality    int // CRF value 23-40
	VP9Speed      int // 0-4 (0=slowest/best, 4=fastest)
	EnableAV1     bool
	AV1Preset     int    // 0-13 (for SVT-AV1)
	AV1CRF        int    // 23-55
	CodecPriority string // "quality" or "speed"

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

	// ATProto Firehose (polling) — near real-time ingestion using author feeds
	EnableATProtoFirehose              bool
	ATProtoFirehosePollIntervalSeconds int

	// ActivityPub Configuration
	EnableActivityPub                bool
	ActivityPubDomain                string
	ActivityPubDeliveryWorkers       int
	ActivityPubDeliveryRetries       int
	ActivityPubDeliveryRetryDelay    int // seconds
	ActivityPubAcceptFollowAutomatic bool
	ActivityPubInstanceDescription   string
	ActivityPubInstanceContactEmail  string
	ActivityPubMaxActivitiesPerPage  int

	// Live Streaming (RTMP) Configuration
	EnableLiveStreaming bool
	RTMPHost            string
	RTMPPort            int
	RTMPMaxConnections  int
	RTMPChunkSize       int
	RTMPReadTimeout     time.Duration
	RTMPWriteTimeout    time.Duration
	MaxStreamDuration   time.Duration

	// HLS Transcoding Configuration
	HLSOutputDir         string
	LiveHLSSegmentLength int           // seconds
	LiveHLSWindowSize    int           // number of segments
	HLSCleanupInterval   time.Duration // cleanup interval
	HLSVariants          string        // comma-separated: "1080p,720p,480p,360p"

	// FFmpeg Configuration
	FFmpegPath              string
	FFmpegPreset            string // encoding preset (veryfast, fast, medium, etc.)
	FFmpegTune              string // tuning (zerolatency, film, animation, etc.)
	MaxConcurrentTranscodes int    // max simultaneous transcodes

	// VOD Replay Configuration
	EnableReplayConversion bool
	ReplayStorageDir       string
	ReplayUploadToIPFS     bool
	ReplayRetentionDays    int // 0 = forever
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
	cfg.RateLimitDuration = time.Duration(cfg.RateLimitWindow) * time.Second

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

	// Multi-Codec Configuration
	cfg.VideoCodecs = getStringSliceEnv("VIDEO_CODECS", []string{"h264"}) // Default to H.264 only
	cfg.EnableVP9 = getBoolEnv("ENABLE_VP9", false)
	cfg.VP9Quality = getIntEnv("VP9_QUALITY", 31) // CRF 31 is a good balance
	cfg.VP9Speed = getIntEnv("VP9_SPEED", 2)      // Speed 2 is reasonable
	cfg.EnableAV1 = getBoolEnv("ENABLE_AV1", false)
	cfg.AV1Preset = getIntEnv("AV1_PRESET", 6)                     // Preset 6-8 for balance
	cfg.AV1CRF = getIntEnv("AV1_CRF", 30)                          // CRF 30 for AV1
	cfg.CodecPriority = getEnvOrDefault("CODEC_PRIORITY", "speed") // "quality" or "speed"

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

	// ATProto Firehose (polling)
	cfg.EnableATProtoFirehose = getBoolEnv("ENABLE_ATPROTO_FIREHOSE", false)
	cfg.ATProtoFirehosePollIntervalSeconds = getIntEnv("ATPROTO_FIREHOSE_POLL_INTERVAL_SECONDS", 5)

	// ActivityPub Configuration
	cfg.EnableActivityPub = getBoolEnv("ENABLE_ACTIVITYPUB", false)
	cfg.ActivityPubDomain = getEnvOrDefault("ACTIVITYPUB_DOMAIN", "")
	cfg.ActivityPubDeliveryWorkers = getIntEnv("ACTIVITYPUB_DELIVERY_WORKERS", 5)
	cfg.ActivityPubDeliveryRetries = getIntEnv("ACTIVITYPUB_DELIVERY_RETRIES", 10)
	cfg.ActivityPubDeliveryRetryDelay = getIntEnv("ACTIVITYPUB_DELIVERY_RETRY_DELAY", 60)
	cfg.ActivityPubAcceptFollowAutomatic = getBoolEnv("ACTIVITYPUB_ACCEPT_FOLLOW_AUTOMATIC", true)
	cfg.ActivityPubInstanceDescription = getEnvOrDefault("ACTIVITYPUB_INSTANCE_DESCRIPTION", "A PeerTube-compatible video platform")
	cfg.ActivityPubInstanceContactEmail = getEnvOrDefault("ACTIVITYPUB_INSTANCE_CONTACT_EMAIL", "")
	cfg.ActivityPubMaxActivitiesPerPage = getIntEnv("ACTIVITYPUB_MAX_ACTIVITIES_PER_PAGE", 20)

	// Live Streaming (RTMP) Configuration
	cfg.EnableLiveStreaming = getBoolEnv("ENABLE_LIVE_STREAMING", false)
	cfg.RTMPHost = getEnvOrDefault("RTMP_HOST", "0.0.0.0")
	cfg.RTMPPort = getIntEnv("RTMP_PORT", 1935)
	cfg.RTMPMaxConnections = getIntEnv("RTMP_MAX_CONNECTIONS", 100)
	cfg.RTMPChunkSize = getIntEnv("RTMP_CHUNK_SIZE", 4096)
	cfg.RTMPReadTimeout = time.Duration(getIntEnv("RTMP_READ_TIMEOUT", 30)) * time.Second
	cfg.RTMPWriteTimeout = time.Duration(getIntEnv("RTMP_WRITE_TIMEOUT", 30)) * time.Second
	cfg.MaxStreamDuration = time.Duration(getIntEnv("MAX_STREAM_DURATION", 0)) * time.Second // 0 = unlimited

	// HLS Transcoding Configuration
	cfg.HLSOutputDir = getEnvOrDefault("HLS_OUTPUT_DIR", "./storage/live")
	cfg.LiveHLSSegmentLength = getIntEnv("LIVE_HLS_SEGMENT_LENGTH", 2)
	cfg.LiveHLSWindowSize = getIntEnv("LIVE_HLS_WINDOW_SIZE", 10)
	cfg.HLSCleanupInterval = time.Duration(getIntEnv("HLS_CLEANUP_INTERVAL", 10)) * time.Second
	cfg.HLSVariants = getEnvOrDefault("HLS_VARIANTS", "1080p,720p,480p,360p")

	// FFmpeg Configuration
	cfg.FFmpegPath = getEnvOrDefault("FFMPEG_PATH", "ffmpeg")
	cfg.FFmpegPreset = getEnvOrDefault("FFMPEG_PRESET", "veryfast")
	cfg.FFmpegTune = getEnvOrDefault("FFMPEG_TUNE", "zerolatency")
	cfg.MaxConcurrentTranscodes = getIntEnv("MAX_CONCURRENT_TRANSCODES", 10)

	// VOD Replay Configuration
	cfg.EnableReplayConversion = getBoolEnv("ENABLE_REPLAY_CONVERSION", true)
	cfg.ReplayStorageDir = getEnvOrDefault("REPLAY_STORAGE_DIR", "./storage/replays")
	cfg.ReplayUploadToIPFS = getBoolEnv("REPLAY_UPLOAD_TO_IPFS", true)
	cfg.ReplayRetentionDays = getIntEnv("REPLAY_RETENTION_DAYS", 30)

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
