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
	ActionPasswordChange        = "auth.password.change"
	// ActionPasswordSet records a PASSWORD-LESS account acquiring its first
	// password, authorised by a step-up assertion rather than a current
	// password (see auth/stepup.go). Distinct from auth.password.change: the
	// authorisation is different, and "this account gained a second sign-in
	// method" is the event an operator reading the trail wants to find.
	ActionPasswordSet = "auth.password.set"
	// ActionStepUpGrant records a completed (or refused) provider
	// re-authentication. The reason names the provider or the rule that
	// refused — never a DID, handle, subject or token.
	ActionStepUpGrant        = "auth.step_up.grant"
	ActionEmailVerifyRequest = "auth.email_verify.request"
	ActionEmailVerifyConfirm = "auth.email_verify.confirm"
	// Two-step email change (AUTH-05). No event carries either address — the
	// sensitive-key discipline keeps email addresses out of the audit trail, so
	// the actor id is the whole record.
	ActionEmailChangeRequest = "auth.email_change.request"
	ActionEmailChangeConfirm = "auth.email_change.confirm"
	ActionEmailChangeCancel  = "auth.email_change.cancel"
	ActionAccountDeactivate  = "auth.account.deactivate"
	ActionAccountDelete      = "auth.account.delete"
	ActionAdminUserDelete    = "admin.user.delete"
	ActionOAuthLink          = "auth.oauth.link"
	ActionOAuthUnlink        = "auth.oauth.unlink"
	ActionMFAEnable          = "auth.mfa.enable"
	ActionMFADisable         = "auth.mfa.disable"
	ActionMFAChallenge       = "auth.mfa.challenge"
	// ActionAdminMFAReset records an administrator removing a user's second
	// factor (A05 ruling 2) — the operator answer to a lost authenticator. It
	// is an ADMIN-domain action against a target account, distinct from
	// auth.mfa.disable, which the account holder performs on themselves.
	ActionAdminMFAReset = "admin.user.mfa_reset"
	ActionRateLimited   = "auth.rate_limited"
	ActionReportResolve = "moderation.report.resolve"
	// ActionWatchedWordMatchResolve records a moderator triaging one
	// watched-word match (resolved / dismissed). The metadata carries the
	// outcome and whether a note was supplied; the note itself lives on the
	// match row, because audit_log's metadata allowlist rejects prose.
	ActionWatchedWordMatchResolve = "moderation.watched_word_match.resolve"
	ActionReportDelete            = "moderation.report.delete"
	ActionVideoBlock              = "moderation.video.block"
	ActionVideoUnblock            = "moderation.video.unblock"
	ActionVideoApprove            = "moderation.video.quarantine_approve"
	ActionVideoReject             = "moderation.video.quarantine_reject"
	ActionInstanceBlock           = "moderation.instance.block"
	ActionInstanceUnblock         = "moderation.instance.unblock"
	// ActionRemoteActorBlock/Unblock record an admin blocking ONE remote
	// account for every reader on this instance (A29 parity), rather than
	// defederating that account's whole server. The blocked actor URL is the
	// resource, so the trail names who was silenced without a second lookup.
	ActionRemoteActorBlock   = "moderation.remote_actor.block"
	ActionRemoteActorUnblock = "moderation.remote_actor.unblock"
	// ActionFederationInboxRejected records an inbound ActivityPub activity
	// refused because its origin instance is on the admin blocklist (A29-F4).
	// A29 measured the gap: the refusal answered 202 — indistinguishable from
	// acceptance at HTTP level — with no audit row, no log line beyond the plain
	// request line, and no inbox row, so an admin could not tell a block that was
	// working from a peer that had gone quiet. The reason carries `domain=<host>`
	// and nothing else; the activity id, the actor URL and the body never appear
	// (audit_log's metadata allowlist rejects prose, and a refused payload is
	// content this instance deliberately did not accept).
	ActionFederationInboxRejected = "federation.inbox.rejected"
	ActionRemoteVideoBlock        = "moderation.remote_video.block"
	ActionRemoteVideoUnblock      = "moderation.remote_video.unblock"
	ActionAdminUserUpdate         = "admin.user.update"
	// ActionOwnerTransfer records the instance-owner marker moving from one
	// administrator to another (0131 + the A16 ruling). Before it existed the
	// marker had one writer — the first-run claim — and no way to move, so an
	// owner who left stranded the instance unowned. Reason names both parties by
	// id; the structured change is is_owner false->true on the new owner.
	ActionOwnerTransfer = "admin.owner.transfer"
	// ActionAdminInstanceUpdate records an admin changing the DB-backed instance
	// settings overlay (fix_plan P10). Reason carries the changed KEY NAMES only
	// — never the values, which can include the operator contact email.
	ActionAdminInstanceUpdate = "admin.instance.update"
	// ActionAdminInstanceDocumentUpdate records an admin writing/clearing an
	// instance document (homepage / custom_css / custom_js, config-parity W1).
	// Reason carries the document NAME and the new content sha256 only — never
	// the body (custom JS/CSS is operator-authored code).
	ActionAdminInstanceDocumentUpdate = "admin.instance_document.update"
	// ActionAdminMailTest records an admin sending the outbound-mail probe. The
	// probe goes to the instance's own contact address and nowhere else (the
	// caller cannot choose a recipient), and the reason carries the OUTCOME only
	// — sent, mail_not_configured, no_contact_email, send_failed — never an
	// address. It is audited because it is an authenticated action that causes
	// outbound network traffic, which is exactly the shape worth being able to
	// look back at.
	ActionAdminMailTest = "admin.mail.test"
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
	// ActionAuditRetentionPrune is the audit trail recording its own retention
	// sweep. It is written once per run that actually deleted something, with
	// the row count in the `count` metadata field — audit_log carries no prose,
	// so the number is structured or it is nowhere. A run that deleted nothing
	// writes no row: the trail's own bookkeeping must not become the bulk of the
	// trail on a quiet instance.
	ActionAuditRetentionPrune = "admin.audit.retention_prune"
	// ActionMediaGCAdoptBucket records an admin claiming the configured object
	// store for this install — writing the instance identity into the ownership
	// marker, which re-enables DESTRUCTIVE media garbage collection against a
	// bucket boot refused to delete from. Reason carries the resulting ownership
	// state or a failure category only; never the bucket, endpoint or key.
	ActionMediaGCAdoptBucket = "admin.media.gc.adopt_bucket"
	// ActionStorageMigrationStart / Cancel record an admin opening or aborting a
	// campaign that moves the whole media library to another backend (phase-2
	// storage). Reason carries the campaign id and the two store IDENTITY strings
	// (endpoint/bucket or path) — never an access key or secret, which are what
	// authorise a store rather than name it.
	ActionStorageMigrationStart  = "admin.storage.migration.start"
	ActionStorageMigrationCancel = "admin.storage.migration.cancel"
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
	// session as a VOD on ingest-stop (P12). ResourceID carries the live stream
	// and Reason the safe outcome/stage plus the published video id — never the
	// stream key.
	ActionLiveReplay = "content.live.replay"
	// ActionLiveForceClose records the live_max_duration_secs watchdog
	// force-closing an over-limit live session (config-parity W11). ResourceID
	// carries the stream and Reason the code `max_duration` — never the stream
	// key. A26 measured this row landing with an EMPTY resource_id and the id
	// buried in free text, so the audit filter could not target the stream it
	// was about.
	ActionLiveForceClose = "content.live.force_close"
	// ActionLiveEnded records the CREATOR (or one of their channel's content
	// managers) ending their own broadcast with POST /live/{id}/end. It is a
	// separate action from ActionLiveTerminate because a creator stopping their
	// own stream is not a moderation event and must not appear in the trail as
	// one — and because A26 measured the owner's end writing no audit row at
	// all, the only deliberate end of a broadcast that left no trace. Actor is
	// the user + role; ResourceID is the stream; Reason is `owner_ended` plus
	// the partial-outcome markers.
	ActionLiveEnded = "content.live.ended"
	// ActionLiveTerminate records a MODERATOR (or the stream's own owner)
	// deliberately ending a live broadcast. ResourceID carries the stream id —
	// unlike the two live actions above, which A26 measured leaving it empty and
	// burying the id in free-text Reason, so the audit filter could not target a
	// stream. Reason carries the allow-listed reason CODE only (the moderator's
	// free text lives on the live_streams row, where a video block's reason
	// lives) plus, when they occurred, the partial-outcome markers
	// `publisher_not_dropped` / `key_not_rotated` — never the stream key.
	ActionLiveTerminate = "content.live.terminate"
	// ActionUploadMalwareRejected records that the malware scanner (ClamAV,
	// UPLOAD-13) kept an uploaded original out of the published state — an
	// infection, or an unscannable file under a non-publishing policy. Reason
	// carries only the safe video id, the outcome (infected|scan_error), and the
	// applied policy — never the scanned bytes or any file content.
	ActionUploadMalwareRejected = "content.upload.malware_rejected"
	// ActionUploadMalwareSkipped records that a file was PUBLISHED WITHOUT being
	// scanned because the scan could not complete and MALWARE_SCAN_MODE is
	// fail-open. A28 measured this path leaving nothing but a WARN log line, so
	// an instance that spent a week publishing unscanned media had no durable
	// trace of it. Reason carries the safe video id, the reason class
	// (scanner_unavailable) and the applied policy — never the scanner's error,
	// which carries CLAMAV_ADDR, and never any file content.
	ActionUploadMalwareSkipped = "content.upload.malware_scan_skipped"
	// ActionMalwareScanDisabled records, ONCE PER BOOT, that this instance runs
	// with MALWARE_SCAN_MODE=disabled — the explicit opt-out from the
	// scan-by-default posture. An opt-out that lives only in an env file is not
	// reviewable; this row is what makes "we ingest unscanned" a decision
	// somebody can point at afterwards. Reason carries the mode only.
	ActionMalwareScanDisabled = "system.malware_scan.disabled"
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
	// Suggestion bans (search-service moderation): a moderator/admin suppressing
	// a query string from instance-wide autosuggest, or lifting that ban. The
	// domain is `moderation`, not `admin`, because that is what the action IS —
	// the same lever class as blocking a video, held by whoever is on shift.
	// ResourceID carries a FINGERPRINT of the aggregate key, never the query
	// itself: a search query is user-authored free text and free-form content
	// must never enter audit_log. The reviewable plaintext lives in the ban list
	// (GET /admin/search/suggestion-bans), which is the reversal surface.
	ActionSearchSuggestionBan   = "moderation.search.suggestion_ban"
	ActionSearchSuggestionUnban = "moderation.search.suggestion_unban"
	// CDN edge invalidation, per QUEUED JOB OUTCOME (internal/cdnpurge). The
	// A33 rehearsal (2026-09-08) recorded that a purge had no audit action at
	// all: the whole durable record of a takedown reaching — or failing to
	// reach — the edge was a queue row that a retention sweep eventually
	// deletes, one log line, and a process-local counter that a restart resets.
	//
	// They are written for the QUEUE's outcomes and not for the immediate pass,
	// because those are the outcomes no request can report: the immediate
	// fan-out happens inside the deletion or privacy flip that caused it, and
	// that act is already audited (content.video.delete, auth.account.delete,
	// admin.instance.update, content.video.transcode). A queued job finishes
	// minutes or hours later, in another process, long after the request that
	// created it returned 204.
	//
	// dead_lettered is result=failure and is the one an operator must be able
	// to find afterwards: the edge is still serving objects this instance has
	// stopped serving, and nothing will try again. Metadata carries reason_code
	// (the closed job-reason vocabulary), url_count, purged, failed and
	// attempts — counts, never URLs: a purge path can carry an operator's own
	// purge-API credential in the template it was built from, and audit_log
	// carries no prose anywhere.
	ActionCDNPurgeCompleted    = "cdn.purge.completed"
	ActionCDNPurgeDeadLettered = "cdn.purge.dead_lettered"
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
	// CDN delivery (phase-4 item 2): the purge credential. It is sent
	// header-only, and internal/cdn additionally strips the request URL out of
	// transport errors — some purge APIs want the credential in the query
	// string, so the URL is as sensitive as the header here.
	"cdn_purge_token": true,
	// Anonymous search-aggregation subject (httpapi/search_subject.go): a keyed,
	// day-scoped pseudonym of the client address. It is not a credential, but it
	// is address-derived, so logging it next to any other request field would
	// re-link a visitor to their behaviour — exactly what the day scoping exists
	// to prevent. It is a payload field for vidra-search only, never a log key.
	"subject_id": true,
}

// IsSensitiveKey reports whether a key names a SECRET (case-insensitive). It is
// the canonical check callers and tests use to keep credentials out of logs,
// audit events, response bodies and account archives alike — which is why it
// stops at secrets: an account export is supposed to contain the account's own
// email address, and a check that conflated "secret" with "identifying" would
// have to be weakened at that call site to stay usable.
func IsSensitiveKey(key string) bool { return sensitiveKeys[strings.ToLower(key)] }

// identifierLogKeys names values that identify a PERSON rather than authorize
// one. They are legitimate in a database column and in a body the account owner
// asked for; they are not legitimate as a structured-log key, because a log is
// fanned out to an aggregator with a wider audience and a longer memory than the
// column ever had. AGENTS.md rule 6 has always said so — "never log tokens,
// passwords, EMAIL ADDRESSES, message bodies, or report reasons" — and until
// this list existed the mechanical guard enforced only the credential half.
//
// user_id/actor_id are deliberately absent: an opaque account UUID is the
// bounded identifier the audit envelope and every operator surface are built on,
// and denying it would empty the audit trail rather than protect it.
var identifierLogKeys = map[string]bool{
	"email":         true,
	"email_address": true,
	"ip":            true,
	"ip_address":    true,
	"client_ip":     true,
	"remote_addr":   true,
	// A session id is a bearer-adjacent handle to a live session.
	"session_id": true,
	// The QoE viewer pseudonym (internal/qoe/digest.go) is keyed and day-scoped
	// precisely so it cannot follow a viewer across days; logging it beside any
	// other request field would re-link exactly what the scoping separates —
	// the same argument subject_id is on the secret list for.
	"viewer_digest": true,
}

// IsSensitiveLogKey reports whether a key must never appear as a structured-log
// key: every secret, plus the direct identifiers above. TestNoSensitiveLogKeys
// enforces it across the module.
func IsSensitiveLogKey(key string) bool {
	k := strings.ToLower(key)
	return sensitiveKeys[k] || identifierLogKeys[k]
}

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
