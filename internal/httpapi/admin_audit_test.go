package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// httpAuditFakeRepo is an in-memory audit.Repository for handler tests.
type httpAuditFakeRepo struct{ rows []sqlcgen.ListAuditLogRow }

func (f *httpAuditFakeRepo) InsertAuditLog(_ context.Context, a sqlcgen.InsertAuditLogParams) error {
	f.rows = append(f.rows, sqlcgen.ListAuditLogRow{
		ID: a.ID, SchemaVersion: a.SchemaVersion, Domain: a.Domain,
		Action: a.Action, Result: a.Result, ActorID: a.ActorID,
		ActorKind: a.ActorKind, ActorRole: a.ActorRole,
		Reason: a.Reason, RequestID: a.RequestID, CorrelationID: a.CorrelationID,
		TraceID: a.TraceID, PipelineRunID: a.PipelineRunID, JobID: a.JobID,
		ResourceType: a.ResourceType, ResourceID: a.ResourceID,
		Metadata: a.Metadata, Changes: a.Changes, OccurredAt: time.Now(),
	})
	return nil
}

func (f *httpAuditFakeRepo) ListAuditLog(_ context.Context, a sqlcgen.ListAuditLogParams) ([]sqlcgen.ListAuditLogRow, error) {
	var out []sqlcgen.ListAuditLogRow
	for i := len(f.rows) - 1; i >= 0; i-- {
		r := f.rows[i]
		if a.Action != nil && r.Action != *a.Action {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func TestAuditLogPersistsAndLists(t *testing.T) {
	authRepo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(authRepo, issuer, 720*time.Hour)
	srv := New(testConfig(), nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithAuditLog(audit.NewService(&httpAuditFakeRepo{})),
	)

	// The first account is THE admin via the owner-claim flow (0104), which
	// audits an auth.owner_claim success; the second account registers normally
	// and audits an auth.register success.
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	registerAndToken(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)
	// A login audits an auth.login success (used for the action filter below).
	postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)

	rec := getWithAuth(srv, "/api/v1/admin/audit-log", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log list = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body auditLogListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	var reg *auditLogEntryView
	for i := range body.Entries {
		if body.Entries[i].Action == observability.ActionRegister && body.Entries[i].Result == observability.ResultSuccess {
			reg = &body.Entries[i]
		}
	}
	if reg == nil {
		t.Fatalf("no auth.register success in the audit log; body=%s", rec.Body.String())
	}
	if reg.ActorID == "" {
		t.Error("register audit entry should carry the created account's actor_id")
	}
	if reg.SchemaVersion != audit.CurrentSchemaVersion || reg.Domain != "auth" {
		t.Errorf("register envelope = version %d domain %q, want %d/auth", reg.SchemaVersion, reg.Domain, audit.CurrentSchemaVersion)
	}
	if reg.ActorKind != "user" {
		t.Errorf("register actor kind = %s, want user", reg.ActorKind)
	}
	if reg.RequestID == "" || reg.CorrelationID == "" {
		t.Errorf("register correlation = request %q correlation %q, want both", reg.RequestID, reg.CorrelationID)
	}

	// Action filter returns only matching entries.
	var logins auditLogListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/admin/audit-log?action=auth.login", admin).Body.Bytes(), &logins)
	if len(logins.Entries) == 0 {
		t.Error("expected at least one auth.login entry")
	}
	for _, e := range logins.Entries {
		if e.Action != "auth.login" {
			t.Errorf("filtered entry action = %q, want auth.login", e.Action)
		}
	}

	// A regular user cannot read the audit log; anon is unauthorized.
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := getWithAuth(srv, "/api/v1/admin/audit-log", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin audit-log = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/audit-log", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon audit-log = %d, want 401", rec.Code)
	}
}

func TestAuditAutomaticallyAttachesRequestCorrelationAndTrace(t *testing.T) {
	authRepo := newAuthFakeRepo()
	auditRepo := &httpAuditFakeRepo{}
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(authRepo, issuer, 720*time.Hour)
	srv := New(testConfig(), nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithAuditLog(audit.NewService(auditRepo)),
	)
	registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
		`{"email":"ada@example.test","password":"supersecret"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(correlationHeader, "audit-correlation-42")
	req = req.WithContext(trace.ContextWithSpanContext(req.Context(), sc))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d; body=%s", rec.Code, rec.Body.String())
	}

	var login *sqlcgen.ListAuditLogRow
	for i := range auditRepo.rows {
		if auditRepo.rows[i].Action == observability.ActionLogin && auditRepo.rows[i].Result == observability.ResultSuccess {
			login = &auditRepo.rows[i]
		}
	}
	if login == nil {
		t.Fatal("no durable auth.login success event")
	}
	if login.RequestID == "" || login.CorrelationID != "audit-correlation-42" || login.TraceID != tid.String() {
		t.Errorf("audit correlation = request %q correlation %q trace %q", login.RequestID, login.CorrelationID, login.TraceID)
	}
	if login.ActorKind != "user" || !login.ActorID.Valid {
		t.Errorf("audit actor snapshot = kind %q id %+v, want a user UUID", login.ActorKind, login.ActorID)
	}
}
