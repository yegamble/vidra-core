package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	// ATProtoDefaultRefreshInterval is the default interval for refreshing ATProto sessions.
	ATProtoDefaultRefreshInterval = 45 * time.Minute

	// ATProtoSessionFreshnessWindow defines how long a session is considered fresh enough to reuse without check.
	ATProtoSessionFreshnessWindow = 50 * time.Minute

	// ATProtoSessionStoreAssumeAge defines the assumed age of a session when loaded from persistent store.
	ATProtoSessionStoreAssumeAge = 40 * time.Minute

	// ATProtoHTTPTimeout is the timeout for ATProto HTTP client requests.
	ATProtoHTTPTimeout = 5 * time.Second
)

type Config struct {
	Port int

	DatabaseURL string

	RedisURL         string
	RedisTLS         bool
	RedisTLSInsecure bool

	IPFSApi             string
	IPFSCluster         string
	IPFSLocalGatewayURL string

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
	IOTAMode                string
	IOTANetwork             string
	IOTAWalletEncryptionKey string

	FFMPEGPath string

	JWTSecret         string
	PeerTubeJWTSecret string // Optional: PeerTube JWT secret for dual-auth during migration cutover

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
	MaxBatchUploadSize   int
	MaxUserVideoQuota    int64

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

	// Log file output and rotation (matches PeerTube's log.rotation.* config)
	LogDir                string
	LogFilename           string
	AuditLogFilename      string
	LogRotationEnabled    bool
	LogRotationMaxSizeMB  int
	LogRotationMaxFiles   int
	LogRotationMaxAgeDays int

	// Log behavior toggles (matches PeerTube's log.* config)
	LogAnonymizeIP     bool
	LogHTTPRequests    bool
	LogPingRequests    bool
	LogAcceptClientLog bool

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

	EnableEncoding   bool
	EncodingWorkers  int
	KeepOriginalFile bool // PeerTube parity: keep original video file after transcoding (default true)
	MetricsAddr      string

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
	ATProtoAutoSyncEnabled        bool
	ATProtoAutoSyncPublicOnly     bool
	ATProtoMaxRetries             int
	ATProtoRetryBaseDelay         time.Duration

	PublicBaseURL string

	NginxEnabled          bool
	NginxDomain           string
	NginxPort             int
	NginxProtocol         string
	NginxTLSMode          string
	NginxLetsEncryptEmail string

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

	EnableEmail         bool
	SMTPTransport       string
	SMTPSendmailPath    string
	SMTPHost            string
	SMTPPort            int
	SMTPUsername        string
	SMTPPassword        string
	SMTPTLS             bool
	SMTPDisableSTARTTLS bool
	SMTPCAFile          string
	SMTPFromAddress     string
	SMTPFromName        string

	ObjectStorageConfig    ObjectStorageConfig
	CSPConfig              CSPConfig
	StaticFilesPrivateAuth bool

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

	setupCompleted := GetEnvOrDefault("SETUP_COMPLETED", "")
	setupCompleted = strings.ToLower(strings.TrimSpace(setupCompleted))
	isSetupCompleted := setupCompleted == "true" || setupCompleted == "1"
	setupExplicitlyDisabled := setupCompleted == "false" || setupCompleted == "0"

	cfg.DatabaseURL = GetEnvOrDefault("DATABASE_URL", "")
	cfg.RedisURL = GetEnvOrDefault("REDIS_URL", "")
	cfg.RedisTLS = GetEnvOrDefault("REDIS_TLS", "") == "true"
	cfg.RedisTLSInsecure = GetEnvOrDefault("REDIS_TLS_INSECURE", "") == "true"
	jwtSecret := GetEnvOrDefault("JWT_SECRET", "")

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
