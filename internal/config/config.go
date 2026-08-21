// Package config loads and validates Vidra backend configuration from the
// environment. Configuration is the single source of truth for runtime wiring;
// no other package should read os.Getenv directly.
//
// It is also the ONLY validation engine: Load boots from the process
// environment, while LoadFrom / CheckEnv run the same parsing and validation
// against candidate values (a generated env file, a wizard's answers) so setup
// tooling can never drift from what actually boots.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
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
	// HTTPStreamRequestTimeout is the deadline used INSTEAD of HTTPRequestTimeout
	// on the byte-streaming routes: resumable chunk PUTs, the direct upload /
	// replace POSTs, DM attachment uploads, and the media GETs (thumbnails, HLS,
	// originals, downloads). Those requests are bounded by the client's link
	// speed, not by server work — an 8 MiB chunk needs a sustained ~2.2 Mbit/s to
	// finish inside 30s, so the general deadline makes a typical residential or
	// mobile upload retry-loop forever, and cuts long downloads mid-stream.
	// A longer deadline is used rather than NO deadline so a stalled peer still
	// releases its goroutine and pooled DB connection eventually. The default 1h
	// covers a 2 GiB UPLOAD_MAX_SIZE at ~4.7 Mbit/s.
	HTTPStreamRequestTimeout time.Duration
	// HTTPBodyLimit is the maximum accepted request body size, as an Echo size
	// string (e.g. "8M", "512K"). Oversized requests are rejected with 413.
	HTTPBodyLimit string

	// TrustedProxyCIDRs are EXTRA networks whose X-Forwarded-For the server
	// believes, on top of the built-in loopback + RFC1918/ULA + link-local set
	// (see internal/httpapi.New, which explains why the walk stops at the nearest
	// untrusted hop). Comma-separated CIDRs, empty by default.
	//
	// It exists for one topology: a TLS terminator on a PUBLIC address — a cloud
	// load balancer, a CDN, an operator-run nginx on another host
	// (VIDRA_TLS_MODE=external). Such a hop is untrusted by default, which is
	// fail-SAFE rather than fail-open — the proxy itself becomes the client for
	// every per-IP budget on the instance, so one visitor's login attempts spend
	// everybody's — but it is still wrong, and a CIDR is the only honest way to
	// say "that address really is our edge". Naming a network that is NOT under
	// the operator's control hands every attacker behind it the ability to forge
	// their own client address, so this stays empty unless somebody means it.
	//
	// Each entry must parse as a CIDR (validate() refuses otherwise): a bare IP
	// with no prefix length is the typo that would silently trust nothing.
	TrustedProxyCIDRs []string

	// PostgreSQL connection (DSN form, e.g. postgres://user:pass@host:5432/db).
	DatabaseURL string

	// ExternalPostgres declares that DATABASE_URL points at a database this
	// deployment does NOT run — a managed instance, somebody else's server —
	// rather than the compose-network postgres. It is DISPLAY-ONLY: nothing in
	// the api may key behaviour on it, and nothing does. It exists because the
	// admin infrastructure page has to tell an operator where their backups come
	// from, and the two answers are opposite advice ("the nightly pg_dump this
	// stack writes" vs "your provider's snapshots — check they are on"). The DSN
	// cannot be inspected for the difference: a managed database and a
	// self-hosted one on another host look identical, and reading the host out
	// of a DSN to guess would be wrong precisely on the deployments that matter.
	// So the operator says which it is.
	//
	// It is read with isShellTrue, NOT the typed-bool parser: the deploy shell
	// scripts read the same variable, `yes` is a spelling they and setup.IsTrue
	// both accept, and strconv.ParseBool does not — so the ordinary parser would
	// refuse to BOOT an instance over the spelling of a variable that governs one
	// paragraph of display copy. Nothing here is ever a config error.
	ExternalPostgres bool

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

	// AllowPlainHTTP is the operator's EXPLICIT consent to a plain-http public
	// origin in production — the lab, LAN and air-gapped deployments phase-1 item
	// 12 exists for, where there is no certificate to be had and no browser on the
	// public internet to protect.
	//
	// It is a separate variable rather than an inference from the scheme because
	// the two mistakes are not symmetrical. A production deployment that MEANT to
	// be https and typed http:// gets Secure cookies dropped and HSTS withheld —
	// silently, on the one deployment where those matter most — and the only way
	// to tell that apart from a deliberate lab install is to make somebody say so.
	// So an http:// origin in production without this is a refusal to boot (see
	// validate()), and with it every https-derived default flips off together:
	// PublicOriginIsHTTPS is the one predicate CookieSecure and HSTS both read.
	AllowPlainHTTP bool

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

	// TranscodingStreamOutput writes the HLS ladder straight into the blob store
	// through a loopback signing sidecar instead of staging it on scratch and
	// uploading afterwards. Peak transcode disk drops to roughly the progressive
	// MP4s alone, but the pipeline reads its own output back to build those
	// downloads, so those reads become object-store range requests: this trades
	// scratch disk for egress. Default false — right for a small-disk host with
	// cheap or CDN-fronted egress (Backblaze B2 via the Bandwidth Alliance),
	// wrong where egress is metered and disk is plentiful. Only meaningful with
	// TranscodingEnabled and a non-local storage backend.
	TranscodingStreamOutput bool

	// TranscodingMinFreeScratchMB is the free-space floor (MiB) on the transcode
	// scratch filesystem below which the worker claims no new jobs. A transcode
	// writes several times its source before anything is uploaded, and on the
	// single-disk deployment that volume shares a filesystem with the Postgres
	// data directory — so filling it does not merely fail a job, it stops the
	// database accepting writes. 0 uses the built-in default (10 GiB).
	TranscodingMinFreeScratchMB int

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

	// OwnerClaimToken pins the first-run owner-claim token (0104) to this FIXED
	// value instead of the high-entropy random minted at boot, so test harnesses
	// and local dev can claim the owner (admin) account deterministically without
	// scraping the boot log. DEVELOPMENT/TEST-ONLY: a fixed, environment-visible
	// admin-bootstrap credential defeats the point of the random token, so
	// production refuses to boot when it is set. Empty (default) keeps the random
	// mint; at least 16 characters when set.
	OwnerClaimToken string

	// Rate limiting (Redis fixed-window) applied to the /api surface.
	RateLimitEnabled  bool
	RateLimitRequests int
	RateLimitWindow   time.Duration
	// AuthRateLimitRequests is a stricter per-IP budget applied (over the same
	// RateLimitWindow) to the sensitive auth endpoints — login, register, and the
	// password-reset / email-verify confirmations — to throttle credential
	// stuffing and token guessing. Gated by RateLimitEnabled.
	AuthRateLimitRequests int
	// MediaRateLimitRequests is a much larger per-IP budget applied (over the same
	// RateLimitWindow) to the media GETs — thumbnails, HLS playlists/segments,
	// storyboards, downloads, avatars/banners. One home page is a single feed call
	// plus ~20 thumbnails, and HLS playback adds ~10 segments/minute on top, so a
	// single ordinary viewer blows the general RateLimitRequests budget inside
	// their first minute. These are cheap, cacheable blob reads: they need a
	// ceiling against scraping, not the API budget. Gated by RateLimitEnabled.
	MediaRateLimitRequests int
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

// DefaultDatabaseURL is the development fallback for DATABASE_URL (the local
// Compose postgres). Exported because cmd/api's `migrate` subcommand must
// resolve the destination database EXACTLY as the server does — a migrator that
// defaults somewhere else than the api is a data-loss bug — while deliberately
// skipping the server's unrelated runtime validation.
const DefaultDatabaseURL = "postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable"

// Load reads configuration from the process environment, applying safe
// development defaults. It returns an error if a required value is missing or
// malformed.
//
// Required in production: DATABASE_URL, REDIS_URL. In development they default
// to local Docker Compose service addresses.
func Load() (*Config, error) {
	return LoadFrom(os.LookupEnv)
}

// LoadFrom is Load against an arbitrary environment source: lookup answers one
// variable at a time, reporting ok=false when it is not set. It exists so the
// setup engine, wizard, and doctor can validate CANDIDATE values — a generated
// env file, a half-finished wizard answer set — with EXACTLY the code that
// boots the process, instead of a parallel validation library that drifts from
// it (see docs/productionization/interfaces.md §1). CheckEnv is the map form.
func LoadFrom(lookup func(key string) (string, bool)) (*Config, error) {
	// Every read below — plain string and typed alike — goes through p, so it
	// sees the candidate environment and nothing else. Typed getters record
	// malformed values on p; checked via p.Err() below before semantic
	// validation so every bad variable is reported at once.
	p := &envParser{lookup: lookup}
	getEnv := p.Str

	env := getEnv("VIDRA_ENV", "development")

	cfg := &Config{
		Environment:                    env,
		LogLevel:                       strings.ToLower(getEnv("LOG_LEVEL", "info")),
		LogFormat:                      strings.ToLower(getEnv("LOG_FORMAT", "json")),
		OTelEnabled:                    p.Bool("OTEL_ENABLED", false),
		OTelExporterEndpoint:           getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTelExporterProtocol:           strings.ToLower(getEnv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")),
		OTelServiceName:                getEnv("OTEL_SERVICE_NAME", "vidra-core"),
		MetricsEnabled:                 p.Bool("METRICS_ENABLED", false),
		HTTPHost:                       getEnv("HTTP_HOST", "0.0.0.0"),
		InstanceName:                   getEnv("INSTANCE_NAME", "Vidra (dev)"),
		PublicBaseURL:                  strings.TrimRight(getEnv("PUBLIC_BASE_URL", ""), "/"),
		AllowPlainHTTP:                 p.Bool("VIDRA_ALLOW_PLAIN_HTTP", false),
		TrustedProxyCIDRs:              splitAndTrim(getEnv("TRUSTED_PROXY_CIDRS", "")),
		FederationEnabled:              p.Bool("FEDERATION_ENABLED", false),
		FederationKeyKEK:               getEnv("FEDERATION_KEY_KEK", ""),
		ATProtoEnabled:                 p.Bool("ATPROTO_ENABLED", false),
		ATProtoKeyKEK:                  getEnv("ATPROTO_KEY_KEK", ""),
		ATProtoLoginEnabled:            p.Bool("ATPROTO_LOGIN_ENABLED", false),
		MFAKeyKEK:                      getEnv("MFA_KEY_KEK", ""),
		TOTPIssuer:                     getEnv("TOTP_ISSUER", ""),
		MalwareScanEnabled:             p.Bool("MALWARE_SCAN_ENABLED", false),
		ClamAVAddr:                     getEnv("CLAMAV_ADDR", ""),
		ClamAVTimeout:                  p.Duration("CLAMAV_TIMEOUT", 60*time.Second),
		MalwareScanMode:                getEnv("MALWARE_SCAN_MODE", "fail-closed"),
		TranscodingEnabled:             p.Bool("TRANSCODING_ENABLED", true),
		TranscodingVP9Enabled:          p.Bool("TRANSCODING_VP9_ENABLED", false),
		TranscodingStreamOutput:        p.Bool("TRANSCODING_STREAM_OUTPUT", false),
		TranscodingMinFreeScratchMB:    p.Int("TRANSCODING_MIN_FREE_SCRATCH_MB", 0),
		TranscodingAV1Enabled:          p.Bool("TRANSCODING_AV1_ENABLED", false),
		TranscodeHoldTimeout:           p.Duration("TRANSCODE_HOLD_TIMEOUT", 12*time.Hour),
		WhisperEnabled:                 p.Bool("WHISPER_ENABLED", false),
		WhisperEndpoint:                strings.TrimRight(getEnv("WHISPER_ENDPOINT", ""), "/"),
		WhisperDefaultLanguage:         strings.TrimSpace(getEnv("WHISPER_DEFAULT_LANGUAGE", "en")),
		SearchServiceURL:               strings.TrimRight(getEnv("SEARCH_SERVICE_URL", ""), "/"),
		SearchInternalSecret:           getEnv("SEARCH_INTERNAL_SECRET", ""),
		SearchReconcileInterval:        p.Duration("SEARCH_RECONCILE_INTERVAL", 24*time.Hour),
		SearchHealthInterval:           p.Duration("SEARCH_HEALTH_INTERVAL", 15*time.Second),
		LiveRTMPURL:                    getEnv("LIVE_RTMP_URL", ""),
		LiveIngestSecret:               getEnv("LIVE_INGEST_SECRET", ""),
		LiveHLSRoot:                    strings.TrimRight(getEnv("LIVE_HLS_ROOT", ""), "/"),
		InstanceDescription:            getEnv("INSTANCE_DESCRIPTION", ""),
		InstanceTermsURL:               getEnv("INSTANCE_TERMS_URL", ""),
		InstancePrivacyURL:             getEnv("INSTANCE_PRIVACY_URL", ""),
		InstanceContactEmail:           getEnv("INSTANCE_CONTACT_EMAIL", ""),
		RegistrationEnabled:            p.Bool("REGISTRATION_ENABLED", true),
		RegistrationRequireApproval:    p.Bool("REGISTRATION_REQUIRE_APPROVAL", false),
		QuarantineNewUploads:           p.Bool("QUARANTINE_NEW_UPLOADS", false),
		UploadsEnabled:                 p.Bool("FEATURE_UPLOADS_ENABLED", true),
		ImportsEnabled:                 p.Bool("FEATURE_IMPORTS_ENABLED", true),
		LiveEnabled:                    p.Bool("FEATURE_LIVE_ENABLED", true),
		CommentsEnabled:                p.Bool("FEATURE_COMMENTS_ENABLED", true),
		MailEnabled:                    p.Bool("MAIL_ENABLED", false),
		SMTPHost:                       getEnv("SMTP_HOST", ""),
		SMTPUsername:                   getEnv("SMTP_USERNAME", ""),
		SMTPPassword:                   getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:                       getEnv("SMTP_FROM", ""),
		DevMailCaptureEnabled:          p.Bool("DEV_MAIL_CAPTURE_ENABLED", false),
		ImportAllowPrivateURLs:         p.Bool("HTTP_IMPORT_ALLOW_PRIVATE_URLS", false),
		OwnerClaimToken:                getEnv("OWNER_CLAIM_TOKEN", ""),
		DatabaseURL:                    getEnv("DATABASE_URL", DefaultDatabaseURL),
		ExternalPostgres:               isShellTrue(getEnv("VIDRA_EXTERNAL_POSTGRES", "")),
		RedisURL:                       getEnv("REDIS_URL", "redis://localhost:6379/0"),
		CORSAllowedOrigins:             splitAndTrim(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3000")),
		HTTPReadTimeout:                p.Duration("HTTP_READ_TIMEOUT", 15*time.Second),
		HTTPWriteTimeout:               p.Duration("HTTP_WRITE_TIMEOUT", 30*time.Second),
		HTTPShutdownTimeout:            p.Duration("HTTP_SHUTDOWN_TIMEOUT", 20*time.Second),
		HTTPRequestTimeout:             p.Duration("HTTP_REQUEST_TIMEOUT", 30*time.Second),
		HTTPStreamRequestTimeout:       p.Duration("HTTP_STREAM_REQUEST_TIMEOUT", time.Hour),
		HTTPBodyLimit:                  getEnv("HTTP_BODY_LIMIT", "8M"),
		RateLimitEnabled:               p.Bool("RATE_LIMIT_ENABLED", true),
		RateLimitWindow:                p.Duration("RATE_LIMIT_WINDOW", time.Minute),
		JWTSecret:                      getEnv("JWT_SECRET", devJWTSecret),
		JWTIssuer:                      getEnv("JWT_ISSUER", "vidra"),
		JWTAudience:                    getEnv("JWT_AUDIENCE", "vidra"),
		JWTAccessTTL:                   p.Duration("JWT_ACCESS_TTL", 15*time.Minute),
		JWTRefreshTTL:                  p.Duration("JWT_REFRESH_TTL", 720*time.Hour),
		StorageBackend:                 getEnv("STORAGE_BACKEND", "local"),
		StorageLocalRoot:               getEnv("STORAGE_LOCAL_ROOT", "./data/media"),
		StorageS3Endpoint:              getEnv("STORAGE_S3_ENDPOINT", ""),
		StorageS3Bucket:                getEnv("STORAGE_S3_BUCKET", ""),
		StorageS3AccessKey:             getEnv("STORAGE_S3_ACCESS_KEY", ""),
		StorageS3SecretKey:             getEnv("STORAGE_S3_SECRET_KEY", ""),
		StorageS3Region:                getEnv("STORAGE_S3_REGION", ""),
		StorageS3UseSSL:                p.Bool("STORAGE_S3_USE_SSL", true),
		StorageS3ForcePathStyle:        p.Bool("STORAGE_S3_FORCE_PATH_STYLE", false),
		IPFSEnabled:                    p.Bool("IPFS_ENABLED", false),
		IPFSAPIURL:                     strings.TrimRight(getEnv("IPFS_API_URL", ""), "/"),
		IPFSGatewayURL:                 strings.TrimRight(getEnv("IPFS_GATEWAY_URL", ""), "/"),
		IPFSAddTimeout:                 p.Duration("IPFS_ADD_TIMEOUT", 60*time.Second),
		IPFSReconcileInterval:          p.Duration("IPFS_RECONCILE_INTERVAL", 5*time.Minute),
		IPFSMirrorPrivate:              p.Bool("IPFS_MIRROR_PRIVATE", false),
		IPFSClusterAPIURL:              strings.TrimRight(getEnv("IPFS_CLUSTER_API_URL", ""), "/"),
		IPFSClusterToken:               getEnv("IPFS_CLUSTER_TOKEN", ""),
		IPFSPrivateAPIURL:              strings.TrimRight(getEnv("IPFS_PRIVATE_API_URL", ""), "/"),
		IPFSPrivateClusterAPIURL:       strings.TrimRight(getEnv("IPFS_PRIVATE_CLUSTER_API_URL", ""), "/"),
		IPFSPrivateClusterToken:        getEnv("IPFS_PRIVATE_CLUSTER_TOKEN", ""),
		UploadMaxSize:                  getEnv("UPLOAD_MAX_SIZE", "2G"),
		YtdlpImportEnabled:             p.Bool("YTDLP_IMPORT_ENABLED", false),
		YtdlpPath:                      getEnv("YTDLP_PATH", "yt-dlp"),
		YtdlpTimeout:                   p.Duration("YTDLP_TIMEOUT", 15*time.Minute),
		YtdlpProxy:                     strings.TrimSpace(getEnv("YTDLP_PROXY", "")),
		ChannelSyncEnabled:             p.Bool("CHANNEL_SYNC_ENABLED", false),
		ChannelSyncInterval:            p.Duration("CHANNEL_SYNC_INTERVAL", time.Hour),
		ChannelSyncCooldown:            p.Duration("CHANNEL_SYNC_COOLDOWN", time.Minute),
		PeerTubeImportEnabled:          p.Bool("PEERTUBE_IMPORT_ENABLED", false),
		PeerTubeSourceDatabaseURL:      getEnv("PEERTUBE_SOURCE_DATABASE_URL", ""),
		PeerTubeSourceStorageBackend:   getEnv("PEERTUBE_SOURCE_STORAGE_BACKEND", "local"),
		PeerTubeSourceStorageLocalRoot: getEnv("PEERTUBE_SOURCE_STORAGE_LOCAL_ROOT", ""),
		PeerTubeSourceS3Endpoint:       getEnv("PEERTUBE_SOURCE_S3_ENDPOINT", ""),
		PeerTubeSourceS3Bucket:         getEnv("PEERTUBE_SOURCE_S3_BUCKET", ""),
		PeerTubeSourceS3AccessKey:      getEnv("PEERTUBE_SOURCE_S3_ACCESS_KEY", ""),
		PeerTubeSourceS3SecretKey:      getEnv("PEERTUBE_SOURCE_S3_SECRET_KEY", ""),
		PeerTubeSourceS3Region:         getEnv("PEERTUBE_SOURCE_S3_REGION", ""),
		PeerTubeSourceS3UseSSL:         p.Bool("PEERTUBE_SOURCE_S3_USE_SSL", true),
		PeerTubeSourceS3ForcePathStyle: p.Bool("PEERTUBE_SOURCE_S3_FORCE_PATH_STYLE", false),
		PeerTubeImportConflictPolicy:   strings.ToLower(getEnv("PEERTUBE_IMPORT_CONFLICT_POLICY", "skip")),
		PeerTubeImportMediaMode:        strings.ToLower(getEnv("PEERTUBE_IMPORT_MEDIA_MODE", "copy")),
	}

	cfg.HTTPPort = p.Int("HTTP_PORT", 8080)
	cfg.RateLimitRequests = p.Int("RATE_LIMIT_REQUESTS", 120)
	cfg.AuthRateLimitRequests = p.Int("AUTH_RATE_LIMIT_REQUESTS", 10)

	// The media budget is deliberately an order of magnitude above the API one:
	// see MediaRateLimitRequests. 3000/min is ~50 blob reads a second from one
	// IP — far above any single viewer, far below a useful scrape rate.
	cfg.MediaRateLimitRequests = p.Int("MEDIA_RATE_LIMIT_REQUESTS", 3000)

	cfg.AttachmentUploadRateLimitRequests = p.Int("ATTACHMENT_UPLOAD_RATE_LIMIT_REQUESTS", 60)
	cfg.AttachmentUploadRateLimitWindow = p.Duration("ATTACHMENT_UPLOAD_RATE_LIMIT_WINDOW", 10*time.Minute)
	cfg.SMTPPort = p.Int("SMTP_PORT", 587)
	cfg.YtdlpMaxHeight = p.Int("YTDLP_MAX_HEIGHT", 1080)
	cfg.ChannelSyncMaxPerUser = p.Int("CHANNEL_SYNC_MAX_PER_USER", 5)
	cfg.ChannelSyncBatch = p.Int("CHANNEL_SYNC_BATCH", 15)
	cfg.UploadMaxActiveSessionsPerUser = p.Int("UPLOAD_MAX_ACTIVE_SESSIONS_PER_USER", 5)
	cfg.InstanceDefaultQuotaBytes = p.Int64("INSTANCE_DEFAULT_QUOTA_BYTES", 0)
	cfg.IPFSPinConcurrency = p.Int("IPFS_PIN_CONCURRENCY", 2)

	// Search client per-endpoint deadline overrides (ms; 0 = client defaults).
	cfg.SearchSuggestTimeoutMS = p.Int("SEARCH_SUGGEST_TIMEOUT_MS", 0)
	cfg.SearchQueryTimeoutMS = p.Int("SEARCH_QUERY_TIMEOUT_MS", 0)
	cfg.SearchRecsTimeoutMS = p.Int("SEARCH_RECS_TIMEOUT_MS", 0)

	// Private-tier worker tuning INHERITS the public defaults when unset (spec §2).
	cfg.IPFSPrivateAddTimeout = p.Duration("IPFS_PRIVATE_ADD_TIMEOUT", cfg.IPFSAddTimeout)
	cfg.IPFSPrivatePinConcurrency = p.Int("IPFS_PRIVATE_PIN_CONCURRENCY", cfg.IPFSPinConcurrency)

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

	// Refuse to boot on ANY malformed typed variable before semantic
	// validation — every bad variable is reported, not just the first.
	if err := p.Err(); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// CheckEnv validates a CANDIDATE environment — a generated env file, a wizard's
// proposed answers — as if the process booted with exactly these variables and
// nothing else: a key absent from vars is UNSET (its default applies), and an
// empty value still means unset (`KEY=` == omitted). Keys this package does not
// know (compose-only variables like VIDRA_CORE_TAG) are simply never looked up,
// so they are ignored rather than rejected.
//
// The error is the one Load would have returned; errors attributable to a single
// variable are *VarError, so a caller can map them back to fields with
// errors.As over the joined tree.
func CheckEnv(vars map[string]string) error {
	_, err := LoadFrom(func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	})
	return err
}

// VarError is a configuration error attributable to a SINGLE environment
// variable, so a caller (setup engine, wizard, doctor) can map it back to the
// field the operator must fix — errors.As over the joined tree LoadFrom /
// CheckEnv return, then read Var. Error() reproduces the boot-time message
// verbatim: the wrapper adds attribution, it never changes what an operator
// reads. Rules that span variables (a required combination, a mutual
// exclusion) are deliberately NOT wrapped — they belong to no single field.
type VarError struct {
	// Var is the environment variable name, e.g. "HTTP_PORT".
	Var string
	// Msg is the operator-facing message, in the "config: VAR ..." idiom.
	Msg string
	// cause is the error the message wrapped (a strconv/time parse failure),
	// or nil.
	cause error
}

func (e *VarError) Error() string { return e.Msg }

// Unwrap exposes the underlying parse failure, so errors.Is/As still reach it.
func (e *VarError) Unwrap() error { return e.cause }

// varErrorf builds a VarError attributed to v whose Error() is exactly the text
// fmt.Errorf(format, args...) would have produced, keeping any %w cause
// reachable. Callers pass the message unchanged — message-string equivalence is
// the contract the existing tests and operator docs rely on.
func varErrorf(v, format string, args ...any) *VarError {
	err := fmt.Errorf(format, args...)
	return &VarError{Var: v, Msg: err.Error(), cause: errors.Unwrap(err)}
}

// validate checks the SEMANTIC rules — what a parsed value MEANS, as opposed to
// whether it parsed at all — and reports EVERY one it finds, joined, instead of
// stopping at the first. An operator filling in a generated env file (or a
// wizard rendering one message per input field) needs the whole list: fix one
// rule, re-run, discover the next is precisely the loop the setup engine exists
// to spare them.
//
// It stays a pass SEPARATE from the malformed-value errors LoadFrom collects on
// the parser before calling this (p.Err()). Semantics computed over values that
// never parsed are second-order noise — a malformed HTTP_PORT has already
// defaulted to 8080, so "HTTP_PORT out of range" would be a statement about a
// value the operator never wrote — so a malformed environment still refuses
// first and this never runs on it.
//
// Two shapes are preserved deliberately, and both have tests:
//
//   - Every message is byte-for-byte what it was when this returned early.
//     varErrorf's message-string equivalence is the contract the operator docs,
//     setup.Check and the wizard's field rendering all read.
//   - A GUARD keeps guarding. Where a branch is selected BY an invalid value (an
//     unrecognised STORAGE_BACKEND) or a rule reads something an earlier rule had
//     to establish (the parsed PUBLIC_BASE_URL a path check needs), the dependent
//     checks stay skipped: five "missing S3 key" lines under "unsupported
//     STORAGE_BACKEND" bury the one line that has to change.
//
// Rules that span variables stay unattributed (plain fmt.Errorf) exactly as
// before — they belong to no single field, and a wizard shows them
// instance-level rather than against an input.
func (c *Config) validate() error {
	var errs []error
	// seen dedupes findings on (variable, message). Collecting instead of
	// returning early made a genuine duplicate possible for the first time: one
	// http:// PUBLIC_BASE_URL in production trips the plain-http gate, the
	// federation https rule and the OAuth https rule, and the last two produce
	// the SAME sentence. Nothing downstream dedupes — setup.configIssues turns
	// every leaf into an Issue — so doctor and the wizard would print the same
	// line twice and an operator would look for a second thing to fix.
	//
	// The key includes the variable so two fields failing the same rule
	// ("... is required") still both speak; unattributed multi-variable rules
	// dedupe on their message alone.
	seen := map[string]bool{}
	// add records findings. A nil error — a sub-validator that found nothing —
	// is dropped, so the sub-validator calls read the same as when their result
	// was returned directly. A JOINED error (what the sub-validators now return)
	// is flattened into its leaves, so dedupe sees individual findings rather
	// than a tree, and the final result is one flat list.
	var add func(err error)
	add = func(err error) {
		if err == nil {
			return
		}
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, sub := range joined.Unwrap() {
				add(sub)
			}
			return
		}
		key := err.Error()
		var ve *VarError
		if errors.As(err, &ve) {
			key = ve.Var + "\x00" + ve.Msg
		}
		if seen[key] {
			return
		}
		seen[key] = true
		errs = append(errs, err)
	}

	switch c.Environment {
	case "development", "test", "production":
	default:
		add(varErrorf("VIDRA_ENV", "config: invalid VIDRA_ENV %q (want development|test|production)", c.Environment))
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		add(varErrorf("LOG_LEVEL", "config: invalid LOG_LEVEL %q (want debug|info|warn|error)", c.LogLevel))
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		add(varErrorf("LOG_FORMAT", "config: invalid LOG_FORMAT %q (want json|text)", c.LogFormat))
	}
	if c.OTelEnabled {
		switch c.OTelExporterProtocol {
		case "grpc", "http/protobuf":
		default:
			add(varErrorf("OTEL_EXPORTER_OTLP_PROTOCOL", "config: invalid OTEL_EXPORTER_OTLP_PROTOCOL %q (want grpc|http/protobuf)", c.OTelExporterProtocol))
		}
		if strings.TrimSpace(c.OTelExporterEndpoint) == "" {
			add(varErrorf("OTEL_EXPORTER_OTLP_ENDPOINT", "config: OTEL_EXPORTER_OTLP_ENDPOINT is required when OTEL_ENABLED is true"))
		}
	}
	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		add(varErrorf("HTTP_PORT", "config: HTTP_PORT %d out of range", c.HTTPPort))
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		add(varErrorf("DATABASE_URL", "config: DATABASE_URL is required"))
	}
	if strings.TrimSpace(c.RedisURL) == "" {
		add(varErrorf("REDIS_URL", "config: REDIS_URL is required"))
	}
	if c.HTTPRequestTimeout <= 0 {
		add(varErrorf("HTTP_REQUEST_TIMEOUT", "config: HTTP_REQUEST_TIMEOUT must be positive"))
	}
	if c.HTTPStreamRequestTimeout <= 0 {
		add(varErrorf("HTTP_STREAM_REQUEST_TIMEOUT", "config: HTTP_STREAM_REQUEST_TIMEOUT must be positive"))
	}
	if _, err := bytes.Parse(c.HTTPBodyLimit); err != nil {
		add(varErrorf("HTTP_BODY_LIMIT", "config: invalid HTTP_BODY_LIMIT %q: %w", c.HTTPBodyLimit, err))
	}
	add(c.validatePublicScheme())
	if _, err := parseTrustedProxyCIDRs(c.TrustedProxyCIDRs); err != nil {
		add(err)
	}
	if c.RateLimitEnabled {
		if c.RateLimitRequests <= 0 {
			add(varErrorf("RATE_LIMIT_REQUESTS", "config: RATE_LIMIT_REQUESTS must be positive when rate limiting is enabled"))
		}
		if c.RateLimitWindow <= 0 {
			add(varErrorf("RATE_LIMIT_WINDOW", "config: RATE_LIMIT_WINDOW must be positive when rate limiting is enabled"))
		}
		if c.AuthRateLimitRequests <= 0 {
			add(varErrorf("AUTH_RATE_LIMIT_REQUESTS", "config: AUTH_RATE_LIMIT_REQUESTS must be positive when rate limiting is enabled"))
		}
		if c.MediaRateLimitRequests <= 0 {
			add(varErrorf("MEDIA_RATE_LIMIT_REQUESTS", "config: MEDIA_RATE_LIMIT_REQUESTS must be positive when rate limiting is enabled"))
		}
		if c.AttachmentUploadRateLimitRequests <= 0 {
			add(varErrorf("ATTACHMENT_UPLOAD_RATE_LIMIT_REQUESTS", "config: ATTACHMENT_UPLOAD_RATE_LIMIT_REQUESTS must be positive when rate limiting is enabled"))
		}
		if c.AttachmentUploadRateLimitWindow <= 0 {
			add(varErrorf("ATTACHMENT_UPLOAD_RATE_LIMIT_WINDOW", "config: ATTACHMENT_UPLOAD_RATE_LIMIT_WINDOW must be positive when rate limiting is enabled"))
		}
	}
	if c.JWTAccessTTL <= 0 {
		add(varErrorf("JWT_ACCESS_TTL", "config: JWT_ACCESS_TTL must be positive"))
	}
	if c.JWTRefreshTTL <= 0 {
		add(varErrorf("JWT_REFRESH_TTL", "config: JWT_REFRESH_TTL must be positive"))
	}
	// The backend SELECTS which keys are required, so an unrecognised value can
	// only report itself: listing five missing S3 keys under "unsupported
	// STORAGE_BACKEND" would send an operator to fix the wrong five lines.
	switch c.StorageBackend {
	case "local":
		if strings.TrimSpace(c.StorageLocalRoot) == "" {
			add(varErrorf("STORAGE_LOCAL_ROOT", "config: STORAGE_LOCAL_ROOT is required for the local storage backend"))
		}
	case "s3":
		if strings.TrimSpace(c.StorageS3Endpoint) == "" {
			add(varErrorf("STORAGE_S3_ENDPOINT", "config: STORAGE_S3_ENDPOINT is required for the s3 storage backend"))
		}
		if strings.Contains(c.StorageS3Endpoint, "://") {
			add(varErrorf("STORAGE_S3_ENDPOINT", "config: STORAGE_S3_ENDPOINT must be host[:port] without a scheme (got %q); use STORAGE_S3_USE_SSL to pick http/https", c.StorageS3Endpoint))
		}
		if strings.TrimSpace(c.StorageS3Bucket) == "" {
			add(varErrorf("STORAGE_S3_BUCKET", "config: STORAGE_S3_BUCKET is required for the s3 storage backend"))
		}
		if strings.TrimSpace(c.StorageS3AccessKey) == "" {
			add(varErrorf("STORAGE_S3_ACCESS_KEY", "config: STORAGE_S3_ACCESS_KEY is required for the s3 storage backend"))
		}
		if strings.TrimSpace(c.StorageS3SecretKey) == "" {
			add(varErrorf("STORAGE_S3_SECRET_KEY", "config: STORAGE_S3_SECRET_KEY is required for the s3 storage backend"))
		}
	default:
		add(varErrorf("STORAGE_BACKEND", "config: unsupported STORAGE_BACKEND %q (want local|s3)", c.StorageBackend))
	}
	if _, err := bytes.Parse(c.UploadMaxSize); err != nil {
		add(varErrorf("UPLOAD_MAX_SIZE", "config: invalid UPLOAD_MAX_SIZE %q: %w", c.UploadMaxSize, err))
	}
	if c.UploadMaxActiveSessionsPerUser < 0 {
		add(varErrorf("UPLOAD_MAX_ACTIVE_SESSIONS_PER_USER", "config: UPLOAD_MAX_ACTIVE_SESSIONS_PER_USER must be >= 0 (0 = unlimited), got %d", c.UploadMaxActiveSessionsPerUser))
	}
	if c.InstanceDefaultQuotaBytes < 0 {
		add(varErrorf("INSTANCE_DEFAULT_QUOTA_BYTES", "config: INSTANCE_DEFAULT_QUOTA_BYTES must be >= 0 (0 = unlimited), got %d", c.InstanceDefaultQuotaBytes))
	}
	if c.Environment == "production" {
		// The dev constant is 48 bytes, so these two can never both fire: the
		// first is "you left the default in", the second "yours is too short".
		if c.JWTSecret == devJWTSecret {
			add(varErrorf("JWT_SECRET", "config: JWT_SECRET must be set in production (the dev default is not allowed)"))
		}
		if len(c.JWTSecret) < 32 {
			add(varErrorf("JWT_SECRET", "config: JWT_SECRET must be at least 32 bytes in production"))
		}
		// The development-only escape hatches are REFUSED in production, not
		// merely warned about (deploy/README.md already documents this refusal).
		// DEV_MAIL_CAPTURE_ENABLED mounts GET /api/v1/dev/email-token, which hands
		// out a live password-reset token for ANY address — an admin-takeover
		// primitive one copy-pasted env line away (env/qa.env.example and the
		// meta Makefile's e2e target both set these true, and neither sets
		// VIDRA_ENV=production). HTTP_IMPORT_ALLOW_PRIVATE_URLS re-opens the SSRF
		// hole the dial-time guard closes. The route registration is ALSO gated on
		// the environment in internal/httpapi (defence in depth) — a fail-secure
		// this important should not rest on a single check.
		if c.DevMailCaptureEnabled {
			add(varErrorf("DEV_MAIL_CAPTURE_ENABLED", "config: DEV_MAIL_CAPTURE_ENABLED must not be set in production"))
		}
		if c.ImportAllowPrivateURLs {
			add(varErrorf("HTTP_IMPORT_ALLOW_PRIVATE_URLS", "config: HTTP_IMPORT_ALLOW_PRIVATE_URLS must not be set in production"))
		}
		// A fixed owner-claim token is a deterministic admin-bootstrap credential
		// sitting in the environment — dev/test-only by construction.
		if c.OwnerClaimToken != "" {
			add(varErrorf("OWNER_CLAIM_TOKEN", "config: OWNER_CLAIM_TOKEN must not be set in production"))
		}
	}
	// The length floor only speaks where the token is ALLOWED to exist. In
	// production the refusal above is the whole answer, and "make it longer"
	// underneath "do not set it at all" is advice that contradicts itself.
	if c.Environment != "production" && c.OwnerClaimToken != "" && len(c.OwnerClaimToken) < 16 {
		add(varErrorf("OWNER_CLAIM_TOKEN", "config: OWNER_CLAIM_TOKEN must be at least 16 characters when set"))
	}
	if c.Environment == "production" {
		for _, o := range c.CORSAllowedOrigins {
			if o == "*" {
				add(varErrorf("CORS_ALLOWED_ORIGINS", "config: wildcard CORS origin is not allowed in production"))
				break
			}
		}
	}
	if c.MailEnabled {
		if strings.TrimSpace(c.SMTPHost) == "" {
			add(varErrorf("SMTP_HOST", "config: SMTP_HOST is required when MAIL_ENABLED=true"))
		}
		if c.SMTPPort < 1 || c.SMTPPort > 65535 {
			add(varErrorf("SMTP_PORT", "config: SMTP_PORT %d out of range", c.SMTPPort))
		}
		// "Required" guards "well-formed": an empty address is not also a
		// malformed one.
		if from := strings.TrimSpace(c.SMTPFrom); from == "" {
			add(varErrorf("SMTP_FROM", "config: SMTP_FROM is required when MAIL_ENABLED=true"))
		} else if !strings.Contains(from, "@") || strings.ContainsAny(from, "\r\n") {
			add(varErrorf("SMTP_FROM", "config: SMTP_FROM must be a plain email address"))
		}
	}
	if c.MalwareScanEnabled && strings.TrimSpace(c.ClamAVAddr) == "" {
		add(varErrorf("CLAMAV_ADDR", "config: CLAMAV_ADDR is required when MALWARE_SCAN_ENABLED=true"))
	}
	if c.MalwareScanEnabled && c.ClamAVTimeout <= 0 {
		add(varErrorf("CLAMAV_TIMEOUT", "config: CLAMAV_TIMEOUT must be a positive duration when MALWARE_SCAN_ENABLED=true"))
	}
	switch c.MalwareScanMode {
	case "", "fail-closed", "fail-open", "quarantine": // "" = default fail-closed
	default:
		add(varErrorf("MALWARE_SCAN_MODE", "config: MALWARE_SCAN_MODE %q must be one of fail-closed, fail-open, quarantine", c.MalwareScanMode))
	}
	if c.TranscodingAV1Enabled {
		add(varErrorf("TRANSCODING_AV1_ENABLED", "config: TRANSCODING_AV1_ENABLED is not supported yet — AV1 transcoding is deferred (see fix_plan P6.3); leave it false"))
	}
	if c.WhisperEnabled {
		u, err := url.Parse(c.WhisperEndpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			add(varErrorf("WHISPER_ENDPOINT", "config: WHISPER_ENDPOINT must be a valid http(s) URL when WHISPER_ENABLED=true"))
		}
	}
	if c.WhisperDefaultLanguage != "" && !languageTag.MatchString(c.WhisperDefaultLanguage) {
		add(varErrorf("WHISPER_DEFAULT_LANGUAGE", "config: WHISPER_DEFAULT_LANGUAGE %q must be a BCP-47-ish language tag (e.g. en, pt-BR)", c.WhisperDefaultLanguage))
	}
	// vidra-search integration (search-service W4): validate the URL shape when
	// set, and require a strong shared secret in production. The two are about
	// different variables, so a bad URL does not silence the missing secret.
	if c.SearchServiceURL != "" {
		u, err := url.Parse(c.SearchServiceURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			add(varErrorf("SEARCH_SERVICE_URL", "config: SEARCH_SERVICE_URL must be a valid http(s) URL when set"))
		}
		if c.Environment == "production" && len(c.SearchInternalSecret) < 32 {
			add(varErrorf("SEARCH_INTERNAL_SECRET", "config: SEARCH_INTERNAL_SECRET must be at least 32 characters when SEARCH_SERVICE_URL is set in production"))
		}
	}
	// yt-dlp platform-URL import: validate the combination only when enabled so a
	// default (disabled) deployment never trips these. YTDLP_MAX_HEIGHT is bounded
	// even when disabled (a nonsensical value is always a config error).
	if c.YtdlpMaxHeight < 0 || c.YtdlpMaxHeight > 4320 {
		add(varErrorf("YTDLP_MAX_HEIGHT", "config: YTDLP_MAX_HEIGHT %d out of range (0 = no cap, else 144..4320)", c.YtdlpMaxHeight))
	}
	if c.YtdlpImportEnabled {
		if strings.TrimSpace(c.YtdlpPath) == "" {
			add(varErrorf("YTDLP_PATH", "config: YTDLP_PATH is required when YTDLP_IMPORT_ENABLED=true"))
		}
		if c.YtdlpTimeout <= 0 {
			add(varErrorf("YTDLP_TIMEOUT", "config: YTDLP_TIMEOUT must be a positive duration when YTDLP_IMPORT_ENABLED=true"))
		}
		if c.YtdlpProxy != "" {
			u, perr := url.Parse(c.YtdlpProxy)
			if perr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") {
				add(varErrorf("YTDLP_PROXY", "config: YTDLP_PROXY must be an http(s) or socks5 URL when set"))
			}
		}
	}
	// Channel auto-sync bounds (W2.C4). Validated only when enabled so a default
	// deployment never trips them; the effective on/off state is
	// ChannelSyncEnabled AND YtdlpImportEnabled (checked/logged at wiring time).
	if c.ChannelSyncEnabled {
		if c.ChannelSyncInterval <= 0 {
			add(varErrorf("CHANNEL_SYNC_INTERVAL", "config: CHANNEL_SYNC_INTERVAL must be a positive duration when CHANNEL_SYNC_ENABLED=true"))
		}
		if c.ChannelSyncBatch < 1 || c.ChannelSyncBatch > 100 {
			add(varErrorf("CHANNEL_SYNC_BATCH", "config: CHANNEL_SYNC_BATCH %d out of range (1..100)", c.ChannelSyncBatch))
		}
		if c.ChannelSyncCooldown < 0 {
			add(varErrorf("CHANNEL_SYNC_COOLDOWN", "config: CHANNEL_SYNC_COOLDOWN must not be negative"))
		}
	}
	if c.MFAKeyKEK != "" {
		if k, err := base64.StdEncoding.DecodeString(c.MFAKeyKEK); err != nil || len(k) != 32 {
			add(varErrorf("MFA_KEY_KEK", "config: MFA_KEY_KEK must be base64 of exactly 32 bytes"))
		}
	}
	add(c.validateOAuth())
	if c.FederationEnabled {
		// The parsed origin is the GUARD: the path and production-scheme rules
		// below read fields of it, so neither can speak about a value that is not
		// a URL at all.
		u, err := url.Parse(c.PublicBaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			add(varErrorf("PUBLIC_BASE_URL", "config: PUBLIC_BASE_URL must be a valid http(s) origin when FEDERATION_ENABLED=true"))
		} else {
			if u.Path != "" && u.Path != "/" {
				add(varErrorf("PUBLIC_BASE_URL", "config: PUBLIC_BASE_URL must not include a path (got %q)", c.PublicBaseURL))
			}
			if c.Environment == "production" && u.Scheme != "https" {
				add(varErrorf("PUBLIC_BASE_URL", "config: PUBLIC_BASE_URL must be https in production"))
			}
		}
		if c.FederationKeyKEK != "" {
			if k, err := base64.StdEncoding.DecodeString(c.FederationKeyKEK); err != nil || len(k) != 32 {
				add(varErrorf("FEDERATION_KEY_KEK", "config: FEDERATION_KEY_KEK must be base64 of exactly 32 bytes"))
			}
		} else if c.Environment == "production" {
			add(varErrorf("FEDERATION_KEY_KEK", "config: FEDERATION_KEY_KEK is required in production when FEDERATION_ENABLED=true"))
		}
	}
	if c.ATProtoKeyKEK != "" {
		if k, err := base64.StdEncoding.DecodeString(c.ATProtoKeyKEK); err != nil || len(k) != 32 {
			add(varErrorf("ATPROTO_KEY_KEK", "config: ATPROTO_KEY_KEK must be base64 of exactly 32 bytes"))
		}
	}
	if c.ATProtoEnabled && c.Environment == "production" && c.ATProtoKEK() == "" {
		add(fmt.Errorf("config: ATPROTO_KEY_KEK (or FEDERATION_KEY_KEK) is required in production when ATPROTO_ENABLED=true"))
	}
	add(c.validatePeerTubeImport())
	add(c.validateIPFS())
	return errors.Join(errs...)
}

// validateIPFS checks the hybrid IPFS media-mirroring (P19 + P19.P) configuration.
// Every value is OFF/inert by default; the PUBLIC-tier checks only bite once
// IPFS_ENABLED is set, but the PRIVATE-tier guard fires whenever IPFS_MIRROR_PRIVATE
// is true (the private tier may run standalone with IPFS_ENABLED=false — an operator
// may replicate only non-public media). The private guard is the config-level
// enforcement of the spec §1/§2 invariant: non-public media is mirrored ONLY to a
// dedicated, separate private-swarm node — never the public node, never dual-homed.
func (c *Config) validateIPFS() error {
	var errs []error
	if err := c.validateIPFSPrivate(); err != nil {
		errs = append(errs, err)
	}
	if !c.IPFSEnabled {
		return errors.Join(errs...)
	}
	// "Both are required" is the GUARD for the shape checks: with one missing
	// there is nothing to parse, and "IPFS_API_URL must be a valid http(s) URL"
	// stacked under "IPFS_API_URL is required" says the same thing twice. It is
	// also a MULTI-VARIABLE rule, so it stays unattributed by design.
	if strings.TrimSpace(c.IPFSAPIURL) == "" || strings.TrimSpace(c.IPFSGatewayURL) == "" {
		errs = append(errs, fmt.Errorf("config: IPFS_API_URL and IPFS_GATEWAY_URL are required when IPFS_ENABLED"))
	} else {
		for label, raw := range map[string]string{"IPFS_API_URL": c.IPFSAPIURL, "IPFS_GATEWAY_URL": c.IPFSGatewayURL} {
			u, err := url.Parse(raw)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				errs = append(errs, varErrorf(label, "config: %s must be a valid http(s) URL when IPFS_ENABLED=true", label))
			}
		}
	}
	if c.IPFSClusterAPIURL != "" {
		u, err := url.Parse(c.IPFSClusterAPIURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			errs = append(errs, varErrorf("IPFS_CLUSTER_API_URL", "config: IPFS_CLUSTER_API_URL must be a valid http(s) URL"))
		}
	}
	if c.IPFSAddTimeout <= 0 {
		errs = append(errs, varErrorf("IPFS_ADD_TIMEOUT", "config: IPFS_ADD_TIMEOUT must be positive"))
	}
	if c.IPFSPinConcurrency < 1 {
		errs = append(errs, varErrorf("IPFS_PIN_CONCURRENCY", "config: IPFS_PIN_CONCURRENCY must be >= 1"))
	}
	if c.IPFSReconcileInterval <= 0 {
		errs = append(errs, varErrorf("IPFS_RECONCILE_INTERVAL", "config: IPFS_RECONCILE_INTERVAL must be positive"))
	}
	return errors.Join(errs...)
}

// validateIPFSPrivate enforces the private-tier (P19.P) config shape (spec §2). It
// SUPERSEDES the shipped P19 guard (IPFS_MIRROR_PRIVATE ⇒ IPFS_CLUSTER_API_URL): the
// real requirement is a DEDICATED private-swarm node, not merely a cluster URL. Runs
// regardless of IPFS_ENABLED because the private tier may be operated standalone.
func (c *Config) validateIPFSPrivate() error {
	var errs []error
	privateURL := strings.TrimSpace(c.IPFSPrivateAPIURL)
	// The master opt-in requires a dedicated private-swarm kubo RPC.
	if c.IPFSMirrorPrivate && privateURL == "" {
		errs = append(errs, fmt.Errorf("config: IPFS_MIRROR_PRIVATE requires IPFS_PRIVATE_API_URL (a dedicated private-swarm kubo — non-public media is never mirrored to the public network)"))
	}
	// An unset private URL is the GUARD for everything below: there is no node
	// to be malformed, to collide with the public one, or to have timeouts for.
	if privateURL == "" {
		return errors.Join(errs...)
	}
	// Never dual-home: the private node must be a DIFFERENT node from the public
	// mirror. Equality is compared on the trimmed, slash-normalized values both
	// fields already store, so a trailing-slash difference cannot sneak past.
	if privateURL == strings.TrimSpace(c.IPFSAPIURL) {
		errs = append(errs, fmt.Errorf("config: the private IPFS mirror must be a separate node from the public mirror (refusing to dual-home)"))
	}
	if !isHTTPURL(privateURL) {
		errs = append(errs, varErrorf("IPFS_PRIVATE_API_URL", "config: IPFS_PRIVATE_API_URL must be a valid http(s) URL"))
	}
	if c.IPFSPrivateClusterAPIURL != "" && !isHTTPURL(c.IPFSPrivateClusterAPIURL) {
		errs = append(errs, varErrorf("IPFS_PRIVATE_CLUSTER_API_URL", "config: IPFS_PRIVATE_CLUSTER_API_URL must be a valid http(s) URL"))
	}
	if c.IPFSMirrorPrivate {
		if c.IPFSPrivateAddTimeout <= 0 {
			errs = append(errs, varErrorf("IPFS_PRIVATE_ADD_TIMEOUT", "config: IPFS_PRIVATE_ADD_TIMEOUT must be positive"))
		}
		if c.IPFSPrivatePinConcurrency < 1 {
			errs = append(errs, varErrorf("IPFS_PRIVATE_PIN_CONCURRENCY", "config: IPFS_PRIVATE_PIN_CONCURRENCY must be >= 1"))
		}
	}
	return errors.Join(errs...)
}

// shellTrueSpellings are the values BOTH this process and the deploy shell
// scripts read as true. It MIRRORS setup.IsTrue's list (internal/setup,
// trueSpellings) exactly; this package cannot import that one, because setup
// imports this one and the dependency only runs the one way.
//
// The comparison is exact rather than case-folding, for setup.IsTrue's reason:
// `[ "$VIDRA_EXTERNAL_POSTGRES" = true ]` in a shell script reads `tRue` as
// FALSE, so a Go reader that folded case would disagree with the script it sits
// beside. Here that disagreement would put the managed-database backup advice on
// the admin page of a deployment whose own nightly dump is the only backup it
// has — the exact wrong sentence, on the page that exists to get it right.
var shellTrueSpellings = []string{"true", "TRUE", "True", "yes", "YES", "Yes", "1", "on", "ON", "On"}

// isShellTrue reports whether v is a spelling the deploy scripts read as true.
// Everything else — a blank, a `t`, a typo — is FALSE, and never an error: the
// variables read this way are display-only, and refusing to boot an instance to
// correct a sentence is not a trade anybody would choose.
func isShellTrue(v string) bool {
	t := strings.TrimSpace(v)
	for _, spelling := range shellTrueSpellings {
		if t == spelling {
			return true
		}
	}
	return false
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
	var errs []error
	switch c.PeerTubeImportConflictPolicy {
	case "", "skip", "rename", "merge", "fail": // "" = default skip (Load defaults it)
	default:
		errs = append(errs, varErrorf("PEERTUBE_IMPORT_CONFLICT_POLICY", "config: PEERTUBE_IMPORT_CONFLICT_POLICY %q must be one of skip, rename, merge, fail", c.PeerTubeImportConflictPolicy))
	}
	switch c.PeerTubeImportMediaMode {
	case "", "copy", "reference", "none": // "" = default copy (Load defaults it)
	default:
		errs = append(errs, varErrorf("PEERTUBE_IMPORT_MEDIA_MODE", "config: PEERTUBE_IMPORT_MEDIA_MODE %q must be one of copy, reference, none", c.PeerTubeImportMediaMode))
	}
	switch c.PeerTubeSourceStorageBackend {
	case "", "local", "s3": // "" = default local (Load defaults it)
	default:
		errs = append(errs, varErrorf("PEERTUBE_SOURCE_STORAGE_BACKEND", "config: PEERTUBE_SOURCE_STORAGE_BACKEND %q must be local or s3", c.PeerTubeSourceStorageBackend))
	}
	if strings.Contains(c.PeerTubeSourceS3Endpoint, "://") {
		errs = append(errs, varErrorf("PEERTUBE_SOURCE_S3_ENDPOINT", "config: PEERTUBE_SOURCE_S3_ENDPOINT must be host[:port] without a scheme (got %q)", c.PeerTubeSourceS3Endpoint))
	}
	// The admin import surface being off is the GUARD for the source
	// requirements: the CLI supplies its own source flags, so nothing below is a
	// problem for a deployment that never enables the API path.
	if !c.PeerTubeImportEnabled {
		return errors.Join(errs...)
	}
	// The admin import path needs a source to read from.
	if strings.TrimSpace(c.PeerTubeSourceDatabaseURL) == "" {
		errs = append(errs, varErrorf("PEERTUBE_SOURCE_DATABASE_URL", "config: PEERTUBE_SOURCE_DATABASE_URL is required when PEERTUBE_IMPORT_ENABLED=true"))
	}
	if c.PeerTubeImportMediaMode != "reference" && c.PeerTubeImportMediaMode != "none" && c.PeerTubeSourceStorageBackend == "s3" {
		if strings.TrimSpace(c.PeerTubeSourceS3Endpoint) == "" || strings.TrimSpace(c.PeerTubeSourceS3Bucket) == "" {
			errs = append(errs, fmt.Errorf("config: PEERTUBE_SOURCE_S3_ENDPOINT and PEERTUBE_SOURCE_S3_BUCKET are required when the source storage backend is s3"))
		}
	}
	return errors.Join(errs...)
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
	var errs []error
	// The parsed origin guards the https rule (which reads its scheme); the
	// provider loop below is about different variables entirely, so a bad
	// PUBLIC_BASE_URL no longer hides an incomplete provider.
	u, err := url.Parse(c.PublicBaseURL)
	switch {
	case err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https"):
		errs = append(errs, varErrorf("PUBLIC_BASE_URL", "config: PUBLIC_BASE_URL must be a valid http(s) origin when OAUTH_PROVIDERS is set (OAuth redirect URIs derive from it)"))
	case c.Environment == "production" && u.Scheme != "https":
		errs = append(errs, varErrorf("PUBLIC_BASE_URL", "config: PUBLIC_BASE_URL must be https in production"))
	}
	seen := map[string]bool{}
	for _, p := range c.OAuthProviders {
		// A name that is not a valid identifier guards this provider's other
		// rules: every per-provider variable NAME is derived from it, so
		// "OAUTH_NOT A NAME_CLIENT_ID is required" would point at a variable an
		// operator can never write.
		if !oauthProviderName.MatchString(p.Name) {
			errs = append(errs, varErrorf("OAUTH_PROVIDERS", "config: invalid OAuth provider name %q (want lowercase letters/digits/dashes)", p.Name))
			continue
		}
		if seen[p.Name] {
			errs = append(errs, varErrorf("OAUTH_PROVIDERS", "config: duplicate OAuth provider %q in OAUTH_PROVIDERS", p.Name))
			continue
		}
		seen[p.Name] = true
		envName := strings.ToUpper(strings.ReplaceAll(p.Name, "-", "_"))
		iss, ierr := url.Parse(p.IssuerURL)
		switch {
		case ierr != nil || iss.Host == "" || (iss.Scheme != "http" && iss.Scheme != "https"):
			errs = append(errs, varErrorf("OAUTH_"+envName+"_ISSUER", "config: OAUTH_%s_ISSUER must be a valid http(s) URL", envName))
		case c.Environment == "production" && iss.Scheme != "https":
			errs = append(errs, varErrorf("OAUTH_"+envName+"_ISSUER", "config: OAUTH_%s_ISSUER must be https in production", envName))
		}
		if strings.TrimSpace(p.ClientID) == "" {
			errs = append(errs, varErrorf("OAUTH_"+envName+"_CLIENT_ID", "config: OAUTH_%s_CLIENT_ID is required", envName))
		}
		if strings.TrimSpace(p.ClientSecret) == "" {
			errs = append(errs, varErrorf("OAUTH_"+envName+"_CLIENT_SECRET", "config: OAUTH_%s_CLIENT_SECRET is required", envName))
		}
	}
	return errors.Join(errs...)
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

// PublicOriginIsHTTPS reports whether browsers reach this instance over TLS. It
// is the ONE predicate every https-derived default hangs off — Secure cookies
// (CookieSecure) and the HSTS response header — because those two have to agree:
// a deployment that pins Secure cookies while withholding HSTS, or the reverse,
// is one whose login works in exactly half the browsers that try it.
//
// PUBLIC_BASE_URL's scheme is the source of truth, and the fallbacks are chosen
// to fail SECURE in both directions:
//
//   - https origin → true. The ordinary answer.
//   - http origin → false. validate() has already refused this in production
//     unless VIDRA_ALLOW_PLAIN_HTTP said so, so reaching here means somebody
//     deliberately deployed a lab/LAN instance with no TLS — Secure cookies
//     would simply never be sent back, i.e. no login at all.
//   - unset origin → whether this is production. Unchanged from before: a
//     production deployment is expected to terminate TLS, and guessing "no"
//     because a variable is missing is how a public instance ships without
//     Secure cookies.
//
// Over-emitting HSTS costs nothing: per RFC 6797 §8.1 a browser ignores the
// header on a plain-http response. Under-emitting it on a real TLS deployment is
// the failure that matters, which is why the unset case leans the way it does.
func (c *Config) PublicOriginIsHTTPS() bool {
	switch scheme := strings.ToLower(c.PublicBaseURL); {
	case strings.HasPrefix(scheme, "https://"):
		return true
	case strings.HasPrefix(scheme, "http://"):
		return false
	default:
		return c.Environment == "production"
	}
}

// validatePublicScheme is the gate that makes a plain-http production origin a
// DELIBERATE state rather than a typo nobody notices.
//
// setup.Check validates every generated env file with production forced, so this
// one rule is what lets `VIDRA_TLS_MODE=plain-http` survive it — there is no
// second copy of the rule in the setup engine, on purpose (one engine, zero
// drift; see the package comment).
func (c *Config) validatePublicScheme() error {
	if c.Environment != "production" || c.AllowPlainHTTP {
		return nil
	}
	if !strings.HasPrefix(strings.ToLower(c.PublicBaseURL), "http://") {
		return nil
	}
	return varErrorf("PUBLIC_BASE_URL", "config: PUBLIC_BASE_URL is a plain-http origin (%q) in production. Over http the api cannot set Secure cookies and browsers ignore HSTS, so sessions and every other https-derived default are off — which is either a typo (write https://) or a deliberate lab/LAN/air-gapped install. If it is deliberate, say so: set VIDRA_ALLOW_PLAIN_HTTP=true, and set VIDRA_TLS_MODE=plain-http so the reverse proxy is generated for http as well", c.PublicBaseURL)
}

// parseTrustedProxyCIDRs is the ONE parser for TRUSTED_PROXY_CIDRS: validate()
// calls it for the refusal, TrustedProxyNets calls it for the networks. A second
// parse of the same strings is how "it validated" and "it is trusted" start
// disagreeing about a value with an odd host part.
func parseTrustedProxyCIDRs(entries []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(entries))
	for _, raw := range entries {
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, varErrorf("TRUSTED_PROXY_CIDRS", "config: TRUSTED_PROXY_CIDRS entry %q is not a CIDR block — write a network with a prefix length (203.0.113.7/32 for a single address, 2001:db8::/32 for a range), comma-separated", raw)
		}
		out = append(out, network)
	}
	return out, nil
}

// TrustedProxyNets returns the extra trusted networks, parsed. validate() has
// already refused anything that does not parse, so an error here can only come
// from a Config built by hand (a test) and the bad entry is skipped rather than
// panicking a running server over it.
func (c *Config) TrustedProxyNets() []*net.IPNet {
	nets, err := parseTrustedProxyCIDRs(c.TrustedProxyCIDRs)
	if err == nil {
		return nets
	}
	out := make([]*net.IPNet, 0, len(c.TrustedProxyCIDRs))
	for _, raw := range c.TrustedProxyCIDRs {
		if _, network, err := net.ParseCIDR(raw); err == nil {
			out = append(out, network)
		}
	}
	return out
}

// CookieSecure reports whether auth cookies (the vidra_refresh cookie set by
// cookie-mode sessions) must carry the Secure attribute: exactly when browsers
// reach this instance over TLS. See PublicOriginIsHTTPS for what decides that
// and why the unset case is fail-secure — plain-http local development (and a
// consented plain-http deployment) keeps Secure off, since a Secure cookie on an
// http origin is a cookie the browser never sends back.
func (c *Config) CookieSecure() bool {
	return c.PublicOriginIsHTTPS()
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

// envParser reads every environment variable Load needs, from one swappable
// source. A malformed — non-empty but unparseable — typed value is FATAL at
// config load, never a silent fall-back to the default: env files may be
// generated, and a typo must refuse to boot rather than boot in the wrong
// configuration. Each typed getter records the error (in validate()'s
// "config: VAR ..." idiom, attributed via VarError) and returns the default so
// parsing can continue; LoadFrom checks Err() once every variable has been
// read, reporting all malformed variables at once.
type envParser struct {
	// lookup answers one variable, reporting ok=false when it is not set.
	// os.LookupEnv for Load; a map read for CheckEnv.
	lookup func(key string) (string, bool)
	errs   []error
}

// Str and the typed getters share one contract: an UNSET or EMPTY variable
// means "use the default" (so `KEY=` in an env file is the same as omitting
// KEY). Str returns strings verbatim and so can never fail; every typed getter
// can, which is why they all live on envParser. LoadFrom binds Str to the local
// name getEnv, so a plain string read is one call shorter to write.
func (p *envParser) Str(key, def string) string {
	if v, ok := p.lookup(key); ok && v != "" {
		return v
	}
	return def
}

// Err returns every malformed-variable error recorded while parsing, joined,
// or nil when the environment parsed cleanly.
func (p *envParser) Err() error {
	return errors.Join(p.errs...)
}

func (p *envParser) Int(key string, def int) int {
	v, ok := p.lookup(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.errs = append(p.errs, varErrorf(key, "config: %s must be an integer: %w", key, err))
		return def
	}
	return n
}

func (p *envParser) Int64(key string, def int64) int64 {
	v, ok := p.lookup(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		p.errs = append(p.errs, varErrorf(key, "config: %s must be an integer: %w", key, err))
		return def
	}
	return n
}

func (p *envParser) Bool(key string, def bool) bool {
	v, ok := p.lookup(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.errs = append(p.errs, varErrorf(key, "config: %s must be a boolean (true|false): %w", key, err))
		return def
	}
	return b
}

func (p *envParser) Duration(key string, def time.Duration) time.Duration {
	v, ok := p.lookup(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		p.errs = append(p.errs, varErrorf(key, "config: %s must be a duration (e.g. 30s, 5m, 12h): %w", key, err))
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
