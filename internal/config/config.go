// Package config loads and validates Vidra backend configuration from the
// environment. Configuration is the single source of truth for runtime wiring;
// no other package should read os.Getenv directly.
package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/gommon/bytes"
)

// Config holds all runtime configuration for the vidra-core API service.
type Config struct {
	// Environment is one of "development", "test", or "production".
	Environment string

	// Logging. LogLevel is the minimum emitted level (debug|info|warn|error,
	// default info); LogFormat selects the slog handler (json — default,
	// production — or text for readable local dev). Consumed by
	// internal/observability.NewLogger at startup.
	LogLevel  string
	LogFormat string

	// OpenTelemetry tracing. Opt-in and zero-cost when OTelEnabled is false. When
	// enabled, spans export to OTelExporterEndpoint over OTelExporterProtocol
	// (grpc | http/protobuf); OTelServiceName labels the service in traces.
	OTelEnabled          bool
	OTelExporterEndpoint string
	OTelExporterProtocol string
	OTelServiceName      string

	// MetricsEnabled gates the Prometheus RED-metrics surface (a /metrics scrape
	// endpoint + the request-metrics middleware). Opt-in and zero-cost when false:
	// no registry is built, no middleware is mounted, and /metrics is not routed.
	MetricsEnabled bool

	// HTTP server.
	HTTPHost            string
	HTTPPort            int
	HTTPReadTimeout     time.Duration
	HTTPWriteTimeout    time.Duration
	HTTPShutdownTimeout time.Duration
	// HTTPRequestTimeout bounds per-request handler work via a context deadline
	// (DB/Redis/outbound calls observe it). HTTPWriteTimeout is the hard backstop.
	HTTPRequestTimeout time.Duration
	// HTTPBodyLimit is the maximum accepted request body size, as an Echo size
	// string (e.g. "8M", "512K"). Oversized requests are rejected with 413.
	HTTPBodyLimit string

	// PostgreSQL connection (DSN form, e.g. postgres://user:pass@host:5432/db).
	DatabaseURL string

	// Redis connection (URL form, e.g. redis://host:6379/0).
	RedisURL string

	// CORSAllowedOrigins is the explicit allow-list for browser origins.
	CORSAllowedOrigins []string

	// InstanceName is the human-facing name of this Vidra instance.
	InstanceName string

	// PublicBaseURL is the canonical public origin of this instance (e.g.
	// https://videos.example), used to build federation actor/object ids and
	// URLs. Required when FederationEnabled; must carry no path or trailing
	// slash (a trailing slash is trimmed at load). See .ralph/specs/federation.md.
	PublicBaseURL string

	// FederationEnabled is the master switch for ActivityPub federation. When
	// false (default) all federation routes are unmounted (they 404) — zero cost
	// when off. See .ralph/specs/federation.md.
	FederationEnabled bool

	// ATProtoEnabled is the master switch for the ATProto / Bluesky extension
	// (P10.2, .ralph/specs/atproto.md) — v1 outbound cross-posting only. When
	// false (default) the /me/atproto link/status/unlink endpoints answer 503 and
	// no auto-post worker runs. Independent of FederationEnabled: an instance may
	// enable ActivityPub only, ATProto only, both, or neither.
	ATProtoEnabled bool

	// ATProtoKeyKEK is the base64 (standard) 32-byte key-encryption key used to
	// envelope-encrypt linked Bluesky app passwords at rest (same secretbox
	// envelope as FederationKeyKEK). Deployments already running a federation KEK
	// can share it: ATProtoKEK() falls back to FederationKeyKEK when this is unset.
	// Required in production when ATProtoEnabled; with neither set (dev) app
	// passwords are stored raw with a loud boot warning. NEVER commit a real value.
	ATProtoKeyKEK string

	// ATProtoLoginEnabled is the master switch for ATProto IDENTITY LOGIN (sign in
	// with a Bluesky / any-PDS handle). Independent of ATProtoEnabled (which gates
	// outbound cross-posting): an instance may enable either, both, or neither.
	// When false (default) the /auth/atproto/start + /callback endpoints answer 503.
	// No KEK is needed — identity login keeps no PDS tokens.
	ATProtoLoginEnabled bool

	// MalwareScanEnabled turns on ClamAV scanning of uploaded originals before
	// publish (fail-closed: infected or unscannable media is not published).
	// Requires ClamAVAddr. Default false.
	MalwareScanEnabled bool

	// ClamAVAddr is the clamd TCP address (host:port) used when MalwareScanEnabled.
	ClamAVAddr string

	// ClamAVTimeout bounds a single INSTREAM scan: the connection deadline the
	// scanner sets before streaming the object to clamd. A slow or wedged clamd
	// therefore surfaces as a scan error within this window (which MalwareScanMode
	// then resolves) rather than hanging the upload finalisation. Parsed with
	// time.ParseDuration (e.g. "60s", "2m"); default 60s; must be > 0 when
	// MalwareScanEnabled.
	ClamAVTimeout time.Duration

	// MalwareScanMode is the ClamAV fallback policy applied when a scan cannot
	// complete (a dial/protocol/IO error): "fail-closed" (default — unscannable
	// media is not published), "fail-open" (publish anyway, logged loudly), or
	// "quarantine" (park for moderator review). An INFECTED result always fails
	// the publish regardless of mode. Validated to one of the three.
	MalwareScanMode string

	// TranscodingEnabled turns on the HLS transcoding pipeline: publishing a
	// video enqueues a durable transcode job and an in-process worker produces
	// an H.264/AAC HLS ladder served at /api/v1/videos/{id}/hls/* (this is what
	// powers the player's resolution/quality selector — without it a video only
	// serves its single progressive original). Requires ffmpeg + ffprobe on
	// PATH (detected at boot; a missing binary logs a warning and leaves
	// transcoding off, so the default is safe on a host without ffmpeg).
	// Default true — a video platform ladders uploads out of the box; set
	// TRANSCODING_ENABLED=false to serve originals only.
	TranscodingEnabled bool

	// TranscodingVP9Enabled additionally emits a progressive VP9/WebM alternate
	// alongside the H.264 HLS ladder (surfaced via the video /download list).
	// Only meaningful with TranscodingEnabled. Default false.
	TranscodingVP9Enabled bool

	// TranscodingAV1Enabled is accepted only as false: AV1 transcoding is
	// deferred (see fix_plan P6.3, mirrors the IPFS-storage defer). Setting it
	// true fails config validation with a documented defer note.
	TranscodingAV1Enabled bool

	// TranscodeHoldTimeout bounds how long a publish-after-transcode video may sit
	// in the 'transcoding' hold before the stuck-hold sweeper publishes it anyway
	// (a crashed worker or a lost job — the retained original is playable, so
	// hiding it forever is worse). The sweeper runs every few minutes; this is the
	// age threshold. Parsed by time.ParseDuration; default 12h. Tunable via
	// TRANSCODE_HOLD_TIMEOUT.
	TranscodeHoldTimeout time.Duration

	// WhisperEnabled turns on auto-caption generation (fix_plan P13): a video
	// owner may request an auto-generated WebVTT caption track, produced by
	// extracting the audio (ffmpeg → 16 kHz mono WAV) and POSTing it to an
	// external Whisper transcription service. Requires WhisperEndpoint. Default
	// false — the auto-caption endpoints then answer 503 and no worker runs.
	WhisperEnabled bool

	// WhisperEndpoint is the base URL of the Whisper transcription service. The
	// worker POSTs the extracted audio to <endpoint>/inference as multipart/form-
	// data (`file` + `response_format=verbose_json` [+ `language` hint]) and
	// expects an OpenAI-/whisper.cpp-compatible verbose_json body with a
	// `segments` array of {start,end,text} (seconds), which it renders to WebVTT.
	// It is an OPERATOR-configured internal service (not user input), so it is
	// trusted and not SSRF-guarded. Required when WhisperEnabled.
	WhisperEndpoint string

	// WhisperDefaultLanguage is the BCP-47-ish caption language tag used when an
	// auto-caption request omits one — it is both the tag the caption is stored
	// under and the language hint passed to Whisper. Default "en".
	WhisperDefaultLanguage string

	// --- vidra-search integration (search-service W4) ---
	//
	// SearchServiceURL is the vidra-search origin (e.g. http://search:8080), with
	// any trailing slash trimmed. Empty DISABLES the integration entirely:
	// suggestions degrade to an empty list, search + recommendations fall back to
	// local SQL, and no search client or outbox/reconcile workers run.
	// SearchServiceEnabled() reports URL != "".
	SearchServiceURL string
	// SearchInternalSecret is the shared HMAC secret for the internal S2S auth
	// header (matches vidra-search INTERNAL_SECRET). Required (>=32 chars) in
	// production when SearchServiceURL is set; NEVER commit a real value.
	SearchInternalSecret string
	// SearchSuggestTimeoutMS / SearchQueryTimeoutMS / SearchRecsTimeoutMS override
	// the per-endpoint client deadlines in milliseconds (0 = the client defaults,
	// 250 / 800 / 800 ms respectively).
	SearchSuggestTimeoutMS int
	SearchQueryTimeoutMS   int
	SearchRecsTimeoutMS    int
	// SearchReconcileInterval is the cadence of the full index reconcile sweep
	// (default 24h). Tunable via SEARCH_RECONCILE_INTERVAL.
	SearchReconcileInterval time.Duration
	// SearchHealthInterval is the base cadence of the background health prober
	// that GETs the search service's public /healthz (default 15s; jittered ±20%
	// per tick). The prober is the active half of the routing policy (W9): a down
	// service is detected within ~2 intervals so gateway surfaces skip it (zero
	// per-request latency) instead of each paying a full timeout. Tunable via
	// SEARCH_HEALTH_INTERVAL. Only runs when SearchServiceURL is set.
	SearchHealthInterval time.Duration

	// FederationKeyKEK is the base64 (standard) 32-byte key-encryption key used to
	// envelope-encrypt actor private keys at rest (AES-256-GCM via internal/secretbox).
	// Required in production when FederationEnabled; empty in dev stores keys raw
	// (with a loud boot warning). NEVER commit a real value. See federation.md §3.
	FederationKeyKEK string

	// MFAKeyKEK is the base64 (standard) 32-byte key-encryption key used to
	// envelope-encrypt TOTP shared secrets at rest (same secretbox envelope as
	// FederationKeyKEK). Deployments that already run a federation KEK can share
	// it: MFAKEK() falls back to FederationKeyKEK when this is unset. With
	// neither set (dev), TOTP secrets are stored raw with a loud boot warning.
	// NEVER commit a real value.
	MFAKeyKEK string

	// TOTPIssuer is the issuer label embedded in TOTP enrollment otpauth:// URIs
	// — what authenticator apps display next to the account. Defaults to the
	// instance name.
	TOTPIssuer string

	// LiveRTMPURL is the base RTMP ingest URL returned to a streamer on live-stream
	// create (the streamer appends their stream key in OBS). Empty until an RTMP
	// ingest is provisioned; the create response then omits it.
	LiveRTMPURL string

	// LiveIngestSecret is the shared secret the media server (RTMP ingest) presents
	// on the internal live-ingest start/stop hooks, so only it can flip a stream's
	// live state. Empty disables the ingest hooks entirely (they 404) — they are
	// only safe to expose when a secret is set.
	LiveIngestSecret string

	// LiveHLSRoot is the filesystem directory the RTMP media server writes live
	// HLS output (and session recordings) into — a volume shared read-only with
	// the api. When set, the api serves a live stream's playlist/segments from it
	// (keyed by stream ID) at GET /api/v1/live/{id}/hls/* and, for replay-enabled
	// streams, reads the recorded session from its `rec/` subdirectory to
	// republish as a VOD. Empty (default) disables live HLS serving and replay —
	// both surface as 404 / no-op until a media server is provisioned.
	LiveHLSRoot string

	// Instance about/legal metadata surfaced at GET /api/v1/instance. All
	// optional (empty when unset).
	InstanceDescription  string
	InstanceTermsURL     string
	InstancePrivacyURL   string
	InstanceContactEmail string

	// OAuthProviders are the configured OIDC login providers (OAUTH_PROVIDERS
	// comma list; per-provider OAUTH_<NAME>_ISSUER/_CLIENT_ID/_CLIENT_SECRET and
	// optional _SCOPES). Empty (the default) disables OAuth login entirely.
	// PublicBaseURL is required when any provider is configured — the OAuth
	// callback redirect URI is ALWAYS derived from it server-side (P15), never
	// from request parameters. Client secrets are secrets: never log them
	// (observability sensitive-key rules).
	OAuthProviders []OAuthProviderConfig

	// RegistrationEnabled controls whether public account signup is accepted.
	RegistrationEnabled bool

	// RegistrationRequireApproval, when true (and registration is enabled), makes
	// signup file a pending registration request for admin approval instead of
	// creating an account directly. Default false.
	RegistrationRequireApproval bool

	// QuarantineNewUploads, when true, parks a finished upload by a
	// non-privileged user (role "user" without the admin-granted
	// bypass_quarantine flag) in the 'quarantined' state instead of publishing,
	// until a moderator approves (publish, hooks fire then) or rejects it.
	// Default false. See product-decisions.md §11.
	QuarantineNewUploads bool

	// Feature toggles (all default true). These are the boot-time defaults for
	// the runtime-mutable instance feature switches: an admin can override each
	// live via PATCH /api/v1/admin/instance-settings (the DB overlay), and the
	// upload/import/live-create/comment-create gates consult the effective
	// value. When a toggle is off, the corresponding endpoint returns 403
	// feature_disabled. See fix_plan P10 (admin instance configuration).
	UploadsEnabled  bool
	ImportsEnabled  bool
	LiveEnabled     bool
	CommentsEnabled bool

	// MailEnabled turns on real outbound email over SMTP: password-reset and
	// email-verification tokens are delivered to the account's address instead
	// of being dropped (the no-op default). Requires SMTPHost + SMTPFrom.
	// DEV_MAIL_CAPTURE_ENABLED still wins when both are set (dev seam).
	MailEnabled bool

	// SMTP delivery settings (used when MailEnabled). SMTPHost/SMTPPort locate
	// the relay (STARTTLS is used whenever the server offers it); SMTPUsername/
	// SMTPPassword are optional AUTH PLAIN credentials — the password is a
	// secret and must NEVER be logged (observability sensitive-key rules);
	// SMTPFrom is the sender address on outgoing mail.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	// DevMailCaptureEnabled turns on the DEVELOPMENT-ONLY in-memory mail capture:
	// account-security tokens (password reset, email verification) are held in
	// memory and retrievable via GET /api/v1/dev/email-token instead of being
	// delivered out-of-band. It exists so end-to-end tests can complete the token
	// flows without a real mailer. NEVER enable it in production — it exposes
	// single-use credentials. Default false; the process warns loudly when on.
	DevMailCaptureEnabled bool

	// ImportAllowPrivateURLs relaxes the SSRF guard on URL video import so it may
	// fetch from private/loopback addresses. DEVELOPMENT/TEST-ONLY: it exists so
	// backed end-to-end tests can import from a loopback/compose-network origin
	// (a public origin isn't reachable in CI). NEVER enable it in production — it
	// re-opens the SSRF hole the guard closes. Default false; the process warns
	// loudly when on. See internal/urlsafety.Guard.AllowPrivate.
	ImportAllowPrivateURLs bool

	// Rate limiting (Redis fixed-window) applied to the /api surface.
	RateLimitEnabled  bool
	RateLimitRequests int
	RateLimitWindow   time.Duration
	// AuthRateLimitRequests is a stricter per-IP budget applied (over the same
	// RateLimitWindow) to the sensitive auth endpoints — login, register, and the
	// password-reset / email-verify confirmations — to throttle credential
	// stuffing and token guessing. Gated by RateLimitEnabled.
	AuthRateLimitRequests int
	// AttachmentUploadRateLimitRequests / AttachmentUploadRateLimitWindow bound how
	// many DM attachment uploads a single user may make per window. DM attachments
	// do NOT count against the storage quota (messaging-v2.md D6, product-decisions
	// §14), so this per-user upload limit is the compensating anti-abuse control.
	// Keyed per authenticated user (not per IP). Gated by RateLimitEnabled.
	AttachmentUploadRateLimitRequests int
	AttachmentUploadRateLimitWindow   time.Duration

	// JWT signing for access tokens (HS256).
	JWTSecret    string
	JWTIssuer    string
	JWTAudience  string
	JWTAccessTTL time.Duration
	// JWTRefreshTTL is the lifetime of an opaque refresh-token session.
	JWTRefreshTTL time.Duration

	// Media storage. StorageBackend selects the AUTHORITATIVE blob backend:
	// "local" (default) or "s3". "ipfs" is deliberately rejected — IPFS is a
	// mirror SIDECAR, never an authoritative backend (authority ≠ distribution);
	// see .ralph/specs/ipfs-media.md and the IPFS* fields below. StorageLocalRoot
	// is the directory for the local backend.
	StorageBackend   string
	StorageLocalRoot string

	// S3-compatible object storage (STORAGE_BACKEND=s3): MinIO (dev, compose
	// "storage" profile), AWS S3, Backblaze B2, DigitalOcean Spaces. Endpoint is
	// host[:port] WITHOUT a scheme — StorageS3UseSSL selects http/https;
	// StorageS3ForcePathStyle addresses the bucket as /<bucket>/<key> (required
	// by MinIO). StorageS3AccessKey/StorageS3SecretKey are credentials and must
	// NEVER be logged (see internal/observability.IsSensitiveKey).
	StorageS3Endpoint       string
	StorageS3Bucket         string
	StorageS3AccessKey      string
	StorageS3SecretKey      string
	StorageS3Region         string
	StorageS3UseSSL         bool
	StorageS3ForcePathStyle bool

	// Hybrid IPFS media mirroring (fix_plan P19, .ralph/specs/ipfs-media.md). IPFS
	// is an orthogonal MIRROR of eligible ALREADY-PUBLIC media, present in
	// addition to the authoritative local/S3 store — never a replacement backend,
	// never a single point of failure for uploads or playback. All OFF by default,
	// so existing deploys and `make ci` are unaffected.
	//
	// IPFSEnabled is the master switch: off ⇒ no worker, no enqueue, the admin
	// endpoints answer 503 ipfs_disabled.
	IPFSEnabled bool
	// IPFSAPIURL is the Kubo RPC address (e.g. http://ipfs:5001). Required when
	// enabled. IPFSGatewayURL is the public-facing gateway base emitted in API
	// responses (e.g. https://ipfs.example.org) — required when enabled so we never
	// default to ipfs.io.
	IPFSAPIURL     string
	IPFSGatewayURL string
	// IPFSAddTimeout bounds each add/pin RPC; IPFSPinConcurrency bounds worker
	// parallelism (pinning is I/O heavy); IPFSReconcileInterval is the backfill
	// scan cadence.
	IPFSAddTimeout        time.Duration
	IPFSPinConcurrency    int
	IPFSReconcileInterval time.Duration
	// IPFSMirrorPrivate is the master opt-in for the PRIVATE mirroring tier
	// (fix_plan P19.P, .ralph/specs/ipfs-media-private.md). It defaults false and,
	// when true, REQUIRES a dedicated private-swarm kubo at IPFSPrivateAPIURL —
	// non-public media is replicated ONLY within that operator-controlled swarm,
	// NEVER to the public network. SUPERSESSION: the shipped P19 guard (this flag
	// required a private IPFSClusterAPIURL, a proxy for "private infra exists") is
	// REPLACED by the stricter IPFSPrivateAPIURL requirement below — the real
	// requirement is a separate node, not just a cluster URL. Private-media
	// eligibility/routing is layered on in P19.P2; P19.P1 lands the config +
	// second-client foundation only.
	IPFSMirrorPrivate bool
	// IPFSClusterAPIURL is the optional IPFS Cluster REST API for PUBLIC-tier
	// replication. IPFSClusterToken is its Bearer token — a SECRET, never logged
	// (on the observability.IsSensitiveKey denylist as ipfs_cluster_token).
	IPFSClusterAPIURL string
	IPFSClusterToken  string
	// IPFSPrivateAPIURL is the RPC of the DEDICATED private-swarm kubo (the second,
	// swarm.key'd node — never the public IPFSAPIURL). REQUIRED when
	// IPFSMirrorPrivate=true; must differ from IPFSAPIURL (refusing to dual-home,
	// spec §1). There is deliberately NO IPFSPrivateGatewayURL: private CIDs are
	// never emitted with a gateway URL, and not having the knob is the guarantee.
	IPFSPrivateAPIURL string
	// IPFSPrivateClusterAPIURL / IPFSPrivateClusterToken are the OPTIONAL IPFS
	// Cluster REST API + Bearer token on the PRIVATE swarm (P19.P3 replication).
	// The token is a SECRET (denylist ipfs_private_cluster_token).
	IPFSPrivateClusterAPIURL string
	IPFSPrivateClusterToken  string
	// IPFSPrivateAddTimeout / IPFSPrivatePinConcurrency tune the private-network
	// worker independently; they INHERIT the public defaults when unset.
	IPFSPrivateAddTimeout     time.Duration
	IPFSPrivatePinConcurrency int

	// UploadMaxSize caps a single original-file upload, as an Echo size string
	// (e.g. "2G", "512M"). It overrides HTTPBodyLimit for the upload route only,
	// so the JSON API stays small while media uploads get headroom. Oversized
	// uploads are rejected with 413.
	UploadMaxSize string

	// UploadMaxActiveSessionsPerUser caps how many ACTIVE (unfinished, unexpired)
	// resumable upload sessions one user may hold at once — the batch-upload guard
	// (UPLOAD-10). A create past the cap is refused 429 with the stable code
	// too_many_active_uploads so a batch-upload client queues on it; cancelling or
	// completing an in-flight session frees a slot. 0 disables the limit. Default 5.
	UploadMaxActiveSessionsPerUser int

	// yt-dlp platform-URL import (backport W2.C1, UPLOAD-09). OFF BY DEFAULT —
	// admin/config opt-in only. When on, a URL import may resolve through the
	// hard-sandboxed yt-dlp extractor (internal/ytdlp) instead of a plain media
	// fetch. See .ralph/specs/backport-w2-upload-import.md §5 for the sandboxing
	// and the honest residual-risk stance (yt-dlp's own fetches cannot be
	// dial-pinned, so production SHOULD pair it with YtdlpProxy / a no-egress
	// network). None of these are secrets.

	// YtdlpImportEnabled turns on the ytdlp resolver. When false (default) an
	// explicit resolver=ytdlp import is refused with 503, and resolver=auto never
	// falls back to yt-dlp.
	YtdlpImportEnabled bool
	// YtdlpPath is the yt-dlp executable (PATH lookup name or absolute path).
	YtdlpPath string
	// YtdlpTimeout is the hard wall-clock bound on a single yt-dlp invocation
	// (metadata probe or download); on expiry the child is killed.
	YtdlpTimeout time.Duration
	// YtdlpProxy, when set, is passed to yt-dlp as --proxy: the recommended
	// production egress proxy that denies RFC1918/loopback/link-local so the
	// extractor's own outbound fetches cannot reach internal addresses.
	YtdlpProxy string
	// YtdlpMaxHeight caps the selected video resolution (yt-dlp -f height<=N).
	YtdlpMaxHeight int

	// Channel auto-sync (backport W2.C4, UPLOAD-13). Mirrors an external platform
	// channel's uploads into a local channel by listing it with the sandboxed
	// yt-dlp extractor and enqueuing `ytdlp` imports for unseen entries. OFF by
	// default and additionally gated on YtdlpImportEnabled (the sync path IS a
	// yt-dlp import path). None of these are secrets.

	// ChannelSyncEnabled turns on the channel auto-sync HTTP surface + worker.
	// Effective only when YtdlpImportEnabled is also true; when either is off the
	// endpoints answer 503 and no worker runs.
	ChannelSyncEnabled bool
	// ChannelSyncInterval is the cadence at which a sync re-lists its external
	// channel (POST .../sync-now schedules an immediate run).
	ChannelSyncInterval time.Duration
	// ChannelSyncMaxPerUser caps how many channel syncs one user may hold (<= 0
	// disables the cap).
	ChannelSyncMaxPerUser int
	// ChannelSyncBatch caps how many of the newest external uploads one sync pass
	// imports (yt-dlp --playlist-end).
	ChannelSyncBatch int
	// ChannelSyncCooldown is the minimum spacing between manual POST .../sync-now
	// triggers, measured from the last completed run. A request inside the window
	// is rejected 429. <= 0 disables the throttle.
	ChannelSyncCooldown time.Duration

	// PeerTube import / migration (fix_plan P18, .ralph/specs/peertube-import.md).
	// A one-way tool that reads an existing PeerTube instance's PostgreSQL DB +
	// media storage and maps it into Vidra. Everything here is OFF BY DEFAULT and
	// read-only on the source. The source DSN and S3 keys are SECRETS — never
	// committed, never logged (see internal/observability sensitive-key denylist).

	// PeerTubeImportEnabled gates the admin import API surface (POST/GET
	// /api/v1/admin/peertube-import). When false (default) those endpoints answer
	// 503 and no import worker runs. The cmd/peertube-import CLI is independent of
	// this flag (it takes its own source flags). Requires PeerTubeSourceDatabaseURL.
	PeerTubeImportEnabled bool

	// PeerTubeSourceDatabaseURL is the READ-ONLY DSN of the source PeerTube
	// PostgreSQL database (a dump restored to a scratch DB, or a read-only
	// replica). Connect with a least-privilege read-only role. SECRET: never
	// commit or log it. Empty (default) disables the admin import path.
	PeerTubeSourceDatabaseURL string

	// PeerTubeSourceStorageBackend selects where the source instance's media
	// lives: "local" (a mounted filesystem copy) or "s3" (an S3-compatible store).
	// Default "local".
	PeerTubeSourceStorageBackend string
	// PeerTubeSourceStorageLocalRoot is the directory the source instance's media
	// tree is mounted at (read-only), used when the source storage backend is
	// "local". Path-traversal-guarded on every read.
	PeerTubeSourceStorageLocalRoot string
	// PeerTubeSourceS3* address the source instance's S3-compatible media store
	// (read-only). Endpoint is host[:port] WITHOUT a scheme; AccessKey/SecretKey
	// are SECRETS (never logged, same rules as the primary STORAGE_S3_* keys).
	PeerTubeSourceS3Endpoint       string
	PeerTubeSourceS3Bucket         string
	PeerTubeSourceS3AccessKey      string
	PeerTubeSourceS3SecretKey      string
	PeerTubeSourceS3Region         string
	PeerTubeSourceS3UseSSL         bool
	PeerTubeSourceS3ForcePathStyle bool

	// PeerTubeImportConflictPolicy is the default resolution for username/handle/
	// email/slug collisions between the source and this instance: "skip" (default,
	// safest — leave the existing Vidra row, map the source entity to it),
	// "rename" (import under a de-duplicated identifier), "merge" (attach the
	// source entity's children to the existing Vidra row), or "fail" (abort on any
	// collision). The CLI / admin request may override it per run.
	PeerTubeImportConflictPolicy string

	// PeerTubeImportMediaMode selects media handling for admin-launched imports:
	// "copy" (default) streams source media into this instance's storage,
	// "reference" records existing PeerTube object keys and requires STORAGE_* to
	// point at that same bucket, and "none" imports metadata only.
	PeerTubeImportMediaMode string

	// InstanceDefaultQuotaBytes is the default per-user storage quota in bytes:
	// the total stored size of a user's video files (originals, renditions,
	// thumbnails) across the videos owned via their channels. 0 (or unset) =
	// unlimited. Admins can override per account (nullable
	// users.storage_quota_bytes; NULL inherits this default, 0 = unlimited).
	// Uploads/imports that would exceed the effective quota are rejected with
	// 422 quota_exceeded.
	InstanceDefaultQuotaBytes int64
}

// OAuthProviderConfig describes one OIDC login provider. Providers are generic:
// any spec-compliant OIDC issuer (Google, a GitHub OIDC shim, Keycloak,
// Authentik, …) works via its discovery document. ClientSecret is sensitive
// and must never be logged.
type OAuthProviderConfig struct {
	// Name is the URL-safe identifier used in routes
	// (/api/v1/auth/oauth/<name>) and env-var lookups (dashes map to
	// underscores): lowercase letters, digits, and dashes.
	Name string
	// IssuerURL is the OIDC issuer; discovery is fetched from
	// <issuer>/.well-known/openid-configuration.
	IssuerURL string
	// ClientID / ClientSecret are the OAuth2 client credentials registered
	// with the provider.
	ClientID     string
	ClientSecret string
	// Scopes overrides the requested scopes (default: openid email profile).
	Scopes []string
}

// devJWTSecret is the obviously-fake signing key used only for local dev/test.
// Production must override JWT_SECRET; validate() rejects this value in prod.
const devJWTSecret = "dev-insecure-jwt-secret-change-me-0000000000000000"

// Load reads configuration from the environment, applying safe development
// defaults. It returns an error if a required value is missing or malformed.
//
// Required in production: DATABASE_URL, REDIS_URL. In development they default
// to local Docker Compose service addresses.
func Load() (*Config, error) {
	env := getEnv("VIDRA_ENV", "development")

	cfg := &Config{
		Environment:                    env,
		LogLevel:                       strings.ToLower(getEnv("LOG_LEVEL", "info")),
		LogFormat:                      strings.ToLower(getEnv("LOG_FORMAT", "json")),
		OTelEnabled:                    getEnvBool("OTEL_ENABLED", false),
		OTelExporterEndpoint:           getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTelExporterProtocol:           strings.ToLower(getEnv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")),
		OTelServiceName:                getEnv("OTEL_SERVICE_NAME", "vidra-core"),
		MetricsEnabled:                 getEnvBool("METRICS_ENABLED", false),
		HTTPHost:                       getEnv("HTTP_HOST", "0.0.0.0"),
		InstanceName:                   getEnv("INSTANCE_NAME", "Vidra (dev)"),
		PublicBaseURL:                  strings.TrimRight(getEnv("PUBLIC_BASE_URL", ""), "/"),
		FederationEnabled:              getEnvBool("FEDERATION_ENABLED", false),
		FederationKeyKEK:               getEnv("FEDERATION_KEY_KEK", ""),
		ATProtoEnabled:                 getEnvBool("ATPROTO_ENABLED", false),
		ATProtoKeyKEK:                  getEnv("ATPROTO_KEY_KEK", ""),
		ATProtoLoginEnabled:            getEnvBool("ATPROTO_LOGIN_ENABLED", false),
		MFAKeyKEK:                      getEnv("MFA_KEY_KEK", ""),
		TOTPIssuer:                     getEnv("TOTP_ISSUER", ""),
		MalwareScanEnabled:             getEnvBool("MALWARE_SCAN_ENABLED", false),
		ClamAVAddr:                     getEnv("CLAMAV_ADDR", ""),
		ClamAVTimeout:                  getEnvDuration("CLAMAV_TIMEOUT", 60*time.Second),
		MalwareScanMode:                getEnv("MALWARE_SCAN_MODE", "fail-closed"),
		TranscodingEnabled:             getEnvBool("TRANSCODING_ENABLED", true),
		TranscodingVP9Enabled:          getEnvBool("TRANSCODING_VP9_ENABLED", false),
		TranscodingAV1Enabled:          getEnvBool("TRANSCODING_AV1_ENABLED", false),
		TranscodeHoldTimeout:           getEnvDuration("TRANSCODE_HOLD_TIMEOUT", 12*time.Hour),
		WhisperEnabled:                 getEnvBool("WHISPER_ENABLED", false),
		WhisperEndpoint:                strings.TrimRight(getEnv("WHISPER_ENDPOINT", ""), "/"),
		WhisperDefaultLanguage:         strings.TrimSpace(getEnv("WHISPER_DEFAULT_LANGUAGE", "en")),
		SearchServiceURL:               strings.TrimRight(getEnv("SEARCH_SERVICE_URL", ""), "/"),
		SearchInternalSecret:           getEnv("SEARCH_INTERNAL_SECRET", ""),
		SearchReconcileInterval:        getEnvDuration("SEARCH_RECONCILE_INTERVAL", 24*time.Hour),
		SearchHealthInterval:           getEnvDuration("SEARCH_HEALTH_INTERVAL", 15*time.Second),
		LiveRTMPURL:                    getEnv("LIVE_RTMP_URL", ""),
		LiveIngestSecret:               getEnv("LIVE_INGEST_SECRET", ""),
		LiveHLSRoot:                    strings.TrimRight(getEnv("LIVE_HLS_ROOT", ""), "/"),
		InstanceDescription:            getEnv("INSTANCE_DESCRIPTION", ""),
		InstanceTermsURL:               getEnv("INSTANCE_TERMS_URL", ""),
		InstancePrivacyURL:             getEnv("INSTANCE_PRIVACY_URL", ""),
		InstanceContactEmail:           getEnv("INSTANCE_CONTACT_EMAIL", ""),
		RegistrationEnabled:            getEnvBool("REGISTRATION_ENABLED", true),
		RegistrationRequireApproval:    getEnvBool("REGISTRATION_REQUIRE_APPROVAL", false),
		QuarantineNewUploads:           getEnvBool("QUARANTINE_NEW_UPLOADS", false),
		UploadsEnabled:                 getEnvBool("FEATURE_UPLOADS_ENABLED", true),
		ImportsEnabled:                 getEnvBool("FEATURE_IMPORTS_ENABLED", true),
		LiveEnabled:                    getEnvBool("FEATURE_LIVE_ENABLED", true),
		CommentsEnabled:                getEnvBool("FEATURE_COMMENTS_ENABLED", true),
		MailEnabled:                    getEnvBool("MAIL_ENABLED", false),
		SMTPHost:                       getEnv("SMTP_HOST", ""),
		SMTPUsername:                   getEnv("SMTP_USERNAME", ""),
		SMTPPassword:                   getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:                       getEnv("SMTP_FROM", ""),
		DevMailCaptureEnabled:          getEnvBool("DEV_MAIL_CAPTURE_ENABLED", false),
		ImportAllowPrivateURLs:         getEnvBool("HTTP_IMPORT_ALLOW_PRIVATE_URLS", false),
		DatabaseURL:                    getEnv("DATABASE_URL", "postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable"),
		RedisURL:                       getEnv("REDIS_URL", "redis://localhost:6379/0"),
		CORSAllowedOrigins:             splitAndTrim(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3000")),
		HTTPReadTimeout:                getEnvDuration("HTTP_READ_TIMEOUT", 15*time.Second),
		HTTPWriteTimeout:               getEnvDuration("HTTP_WRITE_TIMEOUT", 30*time.Second),
		HTTPShutdownTimeout:            getEnvDuration("HTTP_SHUTDOWN_TIMEOUT", 20*time.Second),
		HTTPRequestTimeout:             getEnvDuration("HTTP_REQUEST_TIMEOUT", 30*time.Second),
		HTTPBodyLimit:                  getEnv("HTTP_BODY_LIMIT", "8M"),
		RateLimitEnabled:               getEnvBool("RATE_LIMIT_ENABLED", true),
		RateLimitWindow:                getEnvDuration("RATE_LIMIT_WINDOW", time.Minute),
		JWTSecret:                      getEnv("JWT_SECRET", devJWTSecret),
		JWTIssuer:                      getEnv("JWT_ISSUER", "vidra"),
		JWTAudience:                    getEnv("JWT_AUDIENCE", "vidra"),
		JWTAccessTTL:                   getEnvDuration("JWT_ACCESS_TTL", 15*time.Minute),
		JWTRefreshTTL:                  getEnvDuration("JWT_REFRESH_TTL", 720*time.Hour),
		StorageBackend:                 getEnv("STORAGE_BACKEND", "local"),
		StorageLocalRoot:               getEnv("STORAGE_LOCAL_ROOT", "./data/media"),
		StorageS3Endpoint:              getEnv("STORAGE_S3_ENDPOINT", ""),
		StorageS3Bucket:                getEnv("STORAGE_S3_BUCKET", ""),
		StorageS3AccessKey:             getEnv("STORAGE_S3_ACCESS_KEY", ""),
		StorageS3SecretKey:             getEnv("STORAGE_S3_SECRET_KEY", ""),
		StorageS3Region:                getEnv("STORAGE_S3_REGION", ""),
		StorageS3UseSSL:                getEnvBool("STORAGE_S3_USE_SSL", true),
		StorageS3ForcePathStyle:        getEnvBool("STORAGE_S3_FORCE_PATH_STYLE", false),
		IPFSEnabled:                    getEnvBool("IPFS_ENABLED", false),
		IPFSAPIURL:                     strings.TrimRight(getEnv("IPFS_API_URL", ""), "/"),
		IPFSGatewayURL:                 strings.TrimRight(getEnv("IPFS_GATEWAY_URL", ""), "/"),
		IPFSAddTimeout:                 getEnvDuration("IPFS_ADD_TIMEOUT", 60*time.Second),
		IPFSReconcileInterval:          getEnvDuration("IPFS_RECONCILE_INTERVAL", 5*time.Minute),
		IPFSMirrorPrivate:              getEnvBool("IPFS_MIRROR_PRIVATE", false),
		IPFSClusterAPIURL:              strings.TrimRight(getEnv("IPFS_CLUSTER_API_URL", ""), "/"),
		IPFSClusterToken:               getEnv("IPFS_CLUSTER_TOKEN", ""),
		IPFSPrivateAPIURL:              strings.TrimRight(getEnv("IPFS_PRIVATE_API_URL", ""), "/"),
		IPFSPrivateClusterAPIURL:       strings.TrimRight(getEnv("IPFS_PRIVATE_CLUSTER_API_URL", ""), "/"),
		IPFSPrivateClusterToken:        getEnv("IPFS_PRIVATE_CLUSTER_TOKEN", ""),
		UploadMaxSize:                  getEnv("UPLOAD_MAX_SIZE", "2G"),
		YtdlpImportEnabled:             getEnvBool("YTDLP_IMPORT_ENABLED", false),
		YtdlpPath:                      getEnv("YTDLP_PATH", "yt-dlp"),
		YtdlpTimeout:                   getEnvDuration("YTDLP_TIMEOUT", 15*time.Minute),
		YtdlpProxy:                     strings.TrimSpace(getEnv("YTDLP_PROXY", "")),
		ChannelSyncEnabled:             getEnvBool("CHANNEL_SYNC_ENABLED", false),
		ChannelSyncInterval:            getEnvDuration("CHANNEL_SYNC_INTERVAL", time.Hour),
		ChannelSyncCooldown:            getEnvDuration("CHANNEL_SYNC_COOLDOWN", time.Minute),
		PeerTubeImportEnabled:          getEnvBool("PEERTUBE_IMPORT_ENABLED", false),
		PeerTubeSourceDatabaseURL:      getEnv("PEERTUBE_SOURCE_DATABASE_URL", ""),
		PeerTubeSourceStorageBackend:   getEnv("PEERTUBE_SOURCE_STORAGE_BACKEND", "local"),
		PeerTubeSourceStorageLocalRoot: getEnv("PEERTUBE_SOURCE_STORAGE_LOCAL_ROOT", ""),
		PeerTubeSourceS3Endpoint:       getEnv("PEERTUBE_SOURCE_S3_ENDPOINT", ""),
		PeerTubeSourceS3Bucket:         getEnv("PEERTUBE_SOURCE_S3_BUCKET", ""),
		PeerTubeSourceS3AccessKey:      getEnv("PEERTUBE_SOURCE_S3_ACCESS_KEY", ""),
		PeerTubeSourceS3SecretKey:      getEnv("PEERTUBE_SOURCE_S3_SECRET_KEY", ""),
		PeerTubeSourceS3Region:         getEnv("PEERTUBE_SOURCE_S3_REGION", ""),
		PeerTubeSourceS3UseSSL:         getEnvBool("PEERTUBE_SOURCE_S3_USE_SSL", true),
		PeerTubeSourceS3ForcePathStyle: getEnvBool("PEERTUBE_SOURCE_S3_FORCE_PATH_STYLE", false),
		PeerTubeImportConflictPolicy:   strings.ToLower(getEnv("PEERTUBE_IMPORT_CONFLICT_POLICY", "skip")),
		PeerTubeImportMediaMode:        strings.ToLower(getEnv("PEERTUBE_IMPORT_MEDIA_MODE", "copy")),
	}

	port, err := getEnvInt("HTTP_PORT", 8080)
	if err != nil {
		return nil, err
	}
	cfg.HTTPPort = port

	reqs, err := getEnvInt("RATE_LIMIT_REQUESTS", 120)
	if err != nil {
		return nil, err
	}
	cfg.RateLimitRequests = reqs

	authReqs, err := getEnvInt("AUTH_RATE_LIMIT_REQUESTS", 10)
	if err != nil {
		return nil, err
	}
	cfg.AuthRateLimitRequests = authReqs

	attachReqs, err := getEnvInt("ATTACHMENT_UPLOAD_RATE_LIMIT_REQUESTS", 60)
	if err != nil {
		return nil, err
	}
	cfg.AttachmentUploadRateLimitRequests = attachReqs
	cfg.AttachmentUploadRateLimitWindow = getEnvDuration("ATTACHMENT_UPLOAD_RATE_LIMIT_WINDOW", 10*time.Minute)

	smtpPort, err := getEnvInt("SMTP_PORT", 587)
	if err != nil {
		return nil, err
	}
	cfg.SMTPPort = smtpPort

	ytdlpHeight, err := getEnvInt("YTDLP_MAX_HEIGHT", 1080)
	if err != nil {
		return nil, err
	}
	cfg.YtdlpMaxHeight = ytdlpHeight

	channelSyncMax, err := getEnvInt("CHANNEL_SYNC_MAX_PER_USER", 5)
	if err != nil {
		return nil, err
	}
	cfg.ChannelSyncMaxPerUser = channelSyncMax

	channelSyncBatch, err := getEnvInt("CHANNEL_SYNC_BATCH", 15)
	if err != nil {
		return nil, err
	}
	cfg.ChannelSyncBatch = channelSyncBatch

	maxActiveUploads, err := getEnvInt("UPLOAD_MAX_ACTIVE_SESSIONS_PER_USER", 5)
	if err != nil {
		return nil, err
	}
	cfg.UploadMaxActiveSessionsPerUser = maxActiveUploads

	quotaBytes, err := getEnvInt64("INSTANCE_DEFAULT_QUOTA_BYTES", 0)
	if err != nil {
		return nil, err
	}
	cfg.InstanceDefaultQuotaBytes = quotaBytes

	pinConcurrency, err := getEnvInt("IPFS_PIN_CONCURRENCY", 2)
	if err != nil {
		return nil, err
	}
	cfg.IPFSPinConcurrency = pinConcurrency

	// Search client per-endpoint deadline overrides (ms; 0 = client defaults).
	searchSuggestMS, err := getEnvInt("SEARCH_SUGGEST_TIMEOUT_MS", 0)
	if err != nil {
		return nil, err
	}
	cfg.SearchSuggestTimeoutMS = searchSuggestMS
	searchQueryMS, err := getEnvInt("SEARCH_QUERY_TIMEOUT_MS", 0)
	if err != nil {
		return nil, err
	}
	cfg.SearchQueryTimeoutMS = searchQueryMS
	searchRecsMS, err := getEnvInt("SEARCH_RECS_TIMEOUT_MS", 0)
	if err != nil {
		return nil, err
	}
	cfg.SearchRecsTimeoutMS = searchRecsMS

	// Private-tier worker tuning INHERITS the public defaults when unset (spec §2).
	cfg.IPFSPrivateAddTimeout = getEnvDuration("IPFS_PRIVATE_ADD_TIMEOUT", cfg.IPFSAddTimeout)
	privatePinConcurrency, err := getEnvInt("IPFS_PRIVATE_PIN_CONCURRENCY", pinConcurrency)
	if err != nil {
		return nil, err
	}
	cfg.IPFSPrivatePinConcurrency = privatePinConcurrency

	for _, name := range splitAndTrim(getEnv("OAUTH_PROVIDERS", "")) {
		prefix := "OAUTH_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		cfg.OAuthProviders = append(cfg.OAuthProviders, OAuthProviderConfig{
			Name:         name,
			IssuerURL:    strings.TrimRight(getEnv(prefix+"_ISSUER", ""), "/"),
			ClientID:     getEnv(prefix+"_CLIENT_ID", ""),
			ClientSecret: getEnv(prefix+"_CLIENT_SECRET", ""),
			Scopes:       splitAndTrim(getEnv(prefix+"_SCOPES", "")),
		})
	}

	// The TOTP issuer label defaults to the instance name (what authenticator
	// apps display); it cannot reference InstanceName inside the literal above.
	if cfg.TOTPIssuer == "" {
		cfg.TOTPIssuer = cfg.InstanceName
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	switch c.Environment {
	case "development", "test", "production":
	default:
		return fmt.Errorf("config: invalid VIDRA_ENV %q (want development|test|production)", c.Environment)
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: invalid LOG_LEVEL %q (want debug|info|warn|error)", c.LogLevel)
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		return fmt.Errorf("config: invalid LOG_FORMAT %q (want json|text)", c.LogFormat)
	}
	if c.OTelEnabled {
		switch c.OTelExporterProtocol {
		case "grpc", "http/protobuf":
		default:
			return fmt.Errorf("config: invalid OTEL_EXPORTER_OTLP_PROTOCOL %q (want grpc|http/protobuf)", c.OTelExporterProtocol)
		}
		if strings.TrimSpace(c.OTelExporterEndpoint) == "" {
			return fmt.Errorf("config: OTEL_EXPORTER_OTLP_ENDPOINT is required when OTEL_ENABLED is true")
		}
	}
	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		return fmt.Errorf("config: HTTP_PORT %d out of range", c.HTTPPort)
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return fmt.Errorf("config: DATABASE_URL is required")
	}
	if strings.TrimSpace(c.RedisURL) == "" {
		return fmt.Errorf("config: REDIS_URL is required")
	}
	if c.HTTPRequestTimeout <= 0 {
		return fmt.Errorf("config: HTTP_REQUEST_TIMEOUT must be positive")
	}
	if _, err := bytes.Parse(c.HTTPBodyLimit); err != nil {
		return fmt.Errorf("config: invalid HTTP_BODY_LIMIT %q: %w", c.HTTPBodyLimit, err)
	}
	if c.RateLimitEnabled {
		if c.RateLimitRequests <= 0 {
			return fmt.Errorf("config: RATE_LIMIT_REQUESTS must be positive when rate limiting is enabled")
		}
		if c.RateLimitWindow <= 0 {
			return fmt.Errorf("config: RATE_LIMIT_WINDOW must be positive when rate limiting is enabled")
		}
		if c.AuthRateLimitRequests <= 0 {
			return fmt.Errorf("config: AUTH_RATE_LIMIT_REQUESTS must be positive when rate limiting is enabled")
		}
		if c.AttachmentUploadRateLimitRequests <= 0 {
			return fmt.Errorf("config: ATTACHMENT_UPLOAD_RATE_LIMIT_REQUESTS must be positive when rate limiting is enabled")
		}
		if c.AttachmentUploadRateLimitWindow <= 0 {
			return fmt.Errorf("config: ATTACHMENT_UPLOAD_RATE_LIMIT_WINDOW must be positive when rate limiting is enabled")
		}
	}
	if c.JWTAccessTTL <= 0 {
		return fmt.Errorf("config: JWT_ACCESS_TTL must be positive")
	}
	if c.JWTRefreshTTL <= 0 {
		return fmt.Errorf("config: JWT_REFRESH_TTL must be positive")
	}
	switch c.StorageBackend {
	case "local":
		if strings.TrimSpace(c.StorageLocalRoot) == "" {
			return fmt.Errorf("config: STORAGE_LOCAL_ROOT is required for the local storage backend")
		}
	case "s3":
		if strings.TrimSpace(c.StorageS3Endpoint) == "" {
			return fmt.Errorf("config: STORAGE_S3_ENDPOINT is required for the s3 storage backend")
		}
		if strings.Contains(c.StorageS3Endpoint, "://") {
			return fmt.Errorf("config: STORAGE_S3_ENDPOINT must be host[:port] without a scheme (got %q); use STORAGE_S3_USE_SSL to pick http/https", c.StorageS3Endpoint)
		}
		if strings.TrimSpace(c.StorageS3Bucket) == "" {
			return fmt.Errorf("config: STORAGE_S3_BUCKET is required for the s3 storage backend")
		}
		if strings.TrimSpace(c.StorageS3AccessKey) == "" {
			return fmt.Errorf("config: STORAGE_S3_ACCESS_KEY is required for the s3 storage backend")
		}
		if strings.TrimSpace(c.StorageS3SecretKey) == "" {
			return fmt.Errorf("config: STORAGE_S3_SECRET_KEY is required for the s3 storage backend")
		}
	default:
		return fmt.Errorf("config: unsupported STORAGE_BACKEND %q (want local|s3)", c.StorageBackend)
	}
	if _, err := bytes.Parse(c.UploadMaxSize); err != nil {
		return fmt.Errorf("config: invalid UPLOAD_MAX_SIZE %q: %w", c.UploadMaxSize, err)
	}
	if c.UploadMaxActiveSessionsPerUser < 0 {
		return fmt.Errorf("config: UPLOAD_MAX_ACTIVE_SESSIONS_PER_USER must be >= 0 (0 = unlimited), got %d", c.UploadMaxActiveSessionsPerUser)
	}
	if c.InstanceDefaultQuotaBytes < 0 {
		return fmt.Errorf("config: INSTANCE_DEFAULT_QUOTA_BYTES must be >= 0 (0 = unlimited), got %d", c.InstanceDefaultQuotaBytes)
	}
	if c.Environment == "production" {
		if c.JWTSecret == devJWTSecret {
			return fmt.Errorf("config: JWT_SECRET must be set in production (the dev default is not allowed)")
		}
		if len(c.JWTSecret) < 32 {
			return fmt.Errorf("config: JWT_SECRET must be at least 32 bytes in production")
		}
	}
	if c.Environment == "production" {
		for _, o := range c.CORSAllowedOrigins {
			if o == "*" {
				return fmt.Errorf("config: wildcard CORS origin is not allowed in production")
			}
		}
	}
	if c.MailEnabled {
		if strings.TrimSpace(c.SMTPHost) == "" {
			return fmt.Errorf("config: SMTP_HOST is required when MAIL_ENABLED=true")
		}
		if c.SMTPPort < 1 || c.SMTPPort > 65535 {
			return fmt.Errorf("config: SMTP_PORT %d out of range", c.SMTPPort)
		}
		from := strings.TrimSpace(c.SMTPFrom)
		if from == "" {
			return fmt.Errorf("config: SMTP_FROM is required when MAIL_ENABLED=true")
		}
		if !strings.Contains(from, "@") || strings.ContainsAny(from, "\r\n") {
			return fmt.Errorf("config: SMTP_FROM must be a plain email address")
		}
	}
	if c.MalwareScanEnabled && strings.TrimSpace(c.ClamAVAddr) == "" {
		return fmt.Errorf("config: CLAMAV_ADDR is required when MALWARE_SCAN_ENABLED=true")
	}
	if c.MalwareScanEnabled && c.ClamAVTimeout <= 0 {
		return fmt.Errorf("config: CLAMAV_TIMEOUT must be a positive duration when MALWARE_SCAN_ENABLED=true")
	}
	switch c.MalwareScanMode {
	case "", "fail-closed", "fail-open", "quarantine": // "" = default fail-closed
	default:
		return fmt.Errorf("config: MALWARE_SCAN_MODE %q must be one of fail-closed, fail-open, quarantine", c.MalwareScanMode)
	}
	if c.TranscodingAV1Enabled {
		return fmt.Errorf("config: TRANSCODING_AV1_ENABLED is not supported yet — AV1 transcoding is deferred (see fix_plan P6.3); leave it false")
	}
	if c.WhisperEnabled {
		u, err := url.Parse(c.WhisperEndpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("config: WHISPER_ENDPOINT must be a valid http(s) URL when WHISPER_ENABLED=true")
		}
	}
	if c.WhisperDefaultLanguage != "" && !languageTag.MatchString(c.WhisperDefaultLanguage) {
		return fmt.Errorf("config: WHISPER_DEFAULT_LANGUAGE %q must be a BCP-47-ish language tag (e.g. en, pt-BR)", c.WhisperDefaultLanguage)
	}
	// vidra-search integration (search-service W4): validate the URL shape when
	// set, and require a strong shared secret in production.
	if c.SearchServiceURL != "" {
		u, err := url.Parse(c.SearchServiceURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("config: SEARCH_SERVICE_URL must be a valid http(s) URL when set")
		}
		if c.Environment == "production" && len(c.SearchInternalSecret) < 32 {
			return fmt.Errorf("config: SEARCH_INTERNAL_SECRET must be at least 32 characters when SEARCH_SERVICE_URL is set in production")
		}
	}
	// yt-dlp platform-URL import: validate the combination only when enabled so a
	// default (disabled) deployment never trips these. YTDLP_MAX_HEIGHT is bounded
	// even when disabled (a nonsensical value is always a config error).
	if c.YtdlpMaxHeight < 0 || c.YtdlpMaxHeight > 4320 {
		return fmt.Errorf("config: YTDLP_MAX_HEIGHT %d out of range (0 = no cap, else 144..4320)", c.YtdlpMaxHeight)
	}
	if c.YtdlpImportEnabled {
		if strings.TrimSpace(c.YtdlpPath) == "" {
			return fmt.Errorf("config: YTDLP_PATH is required when YTDLP_IMPORT_ENABLED=true")
		}
		if c.YtdlpTimeout <= 0 {
			return fmt.Errorf("config: YTDLP_TIMEOUT must be a positive duration when YTDLP_IMPORT_ENABLED=true")
		}
		if c.YtdlpProxy != "" {
			u, perr := url.Parse(c.YtdlpProxy)
			if perr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") {
				return fmt.Errorf("config: YTDLP_PROXY must be an http(s) or socks5 URL when set")
			}
		}
	}
	// Channel auto-sync bounds (W2.C4). Validated only when enabled so a default
	// deployment never trips them; the effective on/off state is
	// ChannelSyncEnabled AND YtdlpImportEnabled (checked/logged at wiring time).
	if c.ChannelSyncEnabled {
		if c.ChannelSyncInterval <= 0 {
			return fmt.Errorf("config: CHANNEL_SYNC_INTERVAL must be a positive duration when CHANNEL_SYNC_ENABLED=true")
		}
		if c.ChannelSyncBatch < 1 || c.ChannelSyncBatch > 100 {
			return fmt.Errorf("config: CHANNEL_SYNC_BATCH %d out of range (1..100)", c.ChannelSyncBatch)
		}
		if c.ChannelSyncCooldown < 0 {
			return fmt.Errorf("config: CHANNEL_SYNC_COOLDOWN must not be negative")
		}
	}
	if c.MFAKeyKEK != "" {
		if k, err := base64.StdEncoding.DecodeString(c.MFAKeyKEK); err != nil || len(k) != 32 {
			return fmt.Errorf("config: MFA_KEY_KEK must be base64 of exactly 32 bytes")
		}
	}
	if err := c.validateOAuth(); err != nil {
		return err
	}
	if c.FederationEnabled {
		u, err := url.Parse(c.PublicBaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("config: PUBLIC_BASE_URL must be a valid http(s) origin when FEDERATION_ENABLED=true")
		}
		if u.Path != "" && u.Path != "/" {
			return fmt.Errorf("config: PUBLIC_BASE_URL must not include a path (got %q)", c.PublicBaseURL)
		}
		if c.Environment == "production" && u.Scheme != "https" {
			return fmt.Errorf("config: PUBLIC_BASE_URL must be https in production")
		}
		if c.FederationKeyKEK != "" {
			if k, err := base64.StdEncoding.DecodeString(c.FederationKeyKEK); err != nil || len(k) != 32 {
				return fmt.Errorf("config: FEDERATION_KEY_KEK must be base64 of exactly 32 bytes")
			}
		} else if c.Environment == "production" {
			return fmt.Errorf("config: FEDERATION_KEY_KEK is required in production when FEDERATION_ENABLED=true")
		}
	}
	if c.ATProtoKeyKEK != "" {
		if k, err := base64.StdEncoding.DecodeString(c.ATProtoKeyKEK); err != nil || len(k) != 32 {
			return fmt.Errorf("config: ATPROTO_KEY_KEK must be base64 of exactly 32 bytes")
		}
	}
	if c.ATProtoEnabled && c.Environment == "production" && c.ATProtoKEK() == "" {
		return fmt.Errorf("config: ATPROTO_KEY_KEK (or FEDERATION_KEY_KEK) is required in production when ATPROTO_ENABLED=true")
	}
	if err := c.validatePeerTubeImport(); err != nil {
		return err
	}
	if err := c.validateIPFS(); err != nil {
		return err
	}
	return nil
}

// validateIPFS checks the hybrid IPFS media-mirroring (P19 + P19.P) configuration.
// Every value is OFF/inert by default; the PUBLIC-tier checks only bite once
// IPFS_ENABLED is set, but the PRIVATE-tier guard fires whenever IPFS_MIRROR_PRIVATE
// is true (the private tier may run standalone with IPFS_ENABLED=false — an operator
// may replicate only non-public media). The private guard is the config-level
// enforcement of the spec §1/§2 invariant: non-public media is mirrored ONLY to a
// dedicated, separate private-swarm node — never the public node, never dual-homed.
func (c *Config) validateIPFS() error {
	if err := c.validateIPFSPrivate(); err != nil {
		return err
	}
	if !c.IPFSEnabled {
		return nil
	}
	if strings.TrimSpace(c.IPFSAPIURL) == "" || strings.TrimSpace(c.IPFSGatewayURL) == "" {
		return fmt.Errorf("config: IPFS_API_URL and IPFS_GATEWAY_URL are required when IPFS_ENABLED")
	}
	for label, raw := range map[string]string{"IPFS_API_URL": c.IPFSAPIURL, "IPFS_GATEWAY_URL": c.IPFSGatewayURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("config: %s must be a valid http(s) URL when IPFS_ENABLED=true", label)
		}
	}
	if c.IPFSClusterAPIURL != "" {
		u, err := url.Parse(c.IPFSClusterAPIURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("config: IPFS_CLUSTER_API_URL must be a valid http(s) URL")
		}
	}
	if c.IPFSAddTimeout <= 0 {
		return fmt.Errorf("config: IPFS_ADD_TIMEOUT must be positive")
	}
	if c.IPFSPinConcurrency < 1 {
		return fmt.Errorf("config: IPFS_PIN_CONCURRENCY must be >= 1")
	}
	if c.IPFSReconcileInterval <= 0 {
		return fmt.Errorf("config: IPFS_RECONCILE_INTERVAL must be positive")
	}
	return nil
}

// validateIPFSPrivate enforces the private-tier (P19.P) config shape (spec §2). It
// SUPERSEDES the shipped P19 guard (IPFS_MIRROR_PRIVATE ⇒ IPFS_CLUSTER_API_URL): the
// real requirement is a DEDICATED private-swarm node, not merely a cluster URL. Runs
// regardless of IPFS_ENABLED because the private tier may be operated standalone.
func (c *Config) validateIPFSPrivate() error {
	privateURL := strings.TrimSpace(c.IPFSPrivateAPIURL)
	// The master opt-in requires a dedicated private-swarm kubo RPC.
	if c.IPFSMirrorPrivate && privateURL == "" {
		return fmt.Errorf("config: IPFS_MIRROR_PRIVATE requires IPFS_PRIVATE_API_URL (a dedicated private-swarm kubo — non-public media is never mirrored to the public network)")
	}
	if privateURL == "" {
		return nil
	}
	// Never dual-home: the private node must be a DIFFERENT node from the public
	// mirror. Equality is compared on the trimmed, slash-normalized values both
	// fields already store, so a trailing-slash difference cannot sneak past.
	if privateURL == strings.TrimSpace(c.IPFSAPIURL) {
		return fmt.Errorf("config: the private IPFS mirror must be a separate node from the public mirror (refusing to dual-home)")
	}
	if !isHTTPURL(privateURL) {
		return fmt.Errorf("config: IPFS_PRIVATE_API_URL must be a valid http(s) URL")
	}
	if c.IPFSPrivateClusterAPIURL != "" && !isHTTPURL(c.IPFSPrivateClusterAPIURL) {
		return fmt.Errorf("config: IPFS_PRIVATE_CLUSTER_API_URL must be a valid http(s) URL")
	}
	if c.IPFSMirrorPrivate {
		if c.IPFSPrivateAddTimeout <= 0 {
			return fmt.Errorf("config: IPFS_PRIVATE_ADD_TIMEOUT must be positive")
		}
		if c.IPFSPrivatePinConcurrency < 1 {
			return fmt.Errorf("config: IPFS_PRIVATE_PIN_CONCURRENCY must be >= 1")
		}
	}
	return nil
}

// isHTTPURL reports whether raw parses as an absolute http(s) URL with a host.
func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

// validatePeerTubeImport checks the PeerTube import (P18) configuration. The
// conflict policy and source storage backend are always validated (they have
// defaults); the source DSN + S3 credentials are only required when the admin
// import API is enabled — the CLI supplies its own source flags.
func (c *Config) validatePeerTubeImport() error {
	switch c.PeerTubeImportConflictPolicy {
	case "", "skip", "rename", "merge", "fail": // "" = default skip (Load defaults it)
	default:
		return fmt.Errorf("config: PEERTUBE_IMPORT_CONFLICT_POLICY %q must be one of skip, rename, merge, fail", c.PeerTubeImportConflictPolicy)
	}
	switch c.PeerTubeImportMediaMode {
	case "", "copy", "reference", "none": // "" = default copy (Load defaults it)
	default:
		return fmt.Errorf("config: PEERTUBE_IMPORT_MEDIA_MODE %q must be one of copy, reference, none", c.PeerTubeImportMediaMode)
	}
	switch c.PeerTubeSourceStorageBackend {
	case "", "local", "s3": // "" = default local (Load defaults it)
	default:
		return fmt.Errorf("config: PEERTUBE_SOURCE_STORAGE_BACKEND %q must be local or s3", c.PeerTubeSourceStorageBackend)
	}
	if strings.Contains(c.PeerTubeSourceS3Endpoint, "://") {
		return fmt.Errorf("config: PEERTUBE_SOURCE_S3_ENDPOINT must be host[:port] without a scheme (got %q)", c.PeerTubeSourceS3Endpoint)
	}
	if !c.PeerTubeImportEnabled {
		return nil
	}
	// The admin import path needs a source to read from.
	if strings.TrimSpace(c.PeerTubeSourceDatabaseURL) == "" {
		return fmt.Errorf("config: PEERTUBE_SOURCE_DATABASE_URL is required when PEERTUBE_IMPORT_ENABLED=true")
	}
	if c.PeerTubeImportMediaMode != "reference" && c.PeerTubeImportMediaMode != "none" && c.PeerTubeSourceStorageBackend == "s3" {
		if strings.TrimSpace(c.PeerTubeSourceS3Endpoint) == "" || strings.TrimSpace(c.PeerTubeSourceS3Bucket) == "" {
			return fmt.Errorf("config: PEERTUBE_SOURCE_S3_ENDPOINT and PEERTUBE_SOURCE_S3_BUCKET are required when the source storage backend is s3")
		}
	}
	return nil
}

// PeerTubeImportConfigured reports whether a source PeerTube database DSN is set
// — the minimum needed to run an import. The admin API surface mounts only when
// PeerTubeImportEnabled AND this is true.
func (c *Config) PeerTubeImportConfigured() bool {
	return c.PeerTubeImportEnabled && strings.TrimSpace(c.PeerTubeSourceDatabaseURL) != ""
}

// SearchServiceEnabled reports whether the vidra-search integration is wired
// (SEARCH_SERVICE_URL set). When false, every search surface degrades to local
// behaviour and no search client or outbox/reconcile worker is constructed.
func (c *Config) SearchServiceEnabled() bool {
	return c.SearchServiceURL != ""
}

// oauthProviderName constrains provider names to URL-path- and env-var-safe
// identifiers: lowercase letters/digits, dash-separated.
var oauthProviderName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// languageTag is a permissive BCP-47-ish caption language tag (mirrors the
// pattern internal/video enforces on caption languages), used to validate
// WHISPER_DEFAULT_LANGUAGE at load time.
var languageTag = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)

// validateOAuth checks the configured OIDC providers. Providers are disabled
// by default (no OAUTH_PROVIDERS); each configured provider must be complete,
// and PUBLIC_BASE_URL must be set because the callback redirect URI is derived
// from it server-side (never from request parameters — P15).
func (c *Config) validateOAuth() error {
	if len(c.OAuthProviders) == 0 {
		return nil
	}
	u, err := url.Parse(c.PublicBaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("config: PUBLIC_BASE_URL must be a valid http(s) origin when OAUTH_PROVIDERS is set (OAuth redirect URIs derive from it)")
	}
	if c.Environment == "production" && u.Scheme != "https" {
		return fmt.Errorf("config: PUBLIC_BASE_URL must be https in production")
	}
	seen := map[string]bool{}
	for _, p := range c.OAuthProviders {
		if !oauthProviderName.MatchString(p.Name) {
			return fmt.Errorf("config: invalid OAuth provider name %q (want lowercase letters/digits/dashes)", p.Name)
		}
		if seen[p.Name] {
			return fmt.Errorf("config: duplicate OAuth provider %q in OAUTH_PROVIDERS", p.Name)
		}
		seen[p.Name] = true
		envName := strings.ToUpper(strings.ReplaceAll(p.Name, "-", "_"))
		iss, err := url.Parse(p.IssuerURL)
		if err != nil || iss.Host == "" || (iss.Scheme != "http" && iss.Scheme != "https") {
			return fmt.Errorf("config: OAUTH_%s_ISSUER must be a valid http(s) URL", envName)
		}
		if c.Environment == "production" && iss.Scheme != "https" {
			return fmt.Errorf("config: OAUTH_%s_ISSUER must be https in production", envName)
		}
		if strings.TrimSpace(p.ClientID) == "" {
			return fmt.Errorf("config: OAUTH_%s_CLIENT_ID is required", envName)
		}
		if strings.TrimSpace(p.ClientSecret) == "" {
			return fmt.Errorf("config: OAUTH_%s_CLIENT_SECRET is required", envName)
		}
	}
	return nil
}

// OAuthProviderNames returns the configured provider names in configuration
// order (the order OAUTH_PROVIDERS lists them), for the public instance
// document. Never nil.
func (c *Config) OAuthProviderNames() []string {
	names := make([]string, 0, len(c.OAuthProviders))
	for _, p := range c.OAuthProviders {
		names = append(names, p.Name)
	}
	return names
}

// HTTPAddr returns the host:port the HTTP server should bind to.
func (c *Config) HTTPAddr() string {
	return fmt.Sprintf("%s:%d", c.HTTPHost, c.HTTPPort)
}

// CookieSecure reports whether auth cookies (the vidra_refresh cookie set by
// cookie-mode sessions) must carry the Secure attribute. True when the
// instance's canonical public origin is https (PUBLIC_BASE_URL), and always in
// production — fail-secure even when PUBLIC_BASE_URL is unset, since production
// deployments are expected to terminate TLS. Plain-http local development keeps
// Secure off so the cookie still works on http://localhost.
func (c *Config) CookieSecure() bool {
	if strings.HasPrefix(strings.ToLower(c.PublicBaseURL), "https://") {
		return true
	}
	return c.Environment == "production"
}

// ATProtoKEK returns the key-encryption key that seals linked Bluesky app
// passwords at rest: ATPROTO_KEY_KEK when set, otherwise FEDERATION_KEY_KEK (so a
// deployment already running a federation KEK covers ATProto without a second
// secret). Empty means no sealing (dev-only; cmd/api warns loudly).
func (c *Config) ATProtoKEK() string {
	if c.ATProtoKeyKEK != "" {
		return c.ATProtoKeyKEK
	}
	return c.FederationKeyKEK
}

// MFAKEK returns the key-encryption key that seals TOTP secrets at rest:
// MFA_KEY_KEK when set, otherwise FEDERATION_KEY_KEK (so a deployment already
// running a federation KEK covers MFA without a second secret). Empty means no
// sealing (dev-only; cmd/api warns loudly).
func (c *Config) MFAKEK() string {
	if c.MFAKeyKEK != "" {
		return c.MFAKeyKEK
	}
	return c.FederationKeyKEK
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	return n, nil
}

func getEnvInt64(key string, def int64) (int64, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	return n, nil
}

func getEnvBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
