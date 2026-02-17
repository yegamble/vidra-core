package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

func prodDefault(setupMode, prodVal, setupVal bool) bool {
	if setupMode {
		return setupVal
	}
	return prodVal
}

func loadCommonFields(cfg *Config, setupMode bool) {
	cfg.RequireIPFS = getBoolEnv("REQUIRE_IPFS", prodDefault(setupMode, true, false))
	cfg.IPFSApi = getEnvOrDefault("IPFS_API", "")
	cfg.IPFSCluster = getEnvOrDefault("IPFS_CLUSTER_API", "")
	cfg.IPFSClusterSecret = getEnvOrDefault("IPFS_CLUSTER_SECRET", "")
	cfg.IPFSClusterClientCert = getEnvOrDefault("IPFS_CLUSTER_CLIENT_CERT", "")
	cfg.IPFSClusterClientKey = getEnvOrDefault("IPFS_CLUSTER_CLIENT_KEY", "")
	cfg.IPFSClusterCACert = getEnvOrDefault("IPFS_CLUSTER_CA_CERT", "")
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

	cfg.EnableIOTA = getBoolEnv("ENABLE_IOTA", false)
	cfg.EnableIPFS = getBoolEnv("ENABLE_IPFS_CLUSTER", prodDefault(setupMode, true, false))
	cfg.EnableS3 = getBoolEnv("ENABLE_S3", false)
	cfg.StorageDir = getEnvOrDefault("STORAGE_DIR", "./storage")

	cfg.S3Endpoint = getEnvOrDefault("S3_ENDPOINT", "")
	cfg.S3Bucket = getEnvOrDefault("S3_BUCKET", "")
	cfg.S3AccessKey = getEnvOrDefault("S3_ACCESS_KEY", "")
	cfg.S3SecretKey = getEnvOrDefault("S3_SECRET_KEY", "")
	cfg.S3Region = getEnvOrDefault("S3_REGION", "us-east-1")

	cfg.BackupS3Bucket = getEnvOrDefault("BACKUP_S3_BUCKET", "")
	cfg.BackupS3Prefix = getEnvOrDefault("BACKUP_S3_PREFIX", "backups/")
	cfg.BackupS3Endpoint = getEnvOrDefault("BACKUP_S3_ENDPOINT", "")
	cfg.BackupS3AccessKey = getEnvOrDefault("BACKUP_S3_ACCESS_KEY", "")
	cfg.BackupS3SecretKey = getEnvOrDefault("BACKUP_S3_SECRET_KEY", "")
	cfg.BackupS3Region = getEnvOrDefault("BACKUP_S3_REGION", "us-east-1")

	cfg.BackupSFTPHost = getEnvOrDefault("BACKUP_SFTP_HOST", "")
	cfg.BackupSFTPPort = getIntEnv("BACKUP_SFTP_PORT", 22)
	cfg.BackupSFTPUser = getEnvOrDefault("BACKUP_SFTP_USER", "")
	cfg.BackupSFTPPassword = getEnvOrDefault("BACKUP_SFTP_PASSWORD", "")
	cfg.BackupSFTPKeyPath = getEnvOrDefault("BACKUP_SFTP_KEY_PATH", "")
	cfg.BackupSFTPPath = getEnvOrDefault("BACKUP_SFTP_PATH", "/backups")
	cfg.BackupSFTPHostKey = getEnvOrDefault("BACKUP_SFTP_HOST_KEY", "")
	cfg.BackupTarget = getEnvOrDefault("BACKUP_TARGET", "local")
	cfg.BackupEnabled = getBoolEnv("BACKUP_ENABLED", false)
	cfg.BackupSchedule = getEnvOrDefault("BACKUP_SCHEDULE", "0 2 * * *")
	cfg.BackupRetention = getIntEnv("BACKUP_RETENTION", 7)
	cfg.BackupIncludeDB = getBoolEnv("BACKUP_INCLUDE_DB", true)
	cfg.BackupIncludeRedis = getBoolEnv("BACKUP_INCLUDE_REDIS", true)
	cfg.BackupIncludeStorage = getBoolEnv("BACKUP_INCLUDE_STORAGE", true)
	excludeDirsStr := getEnvOrDefault("BACKUP_EXCLUDE_DIRS", "")
	if excludeDirsStr != "" {
		cfg.BackupExcludeDirs = strings.Split(excludeDirsStr, ",")
		for i := range cfg.BackupExcludeDirs {
			cfg.BackupExcludeDirs[i] = strings.TrimSpace(cfg.BackupExcludeDirs[i])
		}
	}

	cfg.MaxUploadSize = getInt64Env("MAX_UPLOAD_SIZE", 5*1024*1024*1024)
	cfg.ChunkSize = getInt64Env("CHUNK_SIZE", 32*1024*1024)
	cfg.MaxConcurrentUploads = getIntEnv("MAX_CONCURRENT_UPLOADS", 10)
	cfg.MaxProcessingWorkers = getIntEnv("MAX_PROCESSING_WORKERS", 4)
	cfg.ProcessingTimeout = getIntEnv("PROCESSING_TIMEOUT", 3600)

	cfg.RateLimitRequests = getIntEnv("RATE_LIMIT_REQUESTS", 100)
	cfg.RateLimitWindow = getIntEnv("RATE_LIMIT_WINDOW", 60)
	cfg.RateLimitDuration = time.Duration(cfg.RateLimitWindow) * time.Second

	cfg.CORSAllowedOrigins = getEnvOrDefault("CORS_ALLOWED_ORIGINS", "*")
	cfg.CORSAllowedMethods = getEnvOrDefault("CORS_ALLOWED_METHODS", "GET,POST,PUT,DELETE,OPTIONS,PATCH")
	cfg.CORSAllowedHeaders = getEnvOrDefault("CORS_ALLOWED_HEADERS", "Accept,Authorization,Content-Type,X-CSRF-Token,X-Requested-With,Idempotency-Key")

	cfg.LogLevel = getEnvOrDefault("LOG_LEVEL", "info")
	cfg.LogFormat = getEnvOrDefault("LOG_FORMAT", "json")

	cfg.HealthCheckTimeout = getIntEnv("HEALTH_CHECK_TIMEOUT", 30)
	cfg.DBPingTimeout = getIntEnv("DB_PING_TIMEOUT", 5)
	cfg.RedisPingTimeout = getIntEnv("REDIS_PING_TIMEOUT", 3)
	cfg.IPFSPingTimeout = getIntEnv("IPFS_PING_TIMEOUT", 10)

	cfg.VideoQualities = getStringSliceEnv("VIDEO_QUALITIES", []string{"360p", "480p", "720p", "1080p"})
	cfg.HLSSegmentDuration = getIntEnv("HLS_SEGMENT_DURATION", 4)
	cfg.ThumbnailCount = getIntEnv("THUMBNAIL_COUNT", 3)
	cfg.VideoCodecs = getStringSliceEnv("VIDEO_CODECS", []string{"h264"})
	cfg.EnableVP9 = getBoolEnv("ENABLE_VP9", false)
	cfg.VP9Quality = getIntEnv("VP9_QUALITY", 31)
	cfg.VP9Speed = getIntEnv("VP9_SPEED", 2)
	cfg.EnableAV1 = getBoolEnv("ENABLE_AV1", false)
	cfg.AV1Preset = getIntEnv("AV1_PRESET", 6)
	cfg.AV1CRF = getIntEnv("AV1_CRF", 30)
	cfg.CodecPriority = getEnvOrDefault("CODEC_PRIORITY", "speed")

	cfg.HLSSecret = getEnvOrDefault("HLS_SIGNING_SECRET", "")
	cfg.HLSTokenTTL = getIntEnv("HLS_TOKEN_TTL", 600)

	cfg.HotStorageLimit = getEnvOrDefault("HOT_STORAGE_LIMIT", "100GB")
	cfg.WarmStorageLimit = getEnvOrDefault("WARM_STORAGE_LIMIT", "1TB")
	cfg.ColdStorageEnabled = getBoolEnv("COLD_STORAGE_ENABLED", true)

	cfg.PinningReplicationFactor = getIntEnv("PINNING_REPLICATION_FACTOR", 3)
	cfg.PinningScoreThreshold = getFloat64Env("PINNING_SCORE_THRESHOLD", 0.3)
	cfg.PinningBackupEnabled = getBoolEnv("PINNING_BACKUP_ENABLED", true)

	cfg.SessionTimeout = getIntEnv("SESSION_TIMEOUT", 24*60*60)
	cfg.RefreshTokenTimeout = getIntEnv("REFRESH_TOKEN_TIMEOUT", 7*24*60*60)

	cfg.APITimeout = getIntEnv("API_TIMEOUT", 60)
	cfg.APIMaxRequestSize = getEnvOrDefault("API_MAX_REQUEST_SIZE", "10MB")
	cfg.APIPaginationDefaultLimit = getIntEnv("API_PAGINATION_DEFAULT_LIMIT", 20)
	cfg.APIPaginationMaxLimit = getIntEnv("API_PAGINATION_MAX_LIMIT", 100)

	cfg.ValidationStrictMode = getBoolEnv("VALIDATION_STRICT_MODE", false)
	cfg.ValidationAllowedAlgorithms = getStringSliceEnv("VALIDATION_ALLOWED_ALGORITHMS", []string{"sha256"})
	cfg.ValidationTestMode = getBoolEnv("VALIDATION_TEST_MODE", false)
	cfg.ValidationEnableIntegrityJobs = getBoolEnv("VALIDATION_ENABLE_INTEGRITY_JOBS", true)
	cfg.ValidationLogEvents = getBoolEnv("VALIDATION_LOG_EVENTS", true)

	cfg.EnableEncodingScheduler = getBoolEnv("ENABLE_ENCODING_SCHEDULER", true)
	cfg.EncodingSchedulerIntervalSeconds = getIntEnv("ENCODING_SCHEDULER_INTERVAL_SECONDS", 5)
	cfg.EncodingSchedulerBurst = getIntEnv("ENCODING_SCHEDULER_BURST", 3)
	cfg.WebPQuality = getIntEnv("WEBP_QUALITY", 0)
	cfg.EnableEncoding = getBoolEnv("ENABLE_ENCODING", false)
	cfg.EncodingWorkers = getIntEnv("ENCODING_WORKERS", 2)
	cfg.MetricsAddr = getEnvOrDefault("METRICS_ADDR", ":9090")

	cfg.EnableCaptionGeneration = getBoolEnv("ENABLE_CAPTION_GENERATION", false)
	cfg.WhisperProvider = getEnvOrDefault("WHISPER_PROVIDER", "local")
	cfg.WhisperModelSize = getEnvOrDefault("WHISPER_MODEL_SIZE", "base")
	cfg.WhisperCppPath = getEnvOrDefault("WHISPER_CPP_PATH", "/usr/local/bin/whisper")
	cfg.WhisperAPIURL = getEnvOrDefault("WHISPER_API_URL", "")
	cfg.WhisperModelsDir = getEnvOrDefault("WHISPER_MODELS_DIR", "/var/lib/whisper/models")
	cfg.WhisperOpenAIAPIKey = getEnvOrDefault("WHISPER_OPENAI_API_KEY", "")
	cfg.WhisperTempDir = getEnvOrDefault("WHISPER_TEMP_DIR", filepath.Join(os.TempDir(), "whisper"))
	cfg.CaptionGenerationWorkers = getIntEnv("CAPTION_GENERATION_WORKERS", 2)
	cfg.AutoCaptionFormat = getEnvOrDefault("AUTO_CAPTION_FORMAT", "vtt")
	cfg.AutoCaptionLanguage = getEnvOrDefault("AUTO_CAPTION_LANGUAGE", "")

	cfg.EnableATProto = getBoolEnv("ENABLE_ATPROTO", false)
	cfg.ATProtoPDSURL = getEnvOrDefault("ATPROTO_PDS_URL", "")
	cfg.ATProtoAuthToken = getEnvOrDefault("ATPROTO_AUTH_TOKEN", "")
	cfg.ATProtoHandle = getEnvOrDefault("ATPROTO_HANDLE", "")
	cfg.ATProtoAppPassword = getEnvOrDefault("ATPROTO_APP_PASSWORD", "")
	cfg.ATProtoTokenKey = getEnvOrDefault("ATPROTO_TOKEN_KEY", "")
	cfg.ATProtoRefreshIntervalSeconds = getIntEnv("ATPROTO_REFRESH_INTERVAL_SECONDS", 2700)
	cfg.ATProtoUseImageEmbed = getBoolEnv("ATPROTO_USE_IMAGE_EMBED", false)
	cfg.ATProtoImageAltField = getEnvOrDefault("ATPROTO_IMAGE_ALT_FIELD", "description")

	cfg.NginxEnabled = getBoolEnv("NGINX_ENABLED", false)
	cfg.NginxDomain = getEnvOrDefault("NGINX_DOMAIN", "localhost")
	cfg.NginxPort = getIntEnv("NGINX_PORT", 80)
	cfg.NginxProtocol = getEnvOrDefault("NGINX_PROTOCOL", "http")
	cfg.NginxTLSMode = getEnvOrDefault("NGINX_TLS_MODE", "")
	cfg.NginxLetsEncryptEmail = getEnvOrDefault("NGINX_LETSENCRYPT_EMAIL", "")

	cfg.PublicBaseURL = getEnvOrDefault("PUBLIC_BASE_URL", "")

	cfg.EnableFederationScheduler = getBoolEnv("ENABLE_FEDERATION_SCHEDULER", prodDefault(setupMode, true, false))
	cfg.FederationSchedulerIntervalSeconds = getIntEnv("FEDERATION_SCHEDULER_INTERVAL_SECONDS", 15)
	cfg.FederationSchedulerBurst = getIntEnv("FEDERATION_SCHEDULER_BURST", 1)
	cfg.FederationIngestIntervalSeconds = getIntEnv("FEDERATION_INGEST_INTERVAL_SECONDS", 60)
	cfg.FederationIngestMaxItems = getIntEnv("FEDERATION_INGEST_MAX_ITEMS", 40)
	cfg.FederationIngestMaxPages = getIntEnv("FEDERATION_INGEST_MAX_PAGES", 2)
	cfg.EnableATProtoLabeler = getBoolEnv("ENABLE_ATPROTO_LABELER", false)
	cfg.EnableATProtoFirehose = getBoolEnv("ENABLE_ATPROTO_FIREHOSE", false)
	cfg.ATProtoFirehosePollIntervalSeconds = getIntEnv("ATPROTO_FIREHOSE_POLL_INTERVAL_SECONDS", 5)

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

	cfg.EnableLiveStreaming = getBoolEnv("ENABLE_LIVE_STREAMING", false)
	cfg.RTMPHost = getEnvOrDefault("RTMP_HOST", "0.0.0.0")
	cfg.RTMPPort = getIntEnv("RTMP_PORT", 1935)
	cfg.RTMPMaxConnections = getIntEnv("RTMP_MAX_CONNECTIONS", 100)
	cfg.RTMPChunkSize = getIntEnv("RTMP_CHUNK_SIZE", 4096)
	cfg.RTMPReadTimeout = time.Duration(getIntEnv("RTMP_READ_TIMEOUT", 30)) * time.Second
	cfg.RTMPWriteTimeout = time.Duration(getIntEnv("RTMP_WRITE_TIMEOUT", 30)) * time.Second
	cfg.MaxStreamDuration = time.Duration(getIntEnv("MAX_STREAM_DURATION", 0)) * time.Second
	cfg.HLSOutputDir = getEnvOrDefault("HLS_OUTPUT_DIR", "./storage/live")
	cfg.LiveHLSSegmentLength = getIntEnv("LIVE_HLS_SEGMENT_LENGTH", 2)
	cfg.LiveHLSWindowSize = getIntEnv("LIVE_HLS_WINDOW_SIZE", 10)
	cfg.HLSCleanupInterval = time.Duration(getIntEnv("HLS_CLEANUP_INTERVAL", 10)) * time.Second
	cfg.HLSVariants = getEnvOrDefault("HLS_VARIANTS", "1080p,720p,480p,360p")

	cfg.FFmpegPath = getEnvOrDefault("FFMPEG_PATH", "ffmpeg")
	cfg.FFmpegPreset = getEnvOrDefault("FFMPEG_PRESET", "veryfast")
	cfg.FFmpegTune = getEnvOrDefault("FFMPEG_TUNE", "zerolatency")
	cfg.MaxConcurrentTranscodes = getIntEnv("MAX_CONCURRENT_TRANSCODES", 10)

	cfg.EnableReplayConversion = getBoolEnv("ENABLE_REPLAY_CONVERSION", true)
	cfg.ReplayStorageDir = getEnvOrDefault("REPLAY_STORAGE_DIR", "./storage/replays")
	cfg.ReplayUploadToIPFS = getBoolEnv("REPLAY_UPLOAD_TO_IPFS", prodDefault(setupMode, true, false))
	cfg.ReplayRetentionDays = getIntEnv("REPLAY_RETENTION_DAYS", 30)

	cfg.EnableTorrents = getBoolEnv("ENABLE_TORRENTS", prodDefault(setupMode, true, false))
	cfg.TorrentListenPort = getIntEnv("TORRENT_LISTEN_PORT", 6881)
	cfg.TorrentMaxConnections = getIntEnv("TORRENT_MAX_CONNECTIONS", 200)
	cfg.TorrentUploadRateLimit = getInt64Env("TORRENT_UPLOAD_RATE_LIMIT", 0)
	cfg.TorrentDownloadRateLimit = getInt64Env("TORRENT_DOWNLOAD_RATE_LIMIT", 0)
	cfg.TorrentSeedRatio = getFloat64Env("TORRENT_SEED_RATIO", 2.0)
	cfg.TorrentDataDir = getEnvOrDefault("TORRENT_DATA_DIR", "./storage/torrents")
	cfg.TorrentCacheSize = getInt64Env("TORRENT_CACHE_SIZE", 64*1024*1024)
	cfg.TorrentTrackerURL = getEnvOrDefault("TORRENT_TRACKER_URL", "")
	cfg.TorrentWebSocketTrackerURL = getEnvOrDefault("TORRENT_WEBSOCKET_TRACKER_URL", "")

	cfg.EnableDHT = getBoolEnv("ENABLE_DHT", prodDefault(setupMode, true, false))
	cfg.DHTBootstrapNodes = getStringSliceEnv("DHT_BOOTSTRAP_NODES", []string{
		"router.bittorrent.com:6881",
		"dht.transmissionbt.com:6881",
		"router.utorrent.com:6881",
		"dht.aelitis.com:6881",
	})
	cfg.DHTAnnounceInterval = time.Duration(getIntEnv("DHT_ANNOUNCE_INTERVAL", 1800)) * time.Second
	cfg.DHTMaxPeers = getIntEnv("DHT_MAX_PEERS", 500)
	cfg.EnablePEX = getBoolEnv("ENABLE_PEX", prodDefault(setupMode, true, false))
	cfg.EnableWebTorrent = getBoolEnv("ENABLE_WEBTORRENT", prodDefault(setupMode, true, false))
	cfg.WebTorrentTrackerPort = getIntEnv("WEBTORRENT_TRACKER_PORT", 8000)

	cfg.SmartSeedingEnabled = getBoolEnv("SMART_SEEDING_ENABLED", prodDefault(setupMode, true, false))
	cfg.SmartSeedingMinSeeders = getIntEnv("SMART_SEEDING_MIN_SEEDERS", 3)
	cfg.SmartSeedingMaxTorrents = getIntEnv("SMART_SEEDING_MAX_TORRENTS", 100)
	cfg.SmartSeedingPrioritizeViews = getBoolEnv("SMART_SEEDING_PRIORITIZE_VIEWS", true)

	cfg.HybridDistributionEnabled = getBoolEnv("HYBRID_DISTRIBUTION_ENABLED", prodDefault(setupMode, true, false))
	cfg.HybridPreferIPFS = getBoolEnv("HYBRID_PREFER_IPFS", false)
	cfg.HybridFallbackTimeout = time.Duration(getIntEnv("HYBRID_FALLBACK_TIMEOUT", 10)) * time.Second

	cfg.EnableEmail = getBoolEnv("ENABLE_EMAIL", false)
	cfg.SMTPTransport = getEnvOrDefault("SMTP_TRANSPORT", "smtp")
	cfg.SMTPSendmailPath = getEnvOrDefault("SMTP_SENDMAIL_PATH", "/usr/sbin/sendmail")
	cfg.SMTPHost = getEnvOrDefault("SMTP_HOST", "localhost")
	cfg.SMTPPort = getIntEnv("SMTP_PORT", 1025)
	cfg.SMTPUsername = getEnvOrDefault("SMTP_USERNAME", "")
	cfg.SMTPPassword = getEnvOrDefault("SMTP_PASSWORD", "")
	cfg.SMTPTLS = getBoolEnv("SMTP_TLS", false)
	cfg.SMTPDisableSTARTTLS = getBoolEnv("SMTP_DISABLE_STARTTLS", false)
	cfg.SMTPCAFile = getEnvOrDefault("SMTP_CA_FILE", "")
	cfg.SMTPFromAddress = getEnvOrDefault("SMTP_FROM", "noreply@localhost")
	cfg.SMTPFromName = getEnvOrDefault("SMTP_FROM_NAME", "Athena")

	cfg.VirusScanEnabled = getBoolEnv("VIRUS_SCAN_ENABLED", prodDefault(setupMode, true, false))
	cfg.ClamAVAddress = getEnvOrDefault("CLAMAV_ADDRESS", "localhost:3310")
	cfg.VirusScanTimeout = getIntEnv("VIRUS_SCAN_TIMEOUT", 300)
	cfg.QuarantineDir = getEnvOrDefault("QUARANTINE_DIR", "./quarantine")
	cfg.VirusScanFallbackOnError = getBoolEnv("VIRUS_SCAN_FALLBACK", false)
	cfg.VirusScanMaxRetries = getIntEnv("CLAMAV_MAX_RETRIES", 3)
	cfg.VirusScanRetryDelay = getIntEnv("CLAMAV_RETRY_DELAY", 1)
	cfg.FileTypeBlockingEnabled = getBoolEnv("FILE_TYPE_BLOCKING_ENABLED", prodDefault(setupMode, true, false))

	cfg.ObjectStorageConfig = loadObjectStorageConfig()
	cfg.CSPConfig = loadCSPConfig()
	cfg.StaticFilesPrivateAuth = getBoolEnv("STATIC_FILES_PRIVATE_AUTH", true)
}
