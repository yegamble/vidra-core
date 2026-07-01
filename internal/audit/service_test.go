package audit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory audit.Repository.
type fakeRepo struct{ rows []sqlcgen.ListAuditLogRow }

func (f *fakeRepo) InsertAuditLog(_ context.Context, a sqlcgen.InsertAuditLogParams) error {
	f.rows = append(f.rows, sqlcgen.ListAuditLogRow{
		ID: uuid.New(), Action: a.Action, Result: a.Result, ActorID: a.ActorID,
		Reason: a.Reason, RequestID: a.RequestID, OccurredAt: time.Now(),
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

	if err := svc.Record(ctx, Event{Action: "auth.login", Result: "success", ActorID: actor, RequestID: "r1"}); err != nil {
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

	// Action filter.
	logins, err := svc.List(ctx, "auth.login", 20, 0)
	if err != nil {
		t.Fatalf("List(filter): %v", err)
	}
	if len(logins) != 1 || logins[0].Action != "auth.login" {
		t.Errorf("filtered = %+v, want one auth.login", logins)
	}
}

func TestRecordActorParsing(t *testing.T) {
	ctx := context.Background()
	svc := NewService(&fakeRepo{})

	// Unauthenticated (empty) and non-UUID actor ids are stored as NULL → "".
	_ = svc.Record(ctx, Event{Action: "auth.login", Result: "failure", ActorID: ""})
	_ = svc.Record(ctx, Event{Action: "auth.login", Result: "failure", ActorID: "not-a-uuid"})

	entries, err := svc.List(ctx, "", 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range entries {
		if e.ActorID != "" {
			t.Errorf("actor_id = %q, want empty for unauthenticated/invalid", e.ActorID)
		}
	}
}
