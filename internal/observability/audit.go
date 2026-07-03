// Package observability holds cross-cutting logging/audit helpers shared by the
// HTTP and service layers. See .ralph/specs/observability.md.
package observability

import (
	"context"
	"log/slog"
	"strings"
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
	ActionVideoDelete         = "content.video.delete"
	ActionChannelDelete       = "content.channel.delete"
	ActionMediaGC             = "admin.media.gc"
	// ActionDonationVerify records a creator proving control of a donation
	// address by signing the challenge (P14). Reason carries the safe address
	// id + network only — never the signature, nonce, or any key material.
	ActionDonationVerify = "content.donation.verify"
	// ActionLiveReplay records the best-effort republish of a recorded live
	// session as a VOD on ingest-stop (P12). Reason carries safe ids/outcome
	// only — never the stream key.
	ActionLiveReplay = "content.live.replay"
	// E2EE one-time-key claims are audited with COUNTS ONLY (never key
	// material): key exhaustion/abuse is a security-relevant signal.
	ActionE2EEClaim = "e2ee.otk.claim"
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
	Action    string // one of the Action* constants
	Result    string // ResultSuccess or ResultFailure
	ActorID   string // user id; empty when unauthenticated/unknown
	RequestID string // correlates with request logs
	Reason    string // safe, non-sensitive detail; omitted when empty
}

// Audit emits ev on logger at info level. The slog record's timestamp is the
// event's occurred_at. A nil logger falls back to the default.
func Audit(ctx context.Context, logger *slog.Logger, ev AuditEvent) {
	if logger == nil {
		logger = slog.Default()
	}
	args := []any{
		"audit", true,
		"action", ev.Action,
		"result", ev.Result,
	}
	if ev.ActorID != "" {
		args = append(args, "actor_id", ev.ActorID)
	}
	if ev.RequestID != "" {
		args = append(args, "request_id", ev.RequestID)
	}
	if ev.Reason != "" {
		args = append(args, "reason", ev.Reason)
	}
	logger.InfoContext(ctx, "audit", args...)
}
