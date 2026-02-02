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

	// IPFS Cluster Security
	IPFSClusterSecret     string
	IPFSClusterClientCert string
	IPFSClusterClientKey  string
	IPFSClusterCACert     string

	// IPFS Streaming Configuration
	EnableIPFSStreaming            bool
	IPFSGatewayURLs                []string
	IPFSStreamingTimeout           time.Duration
	IPFSStreamingPreferLocal       bool
	IPFSGatewayHealthCheckInterval time.Duration
	IPFSStreamingMaxRetries        int
	IPFSStreamingFallbackToLocal   bool
	IPFSStreamingBufferSize        int

	// IOTA Configuration
	IOTANodeURL             string
	IOTAWalletEncryptionKey string

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

	// Metrics Configuration
	EnableMetrics bool
	MetricsAddr   string

	// Caption Generation Configuration
	EnableCaptionGeneration  bool   // Enable automatic caption generation after encoding
	WhisperProvider          string // 'local' or 'openai-api'
	WhisperModelSize         string // 'tiny', 'base', 'small', 'medium', 'large'
	WhisperCppPath           string // Path to whisper.cpp binary (for local provider)
	WhisperAPIURL            string // URL for HTTP Whisper service (for local provider with HTTP API)
	WhisperModelsDir         string // Directory containing Whisper models (for local provider)
	WhisperOpenAIAPIKey      string // OpenAI API key (for openai-api provider)
	WhisperTempDir           string // Temporary directory for audio extraction
	CaptionGenerationWorkers int    // Number of concurrent caption generation workers
	AutoCaptionFormat        string // Default caption format: 'vtt' or 'srt'
	AutoCaptionLanguage      string // Default language hint (empty = auto-detect)

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
	ActivityPubKeyEncryptionKey      string // Master key for encrypting ActivityPub private keys at rest

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

	// Torrent/WebTorrent Configuration
	EnableTorrents             bool
	TorrentListenPort          int
	TorrentMaxConnections      int
	TorrentUploadRateLimit     int64 // bytes per second, 0 = unlimited
	TorrentDownloadRateLimit   int64 // bytes per second, 0 = unlimited
	TorrentSeedRatio           float64
	TorrentDataDir             string
	TorrentCacheSize           int64
	TorrentTrackerURL          string
	TorrentWebSocketTrackerURL string

	// DHT Configuration
	EnableDHT           bool
	DHTBootstrapNodes   []string
	DHTAnnounceInterval time.Duration
	DHTMaxPeers         int

	// Peer Exchange (PEX) Configuration
	EnablePEX bool

	// WebTorrent Configuration
	EnableWebTorrent      bool
	WebTorrentTrackerPort int

	// Smart Seeding Configuration
	SmartSeedingEnabled         bool
	SmartSeedingMinSeeders      int
	SmartSeedingMaxTorrents     int
	SmartSeedingPrioritizeViews bool

	// Hybrid IPFS+Torrent Configuration
	HybridDistributionEnabled bool
	HybridPreferIPFS          bool
	HybridFallbackTimeout     time.Duration

	// Virus Scanning Configuration
	VirusScanEnabled         bool
	ClamAVAddress            string
	VirusScanTimeout         int // seconds
	QuarantineDir            string
	VirusScanFallbackOnError bool
	VirusScanMaxRetries      int
	VirusScanRetryDelay      int // seconds

	// File Type Blocking Configuration
	FileTypeBlockingEnabled bool
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

	// Load RequireIPFS flag first to determine if IPFS_API is required
	cfg.RequireIPFS = getBoolEnv("REQUIRE_IPFS", true)

	cfg.IPFSApi = getEnvOrDefault("IPFS_API", "")
	if cfg.RequireIPFS && cfg.IPFSApi == "" {
		return nil, fmt.Errorf("IPFS_API is required when REQUIRE_IPFS=true")
	}

	cfg.IPFSCluster = getEnvOrDefault("IPFS_CLUSTER_API", "")

	// IPFS Cluster Security Configuration
	cfg.IPFSClusterSecret = getEnvOrDefault("IPFS_CLUSTER_SECRET", "")
	cfg.IPFSClusterClientCert = getEnvOrDefault("IPFS_CLUSTER_CLIENT_CERT", "")
	cfg.IPFSClusterClientKey = getEnvOrDefault("IPFS_CLUSTER_CLIENT_KEY", "")
	cfg.IPFSClusterCACert = getEnvOrDefault("IPFS_CLUSTER_CA_CERT", "")

	// IPFS Streaming Configuration
	cfg.EnableIPFSStreaming = getBoolEnv("ENABLE_IPFS_STREAMING", false)
	defaultGateways := []string{
		"https://ipfs.io",
		"https://dweb.link",
		"https://cloudflare-ipfs.com",
	}
	cfg.IPFSGatewayURLs = getStringSliceEnv("IPFS_GATEWAY_URLS", defaultGateways)
	cfg.IPFSStreamingTimeout = time.Duration(getIntEnv("IPFS_STREAMING_TIMEOUT", 30)) * time.Second
	cfg.IPFSStreamingPreferLocal = getBoolEnv("IPFS_STREAMING_PREFER_LOCAL", true)
	cfg.IPFSGatewayHealthCheckInterval = time.Duration(getIntEnv("IPFS_GATEWAY_HEALTH_CHECK_INTERVAL", 60)) * time.Second
	cfg.IPFSStreamingMaxRetries = getIntEnv("IPFS_STREAMING_MAX_RETRIES", 3)
	cfg.IPFSStreamingFallbackToLocal = getBoolEnv("IPFS_STREAMING_FALLBACK_TO_LOCAL", true)
	cfg.IPFSStreamingBufferSize = getIntEnv("IPFS_STREAMING_BUFFER_SIZE", 32768)
	cfg.IOTANodeURL = getEnvOrDefault("IOTA_NODE_URL", "")
	cfg.IOTAWalletEncryptionKey = getEnvOrDefault("IOTA_WALLET_ENCRYPTION_KEY", "")
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
	// cfg.RequireIPFS is now loaded earlier (line 321) to determine if IPFS_API is required

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

	// Metrics Configuration
	cfg.EnableMetrics = getBoolEnv("ENABLE_METRICS", false)
	cfg.MetricsAddr = getEnvOrDefault("METRICS_ADDR", ":9090")

	// Caption Generation Configuration
	cfg.EnableCaptionGeneration = getBoolEnv("ENABLE_CAPTION_GENERATION", false)
	cfg.WhisperProvider = getEnvOrDefault("WHISPER_PROVIDER", "local")
	cfg.WhisperModelSize = getEnvOrDefault("WHISPER_MODEL_SIZE", "base")
	cfg.WhisperCppPath = getEnvOrDefault("WHISPER_CPP_PATH", "/usr/local/bin/whisper")
	cfg.WhisperAPIURL = getEnvOrDefault("WHISPER_API_URL", "") // HTTP Whisper service URL
	cfg.WhisperModelsDir = getEnvOrDefault("WHISPER_MODELS_DIR", "/var/lib/whisper/models")
	cfg.WhisperOpenAIAPIKey = getEnvOrDefault("WHISPER_OPENAI_API_KEY", "")
	cfg.WhisperTempDir = getEnvOrDefault("WHISPER_TEMP_DIR", "/tmp/whisper")
	cfg.CaptionGenerationWorkers = getIntEnv("CAPTION_GENERATION_WORKERS", 2)
	cfg.AutoCaptionFormat = getEnvOrDefault("AUTO_CAPTION_FORMAT", "vtt")
	cfg.AutoCaptionLanguage = getEnvOrDefault("AUTO_CAPTION_LANGUAGE", "") // empty = auto-detect

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
	cfg.ActivityPubKeyEncryptionKey = getEnvOrDefault("ACTIVITYPUB_KEY_ENCRYPTION_KEY", "")
	if cfg.EnableActivityPub && cfg.ActivityPubKeyEncryptionKey == "" {
		return nil, fmt.Errorf("ACTIVITYPUB_KEY_ENCRYPTION_KEY is required when ActivityPub is enabled")
	}

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

	// Torrent/WebTorrent Configuration
	cfg.EnableTorrents = getBoolEnv("ENABLE_TORRENTS", true)
	cfg.TorrentListenPort = getIntEnv("TORRENT_LISTEN_PORT", 6881)
	cfg.TorrentMaxConnections = getIntEnv("TORRENT_MAX_CONNECTIONS", 200)
	cfg.TorrentUploadRateLimit = getInt64Env("TORRENT_UPLOAD_RATE_LIMIT", 0)     // 0 = unlimited
	cfg.TorrentDownloadRateLimit = getInt64Env("TORRENT_DOWNLOAD_RATE_LIMIT", 0) // 0 = unlimited
	cfg.TorrentSeedRatio = getFloat64Env("TORRENT_SEED_RATIO", 2.0)
	cfg.TorrentDataDir = getEnvOrDefault("TORRENT_DATA_DIR", "./storage/torrents")
	cfg.TorrentCacheSize = getInt64Env("TORRENT_CACHE_SIZE", 64*1024*1024) // 64MB
	cfg.TorrentTrackerURL = getEnvOrDefault("TORRENT_TRACKER_URL", "")
	cfg.TorrentWebSocketTrackerURL = getEnvOrDefault("TORRENT_WEBSOCKET_TRACKER_URL", "")

	// DHT Configuration
	cfg.EnableDHT = getBoolEnv("ENABLE_DHT", true)
	cfg.DHTBootstrapNodes = getStringSliceEnv("DHT_BOOTSTRAP_NODES", []string{
		"router.bittorrent.com:6881",
		"dht.transmissionbt.com:6881",
		"router.utorrent.com:6881",
		"dht.aelitis.com:6881",
	})
	cfg.DHTAnnounceInterval = time.Duration(getIntEnv("DHT_ANNOUNCE_INTERVAL", 1800)) * time.Second // 30 minutes
	cfg.DHTMaxPeers = getIntEnv("DHT_MAX_PEERS", 500)

	// Peer Exchange (PEX) Configuration
	cfg.EnablePEX = getBoolEnv("ENABLE_PEX", true)

	// WebTorrent Configuration
	cfg.EnableWebTorrent = getBoolEnv("ENABLE_WEBTORRENT", true)
	cfg.WebTorrentTrackerPort = getIntEnv("WEBTORRENT_TRACKER_PORT", 8000)

	// Smart Seeding Configuration
	cfg.SmartSeedingEnabled = getBoolEnv("SMART_SEEDING_ENABLED", true)
	cfg.SmartSeedingMinSeeders = getIntEnv("SMART_SEEDING_MIN_SEEDERS", 3)
	cfg.SmartSeedingMaxTorrents = getIntEnv("SMART_SEEDING_MAX_TORRENTS", 100)
	cfg.SmartSeedingPrioritizeViews = getBoolEnv("SMART_SEEDING_PRIORITIZE_VIEWS", true)

	// Hybrid IPFS+Torrent Configuration
	cfg.HybridDistributionEnabled = getBoolEnv("HYBRID_DISTRIBUTION_ENABLED", true)
	cfg.HybridPreferIPFS = getBoolEnv("HYBRID_PREFER_IPFS", false) // Prefer torrent by default
	cfg.HybridFallbackTimeout = time.Duration(getIntEnv("HYBRID_FALLBACK_TIMEOUT", 10)) * time.Second

	// Virus Scanning Configuration
	cfg.VirusScanEnabled = getBoolEnv("VIRUS_SCAN_ENABLED", true)
	cfg.ClamAVAddress = getEnvOrDefault("CLAMAV_ADDRESS", "localhost:3310")
	cfg.VirusScanTimeout = getIntEnv("VIRUS_SCAN_TIMEOUT", 300) // 5 minutes
	cfg.QuarantineDir = getEnvOrDefault("QUARANTINE_DIR", "./quarantine")
	cfg.VirusScanFallbackOnError = getBoolEnv("VIRUS_SCAN_FALLBACK", false)
	cfg.VirusScanMaxRetries = getIntEnv("CLAMAV_MAX_RETRIES", 3)
	cfg.VirusScanRetryDelay = getIntEnv("CLAMAV_RETRY_DELAY", 1)

	// File Type Blocking Configuration
	cfg.FileTypeBlockingEnabled = getBoolEnv("FILE_TYPE_BLOCKING_ENABLED", true)

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
