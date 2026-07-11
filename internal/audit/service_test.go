package audit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory audit.Repository.
type fakeRepo struct{ rows []sqlcgen.ListAuditLogRow }

func (f *fakeRepo) InsertAuditLog(_ context.Context, a sqlcgen.InsertAuditLogParams) error {
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

func (f *fakeRepo) ListAuditLog(_ context.Context, a sqlcgen.ListAuditLogParams) ([]sqlcgen.ListAuditLogRow, error) {
	var out []sqlcgen.ListAuditLogRow
	for i := len(f.rows) - 1; i >= 0; i-- { // newest first
		r := f.rows[i]
		if a.Action != nil && r.Action != *a.Action {
			continue
		}
		out = append(out, r)
	}
	off := min(int(a.ResultOffset), len(out))
	out = out[off:]
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(out) {
		out = out[:a.ResultLimit]
	}
	return out, nil
}

func TestRecordAndList(t *testing.T) {
	ctx := context.Background()
	svc := NewService(&fakeRepo{})
	actor := uuid.New().String()
	pipelineID := uuid.New()
	jobID := uuid.New()

	if err := svc.Record(ctx, Event{
		Action: "auth.login", Result: "success",
		Actor:     ActorSnapshot{ID: actor, Kind: "user", Role: "admin"},
		RequestID: "r1", CorrelationID: "c1",
		TraceID:       "0102030405060708090a0b0c0d0e0f10",
		PipelineRunID: pipelineID, JobID: jobID,
		ResourceType: "user", ResourceID: actor,
		Metadata: []MetadataField{{Key: "auth_method", Value: "password"}},
		Changes:  []Change{{Field: "account_enabled", Before: "false", After: "true"}},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := svc.Record(ctx, Event{Action: "auth.register", Result: "success", ActorID: actor}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	all, err := svc.List(ctx, "", 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("entries = %d, want 2", len(all))
	}
	// Newest first: the register came last.
	if all[0].Action != "auth.register" || all[1].Action != "auth.login" {
		t.Errorf("order = [%s, %s], want [auth.register, auth.login]", all[0].Action, all[1].Action)
	}
	if all[1].ActorID != actor {
		t.Errorf("actor_id = %q, want %q", all[1].ActorID, actor)
	}
	if all[1].SchemaVersion != CurrentSchemaVersion || all[1].Domain != "auth" {
		t.Errorf("envelope = version %d domain %q, want %d/auth", all[1].SchemaVersion, all[1].Domain, CurrentSchemaVersion)
	}
	if all[1].ActorKind != "user" || all[1].ActorRole != "admin" {
		t.Errorf("actor snapshot = %s/%s, want user/admin", all[1].ActorKind, all[1].ActorRole)
	}
	if all[1].CorrelationID != "c1" || all[1].TraceID == "" || all[1].PipelineRunID != pipelineID.String() || all[1].JobID != jobID.String() {
		t.Errorf("correlation fields = %+v, want request/trace/pipeline/job ids", all[1])
	}
	if all[1].ResourceType != "user" || all[1].ResourceID != actor || all[1].Metadata["auth_method"] != "password" {
		t.Errorf("resource/metadata = %+v / %+v", all[1], all[1].Metadata)
	}
	if len(all[1].Changes) != 1 || all[1].Changes[0].Field != "account_enabled" {
		t.Errorf("changes = %+v, want one safe account_enabled diff", all[1].Changes)
	}

	// Action filter.
	logins, err := svc.List(ctx, "auth.login", 20, 0)
	if err != nil {
		t.Fatalf("List(filter): %v", err)
	}
	if len(logins) != 1 || logins[0].Action != "auth.login" {
		t.Errorf("filtered = %+v, want one auth.login", logins)
	}
}

func TestRecordRejectsUnsafeOrUnboundedEnvelopeValues(t *testing.T) {
	svc := NewService(&fakeRepo{})
	tests := []struct {
		name string
		ev   Event
	}{
		{"unknown metadata key", Event{Action: "auth.login", Result: "success", Metadata: []MetadataField{{Key: "email", Value: "a.example"}}}},
		{"URL metadata value", Event{Action: "auth.login", Result: "success", Metadata: []MetadataField{{Key: "provider", Value: "https://identity.example/?token=x"}}}},
		{"content diff", Event{Action: "content.video.update", Result: "success", Changes: []Change{{Field: "description", Before: "old", After: "new"}}}},
		{"bad trace id", Event{Action: "auth.login", Result: "success", TraceID: "not-a-trace"}},
		{"URL resource id", Event{Action: "auth.login", Result: "success", ResourceType: "user", ResourceID: "https://example.test/user"}},
		{"conflicting actor ids", Event{Action: "auth.login", Result: "success", ActorID: uuid.NewString(), Actor: ActorSnapshot{ID: uuid.NewString(), Kind: "user"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.Record(context.Background(), tc.ev); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("Record error = %v, want ErrInvalidEvent", err)
			}
		})
	}
}

func TestRecordBoundsReasonWithoutSplittingUTF8(t *testing.T) {
	svc := NewService(&fakeRepo{})
	if err := svc.Record(context.Background(), Event{
		Action: "auth.login", Result: "failure", Reason: strings.Repeat("é", maxReasonBytes),
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	rows, err := svc.List(context.Background(), "", 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := len(rows[0].Reason); got > maxReasonBytes {
		t.Fatalf("reason bytes = %d, want <= %d", got, maxReasonBytes)
	}
}

func TestRecordAllowsReasonProvidedClassification(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	if err := svc.Record(context.Background(), Event{
		Action: "moderation.video.quarantine_reject", Result: "success",
		ResourceType: "video", ResourceID: uuid.NewString(),
		Reason:   "moderator_rejected",
		Metadata: []MetadataField{{Key: "reason_provided", Value: "true"}},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	rows, err := svc.List(context.Background(), "", 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].Metadata["reason_provided"] != "true" {
		t.Fatalf("rows = %+v, want durable reason_provided=true metadata", rows)
	}
	if got := string(repo.rows[0].Changes); got != "[]" {
		t.Fatalf("empty changes JSON = %q, want [] for the DB shape constraint", got)
	}
}

func TestRecordInheritsCorrelationAndTraceFromContext(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled})
	ctx := observability.ContextWithCorrelation(context.Background(), observability.Correlation{
		RequestID: "request-7", CorrelationID: "correlation-7",
	})
	ctx = trace.ContextWithSpanContext(ctx, sc)

	if err := svc.Record(ctx, Event{Action: "content.live.replay", Result: "success"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	row := repo.rows[0]
	if row.RequestID != "request-7" || row.CorrelationID != "correlation-7" || row.TraceID != tid.String() {
		t.Errorf("inherited ids = request %q correlation %q trace %q", row.RequestID, row.CorrelationID, row.TraceID)
	}
}

func TestRecordActorParsing(t *testing.T) {
	ctx := context.Background()
	svc := NewService(&fakeRepo{})

	// Unauthenticated (empty) actors are stored as NULL → "". A non-UUID user
	// actor is rejected instead of being silently relabelled anonymous.
	_ = svc.Record(ctx, Event{Action: "auth.login", Result: "failure", ActorID: ""})
	if err := svc.Record(ctx, Event{Action: "auth.login", Result: "failure", ActorID: "not-a-uuid"}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("invalid actor Record error = %v, want ErrInvalidEvent", err)
	}
	if err := svc.Record(ctx, Event{
		Action: "admin.media.gc", Result: "success",
		Actor: ActorSnapshot{ID: uuid.NewString(), Kind: "system"},
	}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("system actor carrying user id error = %v, want ErrInvalidEvent", err)
	}

	entries, err := svc.List(ctx, "", 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].ActorID != "" || entries[0].ActorKind != "anonymous" {
		t.Errorf("entries = %+v, want one anonymous actor", entries)
	}
}
