package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"
)

func decodeRecord(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatalf("unmarshal record %q: %v", b, err)
	}
	return rec
}

func TestAuditEmitsRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	Audit(context.Background(), logger, AuditEvent{
		Action: ActionLogin, Result: ResultSuccess, ActorID: "u1", RequestID: "r1",
	})

	rec := decodeRecord(t, buf.Bytes())
	if rec["audit"] != true {
		t.Error("audit flag should be true")
	}
	if rec["msg"] != "audit" {
		t.Errorf("msg = %v, want audit", rec["msg"])
	}
	if rec["schema_version"] != float64(2) || rec["domain"] != "auth" || rec["actor_kind"] != "user" {
		t.Errorf("typed envelope fields = version %v domain %v actor_kind %v", rec["schema_version"], rec["domain"], rec["actor_kind"])
	}
	if rec["action"] != "auth.login" {
		t.Errorf("action = %v, want auth.login", rec["action"])
	}
	if rec["result"] != "success" {
		t.Errorf("result = %v, want success", rec["result"])
	}
	if rec["actor_id"] != "u1" {
		t.Errorf("actor_id = %v, want u1", rec["actor_id"])
	}
	if rec["request_id"] != "r1" {
		t.Errorf("request_id = %v, want r1", rec["request_id"])
	}
	if _, ok := rec["time"]; !ok {
		t.Error("record should carry a timestamp (occurred_at)")
	}
}

func TestAuditEmitsCorrelationAndBoundsReason(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	Audit(context.Background(), logger, AuditEvent{
		Action: ActionVideoReject, Result: ResultFailure,
		ActorID: "u1", ActorRole: "moderator",
		RequestID: "r1", CorrelationID: "c1",
		TraceID:       "0102030405060708090a0b0c0d0e0f10",
		PipelineRunID: "pipeline", JobID: "job",
		ResourceType: "video", ResourceID: "v1",
		Reason: strings.Repeat("é", 512),
	})
	rec := decodeRecord(t, buf.Bytes())
	for key, want := range map[string]any{
		"correlation_id": "c1", "trace_id": "0102030405060708090a0b0c0d0e0f10",
		"pipeline_run_id": "pipeline", "job_id": "job",
		"resource_type": "video", "resource_id": "v1", "actor_role": "moderator",
	} {
		if rec[key] != want {
			t.Errorf("%s = %v, want %v", key, rec[key], want)
		}
	}
	if reason, _ := rec["reason"].(string); len(reason) > 512 || !utf8.ValidString(reason) {
		t.Errorf("bounded reason bytes/utf8 = %d/%v, want <=512/true", len(reason), utf8.ValidString(reason))
	}
}

func TestAuditOmitsEmptyOptionalFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	Audit(context.Background(), logger, AuditEvent{Action: ActionLogout, Result: ResultSuccess})

	rec := decodeRecord(t, buf.Bytes())
	if _, ok := rec["actor_id"]; ok {
		t.Error("actor_id should be omitted when empty")
	}
	if _, ok := rec["reason"]; ok {
		t.Error("reason should be omitted when empty")
	}
}

func TestAuditContainsNoSensitiveKeys(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	Audit(context.Background(), logger, AuditEvent{
		Action: ActionPasswordResetComplete, Result: ResultFailure,
		ActorID: "u1", RequestID: "r1", Reason: "invalid_token",
	})

	for k := range decodeRecord(t, buf.Bytes()) {
		if IsSensitiveKey(k) {
			t.Errorf("audit event must not contain denylisted key %q", k)
		}
	}
}

func TestIsSensitiveKey(t *testing.T) {
	for _, k := range []string{"password", "Token", "REFRESH_TOKEN", "authorization", "secret", "private_key", "smtp_password", "ipfs_cluster_token", "ipfs_private_cluster_token"} {
		if !IsSensitiveKey(k) {
			t.Errorf("%q should be flagged sensitive", k)
		}
	}
	for _, k := range []string{"action", "result", "actor_id", "request_id", "reason"} {
		if IsSensitiveKey(k) {
			t.Errorf("%q should NOT be flagged sensitive", k)
		}
	}
}
