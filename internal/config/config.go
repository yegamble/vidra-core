package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port int

	DatabaseURL string

	RedisURL string

	IPFSApi     string
	IPFSCluster string

	IPFSClusterSecret     string
	IPFSClusterClientCert string
	IPFSClusterClientKey  string
	IPFSClusterCACert     string

	EnableIPFSStreaming            bool
	IPFSGatewayURLs                []string
	IPFSStreamingTimeout           time.Duration
	IPFSStreamingPreferLocal       bool
	IPFSGatewayHealthCheckInterval time.Duration
	IPFSStreamingMaxRetries        int
	IPFSStreamingFallbackToLocal   bool
	IPFSStreamingBufferSize        int

	IOTANodeURL             string
	IOTAWalletEncryptionKey string

	FFMPEGPath string

	JWTSecret string

	EnableIOTA bool
	EnableIPFS bool
	EnableS3   bool

	StorageDir string

	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3Region    string

	MaxUploadSize        int64
	ChunkSize            int64
	MaxConcurrentUploads int

	MaxProcessingWorkers int
	ProcessingTimeout    int

	RateLimitRequests int
	RateLimitWindow   int
	RateLimitDuration time.Duration

	CORSAllowedOrigins string
	CORSAllowedMethods string
	CORSAllowedHeaders string

	LogLevel  string
	LogFormat string

	HealthCheckTimeout int
	DBPingTimeout      int
	RedisPingTimeout   int
	IPFSPingTimeout    int
	RequireIPFS        bool

	VideoQualities     []string
	HLSSegmentDuration int
	ThumbnailCount     int

	VideoCodecs   []string
	EnableVP9     bool
	VP9Quality    int
	VP9Speed      int
	EnableAV1     bool
	AV1Preset     int
	AV1CRF        int
	CodecPriority string

	HLSSecret   string
	HLSTokenTTL int

	HotStorageLimit    string
	WarmStorageLimit   string
	ColdStorageEnabled bool

	PinningReplicationFactor int
	PinningScoreThreshold    float64
	PinningBackupEnabled     bool

	SessionTimeout      int
	RefreshTokenTimeout int

	APITimeout                int
	APIMaxRequestSize         string
	APIPaginationDefaultLimit int
	APIPaginationMaxLimit     int

	ValidationStrictMode          bool
	ValidationAllowedAlgorithms   []string
	ValidationTestMode            bool
	ValidationEnableIntegrityJobs bool
	ValidationLogEvents           bool

	EnableEncodingScheduler          bool
	EncodingSchedulerIntervalSeconds int
	EncodingSchedulerBurst           int

	WebPQuality int

	EnableEncoding  bool
	EncodingWorkers int
	MetricsAddr     string

	EnableCaptionGeneration  bool
	WhisperProvider          string
	WhisperModelSize         string
	WhisperCppPath           string
	WhisperAPIURL            string
	WhisperModelsDir         string
	WhisperOpenAIAPIKey      string
	WhisperTempDir           string
	CaptionGenerationWorkers int
	AutoCaptionFormat        string
	AutoCaptionLanguage      string

	EnableATProto                 bool
	ATProtoPDSURL                 string
	ATProtoAuthToken              string
	ATProtoHandle                 string
	ATProtoAppPassword            string
	ATProtoTokenKey               string
	ATProtoRefreshIntervalSeconds int
	ATProtoUseImageEmbed          bool
	ATProtoImageAltField          string

	PublicBaseURL string

	EnableFederationScheduler          bool
	FederationSchedulerIntervalSeconds int
	FederationSchedulerBurst           int
	FederationIngestIntervalSeconds    int
	FederationIngestMaxItems           int
	FederationIngestMaxPages           int

	EnableATProtoLabeler bool

	EnableATProtoFirehose              bool
	ATProtoFirehosePollIntervalSeconds int

	EnableActivityPub                bool
	ActivityPubDomain                string
	ActivityPubDeliveryWorkers       int
	ActivityPubDeliveryRetries       int
	ActivityPubDeliveryRetryDelay    int
	ActivityPubAcceptFollowAutomatic bool
	ActivityPubInstanceDescription   string
	ActivityPubInstanceContactEmail  string
	ActivityPubMaxActivitiesPerPage  int
	ActivityPubKeyEncryptionKey      string

	BackupS3Bucket    string
	BackupS3Prefix    string
	BackupS3Endpoint  string
	BackupS3AccessKey string
	BackupS3SecretKey string
	BackupS3Region    string

	BackupSFTPHost     string
	BackupSFTPPort     int
	BackupSFTPUser     string
	BackupSFTPPassword string
	BackupSFTPKeyPath  string
	BackupSFTPPath     string
	BackupSFTPHostKey  string

	BackupTarget         string
	BackupEnabled        bool
	BackupSchedule       string
	BackupRetention      int
	BackupIncludeDB      bool
	BackupIncludeRedis   bool
	BackupIncludeStorage bool
	BackupExcludeDirs    []string

	EnableLiveStreaming bool
	RTMPHost            string
	RTMPPort            int
	RTMPMaxConnections  int
	RTMPChunkSize       int
	RTMPReadTimeout     time.Duration
	RTMPWriteTimeout    time.Duration
	MaxStreamDuration   time.Duration

	HLSOutputDir         string
	LiveHLSSegmentLength int
	LiveHLSWindowSize    int
	HLSCleanupInterval   time.Duration
	HLSVariants          string

	FFmpegPath              string
	FFmpegPreset            string
	FFmpegTune              string
	MaxConcurrentTranscodes int

	EnableReplayConversion bool
	ReplayStorageDir       string
	ReplayUploadToIPFS     bool
	ReplayRetentionDays    int

	EnableTorrents             bool
	TorrentListenPort          int
	TorrentMaxConnections      int
	TorrentUploadRateLimit     int64
	TorrentDownloadRateLimit   int64
	TorrentSeedRatio           float64
	TorrentDataDir             string
	TorrentCacheSize           int64
	TorrentTrackerURL          string
	TorrentWebSocketTrackerURL string

	EnableDHT           bool
	DHTBootstrapNodes   []string
	DHTAnnounceInterval time.Duration
	DHTMaxPeers         int

	EnablePEX bool

	EnableWebTorrent      bool
	WebTorrentTrackerPort int

	SmartSeedingEnabled         bool
	SmartSeedingMinSeeders      int
	SmartSeedingMaxTorrents     int
	SmartSeedingPrioritizeViews bool

	HybridDistributionEnabled bool
	HybridPreferIPFS          bool
	HybridFallbackTimeout     time.Duration

	VirusScanEnabled         bool
	ClamAVAddress            string
	VirusScanTimeout         int
	QuarantineDir            string
	VirusScanFallbackOnError bool
	VirusScanMaxRetries      int
	VirusScanRetryDelay      int

	FileTypeBlockingEnabled bool

	SetupMode bool
}

func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		_ = err
	}

	cfg := &Config{}

	cfg.Port = parsePortFromArgs(os.Args[1:], 8080)
	if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			cfg.Port = p
		}
	}

	setupCompleted := getEnvOrDefault("SETUP_COMPLETED", "")
	setupCompleted = strings.ToLower(strings.TrimSpace(setupCompleted))
	isSetupCompleted := setupCompleted == "true" || setupCompleted == "1"
	setupExplicitlyDisabled := setupCompleted == "false" || setupCompleted == "0"

	cfg.DatabaseURL = getEnvOrDefault("DATABASE_URL", "")
	cfg.RedisURL = getEnvOrDefault("REDIS_URL", "")
	jwtSecret := getEnvOrDefault("JWT_SECRET", "")

	if setupExplicitlyDisabled || (!isSetupCompleted && (cfg.DatabaseURL == "" || cfg.RedisURL == "" || jwtSecret == "")) {
		cfg.SetupMode = true
		cfg.JWTSecret = jwtSecret
		loadCommonFields(cfg, true)
		return cfg, nil
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.RedisURL == "" {
		return nil, fmt.Errorf("REDIS_URL is required")
	}

	loadCommonFields(cfg, false)

	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	if err := validateJWTSecret(cfg.JWTSecret); err != nil {
		return nil, err
	}
	if cfg.RequireIPFS && cfg.IPFSApi == "" {
		return nil, fmt.Errorf("IPFS_API is required when REQUIRE_IPFS=true")
	}
	if cfg.EnableActivityPub && cfg.ActivityPubKeyEncryptionKey == "" {
		return nil, fmt.Errorf("ACTIVITYPUB_KEY_ENCRYPTION_KEY is required when ActivityPub is enabled")
	}

	return cfg, nil
}
