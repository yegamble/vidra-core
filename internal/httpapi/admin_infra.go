package httpapi

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/bytes"
)

// --- GET /api/v1/admin/infrastructure ---
//
// The admin status page answers "is it up". This answers "what is it" — the
// deploy-time shape an operator cannot see from inside the product: which
// storage backend is in force, whether the public origin is really https,
// which optional subsystems are wired, and where their backups come from.
// Without it the only way to check any of that is to SSH to the host and read
// an env file, which is exactly the gap phase-1 item 20 exists to close.
//
// EVERY field here is hand-picked and carries a comment saying why it is safe
// to show. That is deliberate: a struct that mirrors config.Config
// automatically would leak the next secret somebody adds to config. The
// never-list is absolute — no DSN (DATABASE_URL, REDIS_URL,
// PEERTUBE_SOURCE_DATABASE_URL), no S3 access/secret key, no SMTP username or
// password, no JWT_SECRET, no *_KEK, no LIVE_INGEST_SECRET, no
// SEARCH_INTERNAL_SECRET, no IPFS cluster token. A test asserts none of them
// appears in the body.
//
// It reads BOOT CONFIG, never the instance-settings overlay: this page is about
// what the deployment is, and an admin toggle that says "live streaming on"
// while no RTMP ingest exists is the sentence this page has to be able to
// contradict. The runtime toggles live on /admin/config and GET /instance.

// infraServer is the process's own shape: the environment it believes it is in,
// the request deadlines and size caps it enforces, and whether the two
// observability surfaces are mounted. All of it is operational metadata an
// operator sets themselves — nothing here is a credential.
type infraServer struct {
	// Environment is VIDRA_ENV (development|test|production). It decides a
	// dozen fail-secure defaults, so an operator who thinks they deployed
	// production and did not needs to see it in one place.
	Environment string `json:"environment"`
	// RequestTimeoutSeconds / StreamRequestTimeoutSeconds are the per-request
	// deadlines. The stream one applies to uploads and media reads, which are
	// bounded by the client's link speed rather than server work — a slow
	// upload that keeps failing is usually this number, not a bug.
	RequestTimeoutSeconds       int64 `json:"request_timeout_seconds"`
	StreamRequestTimeoutSeconds int64 `json:"stream_request_timeout_seconds"`
	// BodyLimit is HTTP_BODY_LIMIT verbatim (an Echo size string, e.g. "8M"):
	// the point where a request is refused with 413.
	BodyLimit string `json:"body_limit"`
	// UploadMaxBytes is the effective single-upload cap in bytes (0 = no cap),
	// parsed from the boot-validated UPLOAD_MAX_SIZE. The most-asked operator
	// question about a rejected upload.
	UploadMaxBytes int64 `json:"upload_max_bytes"`
	// MetricsEnabled reports whether GET /metrics is mounted. It is worth
	// showing precisely because that endpoint has NO authentication of its own:
	// an operator who did not expect it to be on needs to know it is.
	MetricsEnabled bool `json:"metrics_enabled"`
	// TracingEnabled reports whether OpenTelemetry export is on, and
	// TracingProtocol which wire format it uses (grpc | http/protobuf). The
	// exporter ENDPOINT is deliberately absent: it is an internal address, and
	// naming internal hosts in an admin page is free reconnaissance.
	TracingEnabled  bool   `json:"tracing_enabled"`
	TracingProtocol string `json:"tracing_protocol"`
}

// infraStorage is where media bytes actually live. The bucket's LOCATION is
// operational (an operator has to know which bucket they are filling); the
// credentials that open it are not, and STORAGE_S3_ACCESS_KEY /
// STORAGE_S3_SECRET_KEY are absent from this struct on purpose.
type infraStorage struct {
	// Backend is "local" or "s3".
	Backend string `json:"backend"`
	// LocalRoot is the container path media is written to on the local backend
	// ("" on s3). It is a path inside the api container, not a host secret, and
	// it is what an operator maps to a volume.
	LocalRoot string `json:"local_root"`
	// S3Endpoint/S3Bucket/S3Region identify the object store ("" on local).
	// These are the coordinates an operator already has in their provider
	// console — they identify the bucket, they do not open it.
	S3Endpoint string `json:"s3_endpoint"`
	S3Bucket   string `json:"s3_bucket"`
	S3Region   string `json:"s3_region"`
	// S3UseSSL / S3ForcePathStyle are the two transport switches that most
	// often explain "the bucket is right there and it still will not connect".
	S3UseSSL         bool `json:"s3_use_ssl"`
	S3ForcePathStyle bool `json:"s3_force_path_style"`
}

// infraNetworking is how the outside world reaches this instance. Every field
// is already observable from a browser (the origin, whether it is TLS, which
// origins are allowed) except the trusted-proxy list, which is included because
// getting it wrong is invisible and severe: an unnamed public edge makes every
// visitor share one rate-limit bucket, and a WRONGLY named one hands everybody
// behind it the ability to forge a client address.
type infraNetworking struct {
	// PublicBaseURL is the canonical public origin ("" when unset).
	PublicBaseURL string `json:"public_base_url"`
	// HTTPSEffective is PublicOriginIsHTTPS() — the ONE predicate Secure
	// cookies and HSTS both hang off. It can disagree with the origin's scheme
	// only when the origin is unset, which is precisely the case worth showing.
	HTTPSEffective bool `json:"https_effective"`
	// AllowPlainHTTP is the operator's explicit consent to a plain-http
	// production origin (a lab/LAN/air-gapped install). True on a public
	// deployment is a finding, so it is shown rather than inferred.
	AllowPlainHTTP bool `json:"allow_plain_http"`
	// TrustedProxyCIDRs are the EXTRA networks whose X-Forwarded-For is
	// believed, on top of the built-in loopback/private set. Never nil.
	TrustedProxyCIDRs []string `json:"trusted_proxy_cidrs"`
	// CORSAllowedOrigins is the browser-origin allow-list. Never nil.
	CORSAllowedOrigins []string `json:"cors_allowed_origins"`
	// FederationEnabled / ATProtoEnabled / ATProtoLoginEnabled are the three
	// switches that decide whether this instance talks to other networks at
	// all. They belong here rather than in features because they are also
	// answers to "what can reach me": federation opens an inbound ActivityPub
	// inbox, the ATProto pair does not.
	FederationEnabled   bool `json:"federation_enabled"`
	ATProtoEnabled      bool `json:"atproto_enabled"`
	ATProtoLoginEnabled bool `json:"atproto_login_enabled"`
}

// infraBackups is GUIDANCE, not state — and the distinction is the whole design
// of this block.
//
// The api runs in a container that has no view of the deploy directory: the
// backup script writes to ./backups on the HOST, which is not mounted here and
// must not be (mounting the backup directory into the public-facing service is
// how a compromised api deletes the backups). So this endpoint provably cannot
// report when the last dump ran or how big it was. `vidra doctor`, which runs
// on the host beside the files, can — and says so.
//
// What the api CAN do is tell the operator what is supposed to be happening and
// where to look, which is most of the value: the commonest backup failure is
// not a corrupt dump, it is nobody having set one up.
type infraBackups struct {
	// ExternalPostgres is VIDRA_EXTERNAL_POSTGRES: the operator's declaration
	// that the database is somebody else's to snapshot. It flips which advice
	// below applies, and nothing else in the api reads it.
	ExternalPostgres bool `json:"external_postgres"`
	// ScheduleNote, StalenessNote and ArtifactsNote are the documented backup
	// contract in the operator's words: the cadence, the age at which a backup
	// counts as stale, and what a complete backup consists of.
	ScheduleNote  string `json:"schedule_note"`
	StalenessNote string `json:"staleness_note"`
	ArtifactsNote string `json:"artifacts_note"`
	// LiveStateNote names the one tool that can answer "did it actually run".
	LiveStateNote string `json:"live_state_note"`
}

// infraFeature is one optional subsystem's honest state.
//
// Enabled is "the operator turned this on"; Configured is "the deployment
// supplies what it needs to work". The pair exists because they come apart, and
// the gap is the interesting case: WHISPER_ENABLED with no endpoint, ATPROTO
// with no sealing key, MAIL_ENABLED behind a relay nobody set. A single
// boolean would have to pick one of those to lie about.
//
// Note is present when there is something to say — usually "this exists and you
// are not using it", which is the discovery half of the page: an operator
// cannot turn on a feature they never knew shipped.
type infraFeature struct {
	Key        string `json:"key"`
	Enabled    bool   `json:"enabled"`
	Configured bool   `json:"configured"`
	Note       string `json:"note,omitempty"`
}

// infrastructureResponse is the admin infrastructure document.
type infrastructureResponse struct {
	Server     infraServer     `json:"server"`
	Storage    infraStorage    `json:"storage"`
	Networking infraNetworking `json:"networking"`
	Backups    infraBackups    `json:"backups"`
	Features   []infraFeature  `json:"features"`
}

// The backup guidance, kept as constants so the copy an operator reads here is
// the same sentence the docs and `vidra doctor` use.
const (
	backupScheduleNote  = "deploy/backup.sh runs nightly at 03:15 local time via the vidra-backup systemd timer (with up to 15 minutes of jitter, so the whole fleet does not dump at once). It keeps 14 daily plus 8 weekly generations."
	backupStalenessNote = "A successful backup older than 26 hours means the schedule stopped. The window is deliberately generous — a daily run plus jitter plus clock drift all fit inside it — so anything past it is a real failure, not a late night."
	backupArtifactsNote = "One run writes TWO artifacts, and a restore needs both: the Postgres dump (vidra-*.dump.gz) and the configuration archive (vidra-config-*.tar.gz — the env file and the generated Caddyfile, which hold JWT_SECRET and the KEKs in plaintext and are therefore written mode 600). MEDIA is not in either one: on the local backend snapshot the media volume on the same cadence, and on object storage rely on the bucket's own versioning or lifecycle rules."
	backupLiveStateNote = "Run `vidra doctor` on the host to see whether the last backup actually ran, how old it is, and whether the timer is even installed. This page cannot answer that: the api container has no view of the deploy directory, and mounting the backups into the public-facing service is how a compromised api deletes them."
	backupExternalNote  = "This instance declares an EXTERNAL Postgres (VIDRA_EXTERNAL_POSTGRES=true), so the stack's own dump does not run — deploy/backup.sh execs pg_dump inside the bundled container and refuses without one. Your provider's automated snapshots are the backup: confirm they are enabled, confirm point-in-time restore, and confirm somebody has actually restored one at least once."
)

// handleInfrastructure returns the deploy-time shape of this instance. Behind
// requireRole(admin). Read-only and always 200 — there is nothing to probe and
// nothing that can fail; every value is already in memory.
func (s *Server) handleInfrastructure(c echo.Context) error {
	cfg := s.cfg
	uploadMax, _ := bytes.Parse(cfg.UploadMaxSize) // boot-validated, so err is unreachable

	backups := infraBackups{
		ExternalPostgres: cfg.ExternalPostgres,
		ScheduleNote:     backupScheduleNote,
		StalenessNote:    backupStalenessNote,
		ArtifactsNote:    backupArtifactsNote,
		LiveStateNote:    backupLiveStateNote,
	}
	if cfg.ExternalPostgres {
		backups.ScheduleNote = backupExternalNote
	}

	return c.JSON(http.StatusOK, infrastructureResponse{
		Server: infraServer{
			Environment:                 cfg.Environment,
			RequestTimeoutSeconds:       int64(cfg.HTTPRequestTimeout.Seconds()),
			StreamRequestTimeoutSeconds: int64(cfg.HTTPStreamRequestTimeout.Seconds()),
			BodyLimit:                   cfg.HTTPBodyLimit,
			UploadMaxBytes:              uploadMax,
			MetricsEnabled:              cfg.MetricsEnabled,
			TracingEnabled:              cfg.OTelEnabled,
			TracingProtocol:             cfg.OTelExporterProtocol,
		},
		Storage: infraStorage{
			Backend:          cfg.StorageBackend,
			LocalRoot:        cfg.StorageLocalRoot,
			S3Endpoint:       cfg.StorageS3Endpoint,
			S3Bucket:         cfg.StorageS3Bucket,
			S3Region:         cfg.StorageS3Region,
			S3UseSSL:         cfg.StorageS3UseSSL,
			S3ForcePathStyle: cfg.StorageS3ForcePathStyle,
		},
		Networking: infraNetworking{
			PublicBaseURL:       cfg.PublicBaseURL,
			HTTPSEffective:      cfg.PublicOriginIsHTTPS(),
			AllowPlainHTTP:      cfg.AllowPlainHTTP,
			TrustedProxyCIDRs:   nonNilStrings(cfg.TrustedProxyCIDRs),
			CORSAllowedOrigins:  nonNilStrings(cfg.CORSAllowedOrigins),
			FederationEnabled:   cfg.FederationEnabled,
			ATProtoEnabled:      cfg.ATProtoEnabled,
			ATProtoLoginEnabled: cfg.ATProtoLoginEnabled,
		},
		Backups:  backups,
		Features: s.infraFeatures(),
	})
}

// infraFeatures is the discovery list: every optional subsystem this build
// ships, whether it is on, and whether the deployment gave it what it needs.
//
// It is computed from BOOT FLAGS the api owns, because that is the only honest
// signal available from inside the container — compose profiles, host cron
// entries and the reverse proxy are all invisible here, and a page that guessed
// at them would be confidently wrong. The list is fixed and ordered, so the
// admin UI renders a stable page rather than one that reshuffles per request.
//
// DASH and CDN are deliberately ABSENT. Neither exists in vidra: playback is
// HLS (plus the progressive original), and there is no CDN integration to
// enable or report. Listing them as "available, not configured" would invent
// two features and send an operator looking for switches that do not exist.
func (s *Server) infraFeatures() []infraFeature {
	cfg := s.cfg
	transcodeCapable := s.transcodesvc != nil && s.transcodesvc.Capable()

	features := []infraFeature{
		{
			Key: "object_storage",
			// An s3 backend cannot boot without endpoint, bucket and both keys
			// (config validates it), so "in force" and "configured" are the
			// same question here.
			Enabled:    cfg.StorageBackend == "s3",
			Configured: cfg.StorageBackend == "s3",
			Note:       "",
		},
		{
			Key: "mail",
			// The mailer is wired whenever ANY outbound path exists (a relay,
			// or the dev capture seam), which is the deployment's real
			// capability; Configured is the narrower "a real relay is set".
			Enabled:    s.contactMailer != nil,
			Configured: cfg.MailEnabled && strings.TrimSpace(cfg.SMTPHost) != "" && strings.TrimSpace(cfg.SMTPFrom) != "",
			// Mail is the one feature whose note cannot be written from
			// (enabled, configured) alone — see mailDevCaptureNote. Empty in
			// every ordinary case, which leaves the generic notes in charge.
			Note: mailDevCaptureNote(s.contactMailer != nil, cfg.MailEnabled),
		},
		{
			Key:        "search",
			Enabled:    s.searchClient != nil,
			Configured: cfg.SearchServiceEnabled(),
		},
		{
			Key:        "federation",
			Enabled:    cfg.FederationEnabled,
			Configured: cfg.FederationEnabled,
		},
		{
			Key:     "atproto",
			Enabled: cfg.ATProtoEnabled,
			// Linked Bluesky app passwords are sealed with a KEK. Without one
			// the feature runs unsealed in development and refuses to boot in
			// production, so the key IS the configured question.
			Configured: cfg.ATProtoKEK() != "",
		},
		{
			Key:     "atproto_login",
			Enabled: cfg.ATProtoLoginEnabled,
			// Identity-only: it keeps no PDS tokens and needs no key, so there
			// is nothing further to configure.
			Configured: cfg.ATProtoLoginEnabled,
		},
		{
			Key:        "malware_scan",
			Enabled:    cfg.MalwareScanEnabled,
			Configured: strings.TrimSpace(cfg.ClamAVAddr) != "",
		},
		{
			Key:        "captions",
			Enabled:    cfg.WhisperEnabled,
			Configured: strings.TrimSpace(cfg.WhisperEndpoint) != "",
		},
		{
			Key:     "live",
			Enabled: cfg.LiveEnabled,
			// A live plane needs somewhere for the RTMP media server to write
			// HLS segments and a URL to hand streamers. The toggle alone
			// produces streams nobody can publish to.
			Configured: strings.TrimSpace(cfg.LiveHLSRoot) != "" && strings.TrimSpace(cfg.LiveRTMPURL) != "",
		},
		{
			Key:        "ipfs",
			Enabled:    s.ipfsConfigured(),
			Configured: s.ipfsConfigured(),
		},
		{
			Key:        "tracing",
			Enabled:    cfg.OTelEnabled,
			Configured: strings.TrimSpace(cfg.OTelExporterEndpoint) != "",
		},
		{
			Key:        "metrics",
			Enabled:    cfg.MetricsEnabled,
			Configured: cfg.MetricsEnabled,
		},
		{
			Key:     "vp9_alternates",
			Enabled: cfg.TranscodingVP9Enabled,
			// VP9 alternates are produced by the HLS pipeline, so they need the
			// same ffmpeg the ladder does.
			Configured: transcodeCapable,
		},
	}

	// The notes are attached separately so the table above stays a table: what
	// each feature IS, then what to say about it. An entry that already carries
	// a note said something only it could say, and keeps it.
	for i := range features {
		if features[i].Note != "" {
			continue
		}
		features[i].Note = infraFeatureNote(features[i])
	}
	return features
}

// mailDevCaptureNote covers the one quadrant the generic notes get wrong.
//
// The dev capture seam is a real outbound path — it is how the local and e2e
// stacks "send" — so enabled is true while MAIL_ENABLED is unset. That lands in
// the enabled-but-unconfigured slot, whose note reads "MAIL_ENABLED is set but
// the relay is not fully configured": both halves false. Reporting enabled=false
// instead would be the other lie, since messages really are being handled.
//
// So the quadrant gets its own sentence, and returns "" everywhere else — the
// enabled+MAIL_ENABLED case is a genuinely incomplete relay and the generic
// misconfigured note is exactly right for it.
func mailDevCaptureNote(hasMailer, mailEnabled bool) string {
	if !hasMailer || mailEnabled {
		return ""
	}
	return "Mail is CAPTURED, not delivered: this deployment runs the development mail seam with MAIL_ENABLED unset, so password resets and verification links are held in memory for the local test harness and never leave the process. That is correct for a developer machine and wrong everywhere else — set MAIL_ENABLED=true with SMTP_HOST, SMTP_PORT and SMTP_FROM before anybody outside it relies on email."
}

// infraFeatureNote is the operator-facing sentence for one feature: what the
// gap is and what closing it would buy. A fully-configured, switched-on feature
// gets no note — the success pill already says everything.
func infraFeatureNote(f infraFeature) string {
	switch {
	case f.Enabled && f.Configured:
		return ""
	case f.Enabled && !f.Configured:
		// The dangerous half: the switch says yes and the dependency is
		// missing, so the feature is on paper only.
		return infraFeatureMisconfiguredNotes[f.Key]
	default:
		return infraFeatureOffNotes[f.Key]
	}
}

// infraFeatureOffNotes is the discovery copy: "this ships, you are not using
// it, here is what it would do". Every entry names the variable to set, because
// a note an operator cannot act on is decoration.
var infraFeatureOffNotes = map[string]string{
	"object_storage": "Media is stored on the api container's filesystem, which is bounded by the host's disk and does not survive re-creating the volume. Connect object storage (STORAGE_BACKEND=s3 plus endpoint, bucket and keys) before the library outgrows the disk.",
	"mail":           "This deployment cannot send email, so password resets, address verification and operator alerts are silently dropped. Set MAIL_ENABLED=true with SMTP_HOST, SMTP_PORT and SMTP_FROM.",
	"search":         "Search falls back to a local database query: it works, but there is no ranking, no typo tolerance and no cross-instance results. Run the vidra-search service and point SEARCH_SERVICE_URL at it.",
	"federation":     "This instance is not on the fediverse: nobody on Mastodon or PeerTube can follow its channels, and its videos do not appear on other instances. Set FEDERATION_ENABLED=true with PUBLIC_BASE_URL and FEDERATION_KEY_KEK.",
	"atproto":        "Creators cannot cross-post to Bluesky. Set ATPROTO_ENABLED=true (and ATPROTO_KEY_KEK, which seals their linked app passwords at rest).",
	"atproto_login":  "Visitors cannot sign in with a Bluesky or other atproto account. Set ATPROTO_LOGIN_ENABLED=true — it is identity-only and needs no key.",
	"malware_scan":   "Uploaded files are published without being scanned. Point CLAMAV_ADDR at a ClamAV daemon and set MALWARE_SCAN_ENABLED=true to scan every original before it goes live.",
	"captions":       "Videos get no automatic captions, so they are unsearchable by spoken content and inaccessible to viewers who need subtitles. Run a Whisper-compatible service and set WHISPER_ENABLED=true with WHISPER_ENDPOINT.",
	"live":           "Live streaming is off. Turning it on needs an RTMP media server as well as the toggle: LIVE_RTMP_URL for streamers to publish to and LIVE_HLS_ROOT for the segments it writes.",
	"ipfs":           "Media is served only from this instance's storage. Enabling the IPFS mirror (IPFS_ENABLED with IPFS_API_URL and IPFS_GATEWAY_URL) replicates public media to a content-addressed network so it survives this server.",
	"tracing":        "There are no distributed traces, so a slow request can only be investigated from logs. Set OTEL_ENABLED=true with OTEL_EXPORTER_OTLP_ENDPOINT to export spans to a collector.",
	"metrics":        "No Prometheus metrics are exposed. METRICS_ENABLED=true mounts GET /metrics — note that endpoint has NO authentication of its own, so the reverse proxy must block it before you expose this instance publicly.",
	"vp9_alternates": "Only H.264 is produced. TRANSCODING_VP9_ENABLED=true additionally emits a VP9/WebM download alternate, which is smaller at the same quality but costs a second encode of every upload.",
}

// infraFeatureMisconfiguredNotes is the other half: the switch is on and the
// dependency is not there, so the feature is quietly inert. These are findings,
// not suggestions.
//
// There is deliberately no "mail" entry. config refuses to BOOT with
// MAIL_ENABLED and an incomplete relay (SMTP_HOST and SMTP_FROM are both
// required), so a running instance can only be enabled-but-unconfigured for
// mail via the dev capture seam — which mailDevCaptureNote owns. A note here
// for a state that cannot exist would be copy nobody ever reads and nobody ever
// checks, which is how it becomes wrong.
var infraFeatureMisconfiguredNotes = map[string]string{
	"search":         "A search client is wired but SEARCH_SERVICE_URL is empty, so every query is falling back to the local database.",
	"atproto":        "ATPROTO_ENABLED is set with no ATPROTO_KEY_KEK or FEDERATION_KEY_KEK, so linked Bluesky app passwords would be stored unsealed. Production refuses to boot in this state; fix it before promoting this instance.",
	"malware_scan":   "MALWARE_SCAN_ENABLED is set with no CLAMAV_ADDR, so nothing is being scanned.",
	"captions":       "WHISPER_ENABLED is set with no WHISPER_ENDPOINT, so caption requests answer 503.",
	"live":           "Live streaming is on but the ingest plane is incomplete: LIVE_RTMP_URL tells streamers where to publish and LIVE_HLS_ROOT is where the media server writes segments. Without both, streams can be created and never started.",
	"tracing":        "OTEL_ENABLED is set with no OTEL_EXPORTER_OTLP_ENDPOINT, so spans are produced and discarded.",
	"vp9_alternates": "VP9 alternates are enabled but the transcoding pipeline is not available (ffmpeg/ffprobe missing, or TRANSCODING_ENABLED is off), so no alternate is ever produced.",
}

// nonNilStrings returns a slice that marshals as [] rather than null, so a
// client never has to distinguish "no entries" from "field missing".
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
