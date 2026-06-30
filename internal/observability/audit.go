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
	ActionPasswordResetRequest  = "auth.password_reset.request"
	ActionPasswordResetComplete = "auth.password_reset.complete"
	ActionEmailVerifyRequest    = "auth.email_verify.request"
	ActionEmailVerifyConfirm    = "auth.email_verify.confirm"
	ActionAccountDeactivate     = "auth.account.deactivate"
	ActionRateLimited           = "auth.rate_limited"
	ActionReportResolve         = "moderation.report.resolve"
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
	"private_key":        true,
	"stream_key":         true,
	"jwt":                true,
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
