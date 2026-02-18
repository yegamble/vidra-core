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
	cfg.RequireIPFS = GetBoolEnv("REQUIRE_IPFS", prodDefault(setupMode, true, false))
	cfg.IPFSApi = GetEnvOrDefault("IPFS_API", "")
	cfg.IPFSCluster = GetEnvOrDefault("IPFS_CLUSTER_API", "")
	cfg.IPFSClusterSecret = GetEnvOrDefault("IPFS_CLUSTER_SECRET", "")
	cfg.IPFSClusterClientCert = GetEnvOrDefault("IPFS_CLUSTER_CLIENT_CERT", "")
	cfg.IPFSClusterClientKey = GetEnvOrDefault("IPFS_CLUSTER_CLIENT_KEY", "")
	cfg.IPFSClusterCACert = GetEnvOrDefault("IPFS_CLUSTER_CA_CERT", "")
	cfg.EnableIPFSStreaming = GetBoolEnv("ENABLE_IPFS_STREAMING", false)
	defaultGateways := []string{
		"https://ipfs.io",
		"https://dweb.link",
		"https://cloudflare-ipfs.com",
	}
	cfg.IPFSGatewayURLs = GetStringSliceEnv("IPFS_GATEWAY_URLS", defaultGateways)
	cfg.IPFSStreamingTimeout = time.Duration(GetIntEnv("IPFS_STREAMING_TIMEOUT", 30)) * time.Second
	cfg.IPFSStreamingPreferLocal = GetBoolEnv("IPFS_STREAMING_PREFER_LOCAL", true)
	cfg.IPFSGatewayHealthCheckInterval = time.Duration(GetIntEnv("IPFS_GATEWAY_HEALTH_CHECK_INTERVAL", 60)) * time.Second
	cfg.IPFSStreamingMaxRetries = GetIntEnv("IPFS_STREAMING_MAX_RETRIES", 3)
	cfg.IPFSStreamingFallbackToLocal = GetBoolEnv("IPFS_STREAMING_FALLBACK_TO_LOCAL", true)
	cfg.IPFSStreamingBufferSize = GetIntEnv("IPFS_STREAMING_BUFFER_SIZE", 32768)

	cfg.IOTANodeURL = GetEnvOrDefault("IOTA_NODE_URL", "")
	cfg.IOTAMode = GetEnvOrDefault("IOTA_MODE", "docker")
	cfg.IOTANetwork = GetEnvOrDefault("IOTA_NETWORK", "testnet")
	cfg.IOTAWalletEncryptionKey = GetEnvOrDefault("IOTA_WALLET_ENCRYPTION_KEY", "")
	cfg.FFMPEGPath = GetEnvOrDefault("FFMPEG_PATH", "ffmpeg")
	cfg.JWTSecret = GetEnvOrDefault("JWT_SECRET", "")

	cfg.EnableIOTA = GetBoolEnv("ENABLE_IOTA", false)
	cfg.EnableIPFS = GetBoolEnv("ENABLE_IPFS_CLUSTER", prodDefault(setupMode, true, false))
	cfg.EnableS3 = GetBoolEnv("ENABLE_S3", false)
	cfg.StorageDir = GetEnvOrDefault("STORAGE_DIR", "./storage")

	cfg.S3Endpoint = GetEnvOrDefault("S3_ENDPOINT", "")
	cfg.S3Bucket = GetEnvOrDefault("S3_BUCKET", "")
	cfg.S3AccessKey = GetEnvOrDefault("S3_ACCESS_KEY", "")
	cfg.S3SecretKey = GetEnvOrDefault("S3_SECRET_KEY", "")
	cfg.S3Region = GetEnvOrDefault("S3_REGION", "us-east-1")

	cfg.BackupS3Bucket = GetEnvOrDefault("BACKUP_S3_BUCKET", "")
	cfg.BackupS3Prefix = GetEnvOrDefault("BACKUP_S3_PREFIX", "backups/")
	cfg.BackupS3Endpoint = GetEnvOrDefault("BACKUP_S3_ENDPOINT", "")
	cfg.BackupS3AccessKey = GetEnvOrDefault("BACKUP_S3_ACCESS_KEY", "")
	cfg.BackupS3SecretKey = GetEnvOrDefault("BACKUP_S3_SECRET_KEY", "")
	cfg.BackupS3Region = GetEnvOrDefault("BACKUP_S3_REGION", "us-east-1")

	cfg.BackupSFTPHost = GetEnvOrDefault("BACKUP_SFTP_HOST", "")
	cfg.BackupSFTPPort = GetIntEnv("BACKUP_SFTP_PORT", 22)
	cfg.BackupSFTPUser = GetEnvOrDefault("BACKUP_SFTP_USER", "")
	cfg.BackupSFTPPassword = GetEnvOrDefault("BACKUP_SFTP_PASSWORD", "")
	cfg.BackupSFTPKeyPath = GetEnvOrDefault("BACKUP_SFTP_KEY_PATH", "")
	cfg.BackupSFTPPath = GetEnvOrDefault("BACKUP_SFTP_PATH", "/backups")
	cfg.BackupSFTPHostKey = GetEnvOrDefault("BACKUP_SFTP_HOST_KEY", "")
	cfg.BackupTarget = GetEnvOrDefault("BACKUP_TARGET", "local")
	cfg.BackupEnabled = GetBoolEnv("BACKUP_ENABLED", false)
	cfg.BackupSchedule = GetEnvOrDefault("BACKUP_SCHEDULE", "0 2 * * *")
	cfg.BackupRetention = GetIntEnv("BACKUP_RETENTION", 7)
	cfg.BackupIncludeDB = GetBoolEnv("BACKUP_INCLUDE_DB", true)
	cfg.BackupIncludeRedis = GetBoolEnv("BACKUP_INCLUDE_REDIS", true)
	cfg.BackupIncludeStorage = GetBoolEnv("BACKUP_INCLUDE_STORAGE", true)
	excludeDirsStr := GetEnvOrDefault("BACKUP_EXCLUDE_DIRS", "")
	if excludeDirsStr != "" {
		cfg.BackupExcludeDirs = strings.Split(excludeDirsStr, ",")
		for i := range cfg.BackupExcludeDirs {
			cfg.BackupExcludeDirs[i] = strings.TrimSpace(cfg.BackupExcludeDirs[i])
		}
	}

	cfg.MaxUploadSize = GetInt64Env("MAX_UPLOAD_SIZE", 5*1024*1024*1024)
	cfg.ChunkSize = GetInt64Env("CHUNK_SIZE", 32*1024*1024)
	cfg.MaxConcurrentUploads = GetIntEnv("MAX_CONCURRENT_UPLOADS", 10)
	cfg.MaxProcessingWorkers = GetIntEnv("MAX_PROCESSING_WORKERS", 4)
	cfg.ProcessingTimeout = GetIntEnv("PROCESSING_TIMEOUT", 3600)

	cfg.RateLimitRequests = GetIntEnv("RATE_LIMIT_REQUESTS", 100)
	cfg.RateLimitWindow = GetIntEnv("RATE_LIMIT_WINDOW", 60)
	cfg.RateLimitDuration = time.Duration(cfg.RateLimitWindow) * time.Second

	cfg.CORSAllowedOrigins = GetEnvOrDefault("CORS_ALLOWED_ORIGINS", "*")
	cfg.CORSAllowedMethods = GetEnvOrDefault("CORS_ALLOWED_METHODS", "GET,POST,PUT,DELETE,OPTIONS,PATCH")
	cfg.CORSAllowedHeaders = GetEnvOrDefault("CORS_ALLOWED_HEADERS", "Accept,Authorization,Content-Type,X-CSRF-Token,X-Requested-With,Idempotency-Key")

	cfg.LogLevel = GetEnvOrDefault("LOG_LEVEL", "info")
	cfg.LogFormat = GetEnvOrDefault("LOG_FORMAT", "json")

	cfg.HealthCheckTimeout = GetIntEnv("HEALTH_CHECK_TIMEOUT", 30)
	cfg.DBPingTimeout = GetIntEnv("DB_PING_TIMEOUT", 5)
	cfg.RedisPingTimeout = GetIntEnv("REDIS_PING_TIMEOUT", 3)
	cfg.IPFSPingTimeout = GetIntEnv("IPFS_PING_TIMEOUT", 10)

	cfg.VideoQualities = GetStringSliceEnv("VIDEO_QUALITIES", []string{"360p", "480p", "720p", "1080p"})
	cfg.HLSSegmentDuration = GetIntEnv("HLS_SEGMENT_DURATION", 4)
	cfg.ThumbnailCount = GetIntEnv("THUMBNAIL_COUNT", 3)
	cfg.VideoCodecs = GetStringSliceEnv("VIDEO_CODECS", []string{"h264"})
	cfg.EnableVP9 = GetBoolEnv("ENABLE_VP9", false)
	cfg.VP9Quality = GetIntEnv("VP9_QUALITY", 31)
	cfg.VP9Speed = GetIntEnv("VP9_SPEED", 2)
	cfg.EnableAV1 = GetBoolEnv("ENABLE_AV1", false)
	cfg.AV1Preset = GetIntEnv("AV1_PRESET", 6)
	cfg.AV1CRF = GetIntEnv("AV1_CRF", 30)
	cfg.CodecPriority = GetEnvOrDefault("CODEC_PRIORITY", "speed")

	cfg.HLSSecret = GetEnvOrDefault("HLS_SIGNING_SECRET", "")
	cfg.HLSTokenTTL = GetIntEnv("HLS_TOKEN_TTL", 600)

	cfg.HotStorageLimit = GetEnvOrDefault("HOT_STORAGE_LIMIT", "100GB")
	cfg.WarmStorageLimit = GetEnvOrDefault("WARM_STORAGE_LIMIT", "1TB")
	cfg.ColdStorageEnabled = GetBoolEnv("COLD_STORAGE_ENABLED", true)

	cfg.PinningReplicationFactor = GetIntEnv("PINNING_REPLICATION_FACTOR", 3)
	cfg.PinningScoreThreshold = GetFloat64Env("PINNING_SCORE_THRESHOLD", 0.3)
	cfg.PinningBackupEnabled = GetBoolEnv("PINNING_BACKUP_ENABLED", true)

	cfg.SessionTimeout = GetIntEnv("SESSION_TIMEOUT", 24*60*60)
	cfg.RefreshTokenTimeout = GetIntEnv("REFRESH_TOKEN_TIMEOUT", 7*24*60*60)

	cfg.APITimeout = GetIntEnv("API_TIMEOUT", 60)
	cfg.APIMaxRequestSize = GetEnvOrDefault("API_MAX_REQUEST_SIZE", "10MB")
	cfg.APIPaginationDefaultLimit = GetIntEnv("API_PAGINATION_DEFAULT_LIMIT", 20)
	cfg.APIPaginationMaxLimit = GetIntEnv("API_PAGINATION_MAX_LIMIT", 100)

	cfg.ValidationStrictMode = GetBoolEnv("VALIDATION_STRICT_MODE", false)
	cfg.ValidationAllowedAlgorithms = GetStringSliceEnv("VALIDATION_ALLOWED_ALGORITHMS", []string{"sha256"})
	cfg.ValidationTestMode = GetBoolEnv("VALIDATION_TEST_MODE", false)
	cfg.ValidationEnableIntegrityJobs = GetBoolEnv("VALIDATION_ENABLE_INTEGRITY_JOBS", true)
	cfg.ValidationLogEvents = GetBoolEnv("VALIDATION_LOG_EVENTS", true)

	cfg.EnableEncodingScheduler = GetBoolEnv("ENABLE_ENCODING_SCHEDULER", true)
	cfg.EncodingSchedulerIntervalSeconds = GetIntEnv("ENCODING_SCHEDULER_INTERVAL_SECONDS", 5)
	cfg.EncodingSchedulerBurst = GetIntEnv("ENCODING_SCHEDULER_BURST", 3)
	cfg.WebPQuality = GetIntEnv("WEBP_QUALITY", 0)
	cfg.EnableEncoding = GetBoolEnv("ENABLE_ENCODING", false)
	cfg.EncodingWorkers = GetIntEnv("ENCODING_WORKERS", 2)
	cfg.MetricsAddr = GetEnvOrDefault("METRICS_ADDR", ":9090")

	cfg.EnableCaptionGeneration = GetBoolEnv("ENABLE_CAPTION_GENERATION", false)
	cfg.WhisperProvider = GetEnvOrDefault("WHISPER_PROVIDER", "local")
	cfg.WhisperModelSize = GetEnvOrDefault("WHISPER_MODEL_SIZE", "base")
	cfg.WhisperCppPath = GetEnvOrDefault("WHISPER_CPP_PATH", "/usr/local/bin/whisper")
	cfg.WhisperAPIURL = GetEnvOrDefault("WHISPER_API_URL", "")
	cfg.WhisperModelsDir = GetEnvOrDefault("WHISPER_MODELS_DIR", "/var/lib/whisper/models")
	cfg.WhisperOpenAIAPIKey = GetEnvOrDefault("WHISPER_OPENAI_API_KEY", "")
	cfg.WhisperTempDir = GetEnvOrDefault("WHISPER_TEMP_DIR", filepath.Join(os.TempDir(), "whisper"))
	cfg.CaptionGenerationWorkers = GetIntEnv("CAPTION_GENERATION_WORKERS", 2)
	cfg.AutoCaptionFormat = GetEnvOrDefault("AUTO_CAPTION_FORMAT", "vtt")
	cfg.AutoCaptionLanguage = GetEnvOrDefault("AUTO_CAPTION_LANGUAGE", "")

	cfg.EnableATProto = GetBoolEnv("ENABLE_ATPROTO", false)
	cfg.ATProtoPDSURL = GetEnvOrDefault("ATPROTO_PDS_URL", "")
	cfg.ATProtoAuthToken = GetEnvOrDefault("ATPROTO_AUTH_TOKEN", "")
	cfg.ATProtoHandle = GetEnvOrDefault("ATPROTO_HANDLE", "")
	cfg.ATProtoAppPassword = GetEnvOrDefault("ATPROTO_APP_PASSWORD", "")
	cfg.ATProtoTokenKey = GetEnvOrDefault("ATPROTO_TOKEN_KEY", "")
	cfg.ATProtoRefreshIntervalSeconds = GetIntEnv("ATPROTO_REFRESH_INTERVAL_SECONDS", int(ATProtoDefaultRefreshInterval.Seconds()))
	cfg.ATProtoUseImageEmbed = GetBoolEnv("ATPROTO_USE_IMAGE_EMBED", false)
	cfg.ATProtoImageAltField = GetEnvOrDefault("ATPROTO_IMAGE_ALT_FIELD", "description")

	cfg.NginxEnabled = GetBoolEnv("NGINX_ENABLED", false)
	cfg.NginxDomain = GetEnvOrDefault("NGINX_DOMAIN", "localhost")
	cfg.NginxPort = GetIntEnv("NGINX_PORT", 80)
	cfg.NginxProtocol = GetEnvOrDefault("NGINX_PROTOCOL", "http")
	cfg.NginxTLSMode = GetEnvOrDefault("NGINX_TLS_MODE", "")
	cfg.NginxLetsEncryptEmail = GetEnvOrDefault("NGINX_LETSENCRYPT_EMAIL", "")

	cfg.PublicBaseURL = GetEnvOrDefault("PUBLIC_BASE_URL", "")

	cfg.EnableFederationScheduler = GetBoolEnv("ENABLE_FEDERATION_SCHEDULER", prodDefault(setupMode, true, false))
	cfg.FederationSchedulerIntervalSeconds = GetIntEnv("FEDERATION_SCHEDULER_INTERVAL_SECONDS", 15)
	cfg.FederationSchedulerBurst = GetIntEnv("FEDERATION_SCHEDULER_BURST", 1)
	cfg.FederationIngestIntervalSeconds = GetIntEnv("FEDERATION_INGEST_INTERVAL_SECONDS", 60)
	cfg.FederationIngestMaxItems = GetIntEnv("FEDERATION_INGEST_MAX_ITEMS", 40)
	cfg.FederationIngestMaxPages = GetIntEnv("FEDERATION_INGEST_MAX_PAGES", 2)
	cfg.EnableATProtoLabeler = GetBoolEnv("ENABLE_ATPROTO_LABELER", false)
	cfg.EnableATProtoFirehose = GetBoolEnv("ENABLE_ATPROTO_FIREHOSE", false)
	cfg.ATProtoFirehosePollIntervalSeconds = GetIntEnv("ATPROTO_FIREHOSE_POLL_INTERVAL_SECONDS", 5)

	cfg.EnableActivityPub = GetBoolEnv("ENABLE_ACTIVITYPUB", false)
	cfg.ActivityPubDomain = GetEnvOrDefault("ACTIVITYPUB_DOMAIN", "")
	cfg.ActivityPubDeliveryWorkers = GetIntEnv("ACTIVITYPUB_DELIVERY_WORKERS", 5)
	cfg.ActivityPubDeliveryRetries = GetIntEnv("ACTIVITYPUB_DELIVERY_RETRIES", 10)
	cfg.ActivityPubDeliveryRetryDelay = GetIntEnv("ACTIVITYPUB_DELIVERY_RETRY_DELAY", 60)
	cfg.ActivityPubAcceptFollowAutomatic = GetBoolEnv("ACTIVITYPUB_ACCEPT_FOLLOW_AUTOMATIC", true)
	cfg.ActivityPubInstanceDescription = GetEnvOrDefault("ACTIVITYPUB_INSTANCE_DESCRIPTION", "A PeerTube-compatible video platform")
	cfg.ActivityPubInstanceContactEmail = GetEnvOrDefault("ACTIVITYPUB_INSTANCE_CONTACT_EMAIL", "")
	cfg.ActivityPubMaxActivitiesPerPage = GetIntEnv("ACTIVITYPUB_MAX_ACTIVITIES_PER_PAGE", 20)
	cfg.ActivityPubKeyEncryptionKey = GetEnvOrDefault("ACTIVITYPUB_KEY_ENCRYPTION_KEY", "")

	cfg.EnableLiveStreaming = GetBoolEnv("ENABLE_LIVE_STREAMING", false)
	cfg.RTMPHost = GetEnvOrDefault("RTMP_HOST", "0.0.0.0")
	cfg.RTMPPort = GetIntEnv("RTMP_PORT", 1935)
	cfg.RTMPMaxConnections = GetIntEnv("RTMP_MAX_CONNECTIONS", 100)
	cfg.RTMPChunkSize = GetIntEnv("RTMP_CHUNK_SIZE", 4096)
	cfg.RTMPReadTimeout = time.Duration(GetIntEnv("RTMP_READ_TIMEOUT", 30)) * time.Second
	cfg.RTMPWriteTimeout = time.Duration(GetIntEnv("RTMP_WRITE_TIMEOUT", 30)) * time.Second
	cfg.MaxStreamDuration = time.Duration(GetIntEnv("MAX_STREAM_DURATION", 0)) * time.Second
	cfg.HLSOutputDir = GetEnvOrDefault("HLS_OUTPUT_DIR", "./storage/live")
	cfg.LiveHLSSegmentLength = GetIntEnv("LIVE_HLS_SEGMENT_LENGTH", 2)
	cfg.LiveHLSWindowSize = GetIntEnv("LIVE_HLS_WINDOW_SIZE", 10)
	cfg.HLSCleanupInterval = time.Duration(GetIntEnv("HLS_CLEANUP_INTERVAL", 10)) * time.Second
	cfg.HLSVariants = GetEnvOrDefault("HLS_VARIANTS", "1080p,720p,480p,360p")

	cfg.FFmpegPath = GetEnvOrDefault("FFMPEG_PATH", "ffmpeg")
	cfg.FFmpegPreset = GetEnvOrDefault("FFMPEG_PRESET", "veryfast")
	cfg.FFmpegTune = GetEnvOrDefault("FFMPEG_TUNE", "zerolatency")
	cfg.MaxConcurrentTranscodes = GetIntEnv("MAX_CONCURRENT_TRANSCODES", 10)

	cfg.EnableReplayConversion = GetBoolEnv("ENABLE_REPLAY_CONVERSION", true)
	cfg.ReplayStorageDir = GetEnvOrDefault("REPLAY_STORAGE_DIR", "./storage/replays")
	cfg.ReplayUploadToIPFS = GetBoolEnv("REPLAY_UPLOAD_TO_IPFS", prodDefault(setupMode, true, false))
	cfg.ReplayRetentionDays = GetIntEnv("REPLAY_RETENTION_DAYS", 30)

	cfg.EnableTorrents = GetBoolEnv("ENABLE_TORRENTS", prodDefault(setupMode, true, false))
	cfg.TorrentListenPort = GetIntEnv("TORRENT_LISTEN_PORT", 6881)
	cfg.TorrentMaxConnections = GetIntEnv("TORRENT_MAX_CONNECTIONS", 200)
	cfg.TorrentUploadRateLimit = GetInt64Env("TORRENT_UPLOAD_RATE_LIMIT", 0)
	cfg.TorrentDownloadRateLimit = GetInt64Env("TORRENT_DOWNLOAD_RATE_LIMIT", 0)
	cfg.TorrentSeedRatio = GetFloat64Env("TORRENT_SEED_RATIO", 2.0)
	cfg.TorrentDataDir = GetEnvOrDefault("TORRENT_DATA_DIR", "./storage/torrents")
	cfg.TorrentCacheSize = GetInt64Env("TORRENT_CACHE_SIZE", 64*1024*1024)
	cfg.TorrentTrackerURL = GetEnvOrDefault("TORRENT_TRACKER_URL", "")
	cfg.TorrentWebSocketTrackerURL = GetEnvOrDefault("TORRENT_WEBSOCKET_TRACKER_URL", "")

	cfg.EnableDHT = GetBoolEnv("ENABLE_DHT", prodDefault(setupMode, true, false))
	cfg.DHTBootstrapNodes = GetStringSliceEnv("DHT_BOOTSTRAP_NODES", []string{
		"router.bittorrent.com:6881",
		"dht.transmissionbt.com:6881",
		"router.utorrent.com:6881",
		"dht.aelitis.com:6881",
	})
	cfg.DHTAnnounceInterval = time.Duration(GetIntEnv("DHT_ANNOUNCE_INTERVAL", 1800)) * time.Second
	cfg.DHTMaxPeers = GetIntEnv("DHT_MAX_PEERS", 500)
	cfg.EnablePEX = GetBoolEnv("ENABLE_PEX", prodDefault(setupMode, true, false))
	cfg.EnableWebTorrent = GetBoolEnv("ENABLE_WEBTORRENT", prodDefault(setupMode, true, false))
	cfg.WebTorrentTrackerPort = GetIntEnv("WEBTORRENT_TRACKER_PORT", 8000)

	cfg.SmartSeedingEnabled = GetBoolEnv("SMART_SEEDING_ENABLED", prodDefault(setupMode, true, false))
	cfg.SmartSeedingMinSeeders = GetIntEnv("SMART_SEEDING_MIN_SEEDERS", 3)
	cfg.SmartSeedingMaxTorrents = GetIntEnv("SMART_SEEDING_MAX_TORRENTS", 100)
	cfg.SmartSeedingPrioritizeViews = GetBoolEnv("SMART_SEEDING_PRIORITIZE_VIEWS", true)

	cfg.HybridDistributionEnabled = GetBoolEnv("HYBRID_DISTRIBUTION_ENABLED", prodDefault(setupMode, true, false))
	cfg.HybridPreferIPFS = GetBoolEnv("HYBRID_PREFER_IPFS", false)
	cfg.HybridFallbackTimeout = time.Duration(GetIntEnv("HYBRID_FALLBACK_TIMEOUT", 10)) * time.Second

	cfg.EnableEmail = GetBoolEnv("ENABLE_EMAIL", false)
	cfg.SMTPTransport = GetEnvOrDefault("SMTP_TRANSPORT", "smtp")
	cfg.SMTPSendmailPath = GetEnvOrDefault("SMTP_SENDMAIL_PATH", "/usr/sbin/sendmail")
	cfg.SMTPHost = GetEnvOrDefault("SMTP_HOST", "localhost")
	cfg.SMTPPort = GetIntEnv("SMTP_PORT", 1025)
	cfg.SMTPUsername = GetEnvOrDefault("SMTP_USERNAME", "")
	cfg.SMTPPassword = GetEnvOrDefault("SMTP_PASSWORD", "")
	cfg.SMTPTLS = GetBoolEnv("SMTP_TLS", false)
	cfg.SMTPDisableSTARTTLS = GetBoolEnv("SMTP_DISABLE_STARTTLS", false)
	cfg.SMTPCAFile = GetEnvOrDefault("SMTP_CA_FILE", "")
	cfg.SMTPFromAddress = GetEnvOrDefault("SMTP_FROM", "noreply@localhost")
	cfg.SMTPFromName = GetEnvOrDefault("SMTP_FROM_NAME", "Athena")

	cfg.VirusScanEnabled = GetBoolEnv("VIRUS_SCAN_ENABLED", prodDefault(setupMode, true, false))
	cfg.ClamAVAddress = GetEnvOrDefault("CLAMAV_ADDRESS", "localhost:3310")
	cfg.VirusScanTimeout = GetIntEnv("VIRUS_SCAN_TIMEOUT", 300)
	cfg.QuarantineDir = GetEnvOrDefault("QUARANTINE_DIR", "./quarantine")
	cfg.VirusScanFallbackOnError = GetBoolEnv("VIRUS_SCAN_FALLBACK", false)
	cfg.VirusScanMaxRetries = GetIntEnv("CLAMAV_MAX_RETRIES", 3)
	cfg.VirusScanRetryDelay = GetIntEnv("CLAMAV_RETRY_DELAY", 1)
	cfg.FileTypeBlockingEnabled = GetBoolEnv("FILE_TYPE_BLOCKING_ENABLED", prodDefault(setupMode, true, false))

	cfg.ObjectStorageConfig = loadObjectStorageConfig()
	cfg.CSPConfig = loadCSPConfig()
	cfg.StaticFilesPrivateAuth = GetBoolEnv("STATIC_FILES_PRIVATE_AUTH", true)
}
