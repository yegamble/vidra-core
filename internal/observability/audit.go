// Package observability holds cross-cutting logging/audit helpers shared by the
// HTTP and service layers. See .ralph/specs/observability.md.
package observability

import (
	"context"
	"log/slog"
	"strings"
	"unicode/utf8"
)

// Audit results classify the outcome of a security-sensitive action.
const (
	ResultSuccess = "success"
	ResultFailure = "failure"
)

// Audited actions are stable, dot-namespaced identifiers for security events.
// Keep these in sync with the audit-event table tracked in fix_plan P15/P17.
const (
	ActionLogin                 = "auth.login"
	ActionLogout                = "auth.logout"
	ActionLogoutAll             = "auth.logout_all"
	ActionRegister              = "auth.register"
	ActionOwnerClaim            = "auth.owner_claim"
	ActionRegistrationRequest   = "auth.registration.request"
	ActionRegistrationApprove   = "auth.registration.approve"
	ActionRegistrationReject    = "auth.registration.reject"
	ActionPasswordResetRequest  = "auth.password_reset.request"
	ActionPasswordResetComplete = "auth.password_reset.complete"
	ActionEmailVerifyRequest    = "auth.email_verify.request"
	ActionEmailVerifyConfirm    = "auth.email_verify.confirm"
	ActionAccountDeactivate     = "auth.account.deactivate"
	ActionAccountDelete         = "auth.account.delete"
	ActionAdminUserDelete       = "admin.user.delete"
	ActionOAuthLink             = "auth.oauth.link"
	ActionOAuthUnlink           = "auth.oauth.unlink"
	ActionMFAEnable             = "auth.mfa.enable"
	ActionMFADisable            = "auth.mfa.disable"
	ActionMFAChallenge          = "auth.mfa.challenge"
	ActionRateLimited           = "auth.rate_limited"
	ActionReportResolve         = "moderation.report.resolve"
	ActionReportDelete          = "moderation.report.delete"
	ActionVideoBlock            = "moderation.video.block"
	ActionVideoUnblock          = "moderation.video.unblock"
	ActionVideoApprove          = "moderation.video.quarantine_approve"
	ActionVideoReject           = "moderation.video.quarantine_reject"
	ActionInstanceBlock         = "moderation.instance.block"
	ActionInstanceUnblock       = "moderation.instance.unblock"
	ActionRemoteVideoBlock      = "moderation.remote_video.block"
	ActionRemoteVideoUnblock    = "moderation.remote_video.unblock"
	ActionAdminUserUpdate       = "admin.user.update"
	// ActionAdminInstanceUpdate records an admin changing the DB-backed instance
	// settings overlay (fix_plan P10). Reason carries the changed KEY NAMES only
	// — never the values, which can include the operator contact email.
	ActionAdminInstanceUpdate = "admin.instance.update"
	// ActionAdminInstanceDocumentUpdate records an admin writing/clearing an
	// instance document (homepage / custom_css / custom_js, config-parity W1).
	// Reason carries the document NAME and the new content sha256 only — never
	// the body (custom JS/CSS is operator-authored code).
	ActionAdminInstanceDocumentUpdate = "admin.instance_document.update"
	// ActionAdminInstanceAssetUpdate / Delete record an admin uploading or
	// removing an instance branding image (avatar/banner/logo slots,
	// config-parity W1). Reason carries the asset kind only.
	ActionAdminInstanceAssetUpdate = "admin.instance_asset.update"
	ActionAdminInstanceAssetDelete = "admin.instance_asset.delete"
	ActionVideoUpdate              = "content.video.update"
	ActionVideoDelete              = "content.video.delete"
	ActionVideoTranscode           = "content.video.transcode"
	ActionVideoCaptionGenerate     = "content.video.caption_generate"
	ActionChannelDelete            = "content.channel.delete"
	ActionMediaGC                  = "admin.media.gc"
	// ActionIPFSReconcile records an admin kicking the one-shot IPFS mirror
	// reconcile/backfill (P19.6): re-arm dead-lettered pins + seed pin intents for
	// eligible pre-existing public objects. Reason carries only safe counts
	// (enqueued, classes, rearmed) — never a CID or object key.
	ActionIPFSReconcile = "admin.ipfs.reconcile"
	// ActionDonationVerify records a creator proving control of a donation
	// address by signing the challenge (P14). Reason carries the safe address
	// id + network only — never the signature, nonce, or any key material.
	ActionDonationVerify = "content.donation.verify"
	// ActionLiveReplay records the best-effort republish of a recorded live
	// session as a VOD on ingest-stop (P12). Reason carries safe ids/outcome
	// only — never the stream key.
	ActionLiveReplay = "content.live.replay"
	// ActionLiveForceClose records the live_max_duration_secs watchdog
	// force-closing an over-limit live session (config-parity W11). Reason
	// carries the safe stream id only — never the stream key.
	ActionLiveForceClose = "content.live.force_close"
	// ActionUploadMalwareRejected records that the malware scanner (ClamAV,
	// UPLOAD-13) kept an uploaded original out of the published state — an
	// infection, or an unscannable file under a non-publishing policy. Reason
	// carries only the safe video id, the outcome (infected|scan_error), and the
	// applied policy — never the scanned bytes or any file content.
	ActionUploadMalwareRejected = "content.upload.malware_rejected"
	// E2EE one-time-key claims are audited with COUNTS ONLY (never key
	// material): key exhaustion/abuse is a security-relevant signal.
	ActionE2EEClaim = "e2ee.otk.claim"
	// PeerTube import (P18): an admin launching/finishing a migration run. Reason
	// carries only safe metadata (mode, detected version, per-entity counts) —
	// never the source DSN, credentials, or any imported PII/secret.
	ActionPeerTubeImportStart  = "admin.peertube_import.start"
	ActionPeerTubeImportFinish = "admin.peertube_import.finish"
	// Federation follower-approval queue decisions (config-parity W12,
	// federation_follower_approval): an admin approving/rejecting a pending
	// inbound channel Follow. Reason carries the safe follow row id only —
	// never the activity payload.
	ActionFederationFollowerApprove = "admin.federation.follower_approve"
	ActionFederationFollowerReject  = "admin.federation.follower_reject"
)

// sensitiveKeys is the canonical denylist of structured-log field names that
// must never appear in an audit event or any log/trace/metric label. Mirrors the
// security-sensitive list in .ralph/specs/observability.md.
var sensitiveKeys = map[string]bool{
	"password":           true,
	"password_hash":      true,
	"token":              true,
	"refresh_token":      true,
	"access_token":       true,
	"reset_token":        true,
	"verification_token": true,
	"authorization":      true,
	"cookie":             true,
	"secret":             true,
	"secret_key":         true,
	"smtp_password":      true,
	"client_secret":      true,
	// ATProto / Bluesky (P10.2): the linked app password (and its sealed form)
	// are secrets — never log, span-tag, or return them.
	"app_password":        true,
	"app_password_sealed": true,
	"id_token":            true,
	"code_verifier":       true,
	"access_key":          true,
	"private_key":         true,
	"private_key_pem":     true,
	"kek":                 true,
	"stream_key":          true,
	"jwt":                 true,
	"totp_secret":         true,
	"totp_secret_sealed":  true,
	"otpauth_uri":         true,
	"mfa_token":           true,
	"recovery_code":       true,
	"recovery_codes":      true,
	// E2EE (P11.2): envelope ciphertext and one-time prekeys are opaque
	// client material — never log them. (identity_key/signing_key are public
	// keys, but logging them serves no purpose either; keep them out too.)
	"ciphertext":    true,
	"envelope":      true,
	"envelopes":     true,
	"one_time_key":  true,
	"one_time_keys": true,
	"identity_key":  true,
	"signing_key":   true,
	// PeerTube import (P18): the read-only SOURCE database DSN carries a password,
	// and the source S3 credentials are secrets — never log the source connection.
	"source_dsn":          true,
	"source_database_url": true,
	"peertube_source_dsn": true,
	"source_secret_key":   true,
	"source_access_key":   true,
	// IPFS media mirroring (P19): the IPFS Cluster Bearer token is a secret —
	// never log, span-tag, or return it. P19.P adds the private-swarm cluster token.
	"ipfs_cluster_token":         true,
	"ipfs_private_cluster_token": true,
	"cluster_token":              true,
}

// IsSensitiveKey reports whether a structured-log key is on the denylist
// (case-insensitive). It is the canonical check callers and tests use to keep
// secrets out of logs and audit events.
func IsSensitiveKey(key string) bool { return sensitiveKeys[strings.ToLower(key)] }

// AuditEvent is a typed, security-sensitive event, emitted distinct from request
// logs (marked audit=true). It must never carry secrets or unnecessary PII:
// actors are identified by ID, never by email, and Reason must be a safe,
// non-sensitive classification (e.g. "invalid_credentials"), never a token.
type AuditEvent struct {
	SchemaVersion int16  // typed-envelope version; 2 for newly emitted events
	Domain        string // stable action domain (auth, admin, moderation, content...)
	Action        string // one of the Action* constants
	Result        string // ResultSuccess or ResultFailure
	ActorID       string // user UUID; empty for anonymous/system/service actors
	ActorKind     string // anonymous|user|system|service
	ActorRole     string // user|moderator|admin; user actors only
	RequestID     string // correlates with request logs
	CorrelationID string // stable cross-service correlation id
	TraceID       string // W3C trace id when tracing is active
	PipelineRunID string // unified job pipeline id, when applicable
	JobID         string // unified job id, when applicable
	ResourceType  string // bounded resource classification
	ResourceID    string // bounded opaque id; never a URL
	Reason        string // safe, bounded classification; omitted when empty
}

// Audit emits ev on logger at info level. The slog record's timestamp is the
// event's occurred_at. A nil logger falls back to the default.
func Audit(ctx context.Context, logger *slog.Logger, ev AuditEvent) {
	if logger == nil {
		logger = slog.Default()
	}
	if ev.SchemaVersion == 0 {
		ev.SchemaVersion = 2
	}
	if ev.Domain == "" {
		ev.Domain = auditDomain(ev.Action)
	}
	if ev.ActorKind == "" {
		if ev.ActorID == "" {
			ev.ActorKind = "anonymous"
		} else {
			ev.ActorKind = "user"
		}
	}
	args := []any{
		"audit", true,
		"schema_version", ev.SchemaVersion,
		"domain", ev.Domain,
		"action", ev.Action,
		"result", ev.Result,
		"actor_kind", ev.ActorKind,
	}
	if ev.ActorID != "" {
		args = append(args, "actor_id", ev.ActorID)
	}
	if ev.RequestID != "" {
		args = append(args, "request_id", ev.RequestID)
	}
	if ev.ActorRole != "" {
		args = append(args, "actor_role", ev.ActorRole)
	}
	if ev.CorrelationID != "" {
		args = append(args, "correlation_id", ev.CorrelationID)
	}
	if ev.TraceID != "" {
		args = append(args, "trace_id", ev.TraceID)
	}
	if ev.PipelineRunID != "" {
		args = append(args, "pipeline_run_id", ev.PipelineRunID)
	}
	if ev.JobID != "" {
		args = append(args, "job_id", ev.JobID)
	}
	if ev.ResourceType != "" {
		args = append(args, "resource_type", ev.ResourceType)
	}
	if ev.ResourceID != "" {
		args = append(args, "resource_id", ev.ResourceID)
	}
	if ev.Reason != "" {
		args = append(args, "reason", boundedAuditText(ev.Reason, 512))
	}
	logger.InfoContext(ctx, "audit", args...)
}

func auditDomain(action string) string {
	if i := strings.IndexByte(action, '.'); i > 0 {
		return action[:i]
	}
	return "legacy"
}

func boundedAuditText(value string, maxBytes int) string {
	value = strings.ToValidUTF8(strings.TrimSpace(value), "")
	for len(value) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value
}
