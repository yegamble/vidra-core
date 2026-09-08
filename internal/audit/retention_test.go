package audit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// seed appends n rows all stamped at occurredAt, bypassing Record so the test
// controls the age directly (Record stamps now()).
func (f *fakeRepo) seed(n int, occurredAt time.Time) {
	for i := 0; i < n; i++ {
		f.rows = append(f.rows, sqlcgen.ListAuditLogRow{
			Action: "auth.login", Result: "success", OccurredAt: occurredAt,
		})
	}
}

// TestPruneDeletesOnlyExpiredRows is the whole policy: past the window goes,
// inside the window stays.
func TestPruneDeletesOnlyExpiredRows(t *testing.T) {
	now := time.Now()
	repo := &fakeRepo{}
	repo.seed(5, now.Add(-500*24*time.Hour)) // well past 400 days
	repo.seed(3, now.Add(-10*24*time.Hour))  // comfortably inside
	svc := NewService(repo)

	deleted, err := svc.Prune(context.Background(), now, DefaultRetention)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 5 {
		t.Errorf("deleted %d rows, want 5", deleted)
	}
	// 3 survivors + the one bookkeeping row the sweep wrote.
	if got := len(repo.rows); got != 4 {
		t.Errorf("%d rows remain, want 4 (3 inside the window + the prune's own row)", got)
	}
}

// TestPruneRecordsItsOwnRunWithACount: the run is auditable, and the count is
// in the STRUCTURED envelope — audit_log cannot carry prose, so a number in a
// reason string would be a number nothing can read back.
func TestPruneRecordsItsOwnRunWithACount(t *testing.T) {
	now := time.Now()
	repo := &fakeRepo{}
	repo.seed(7, now.Add(-500*24*time.Hour))
	svc := NewService(repo)

	if _, err := svc.Prune(context.Background(), now, DefaultRetention); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(repo.rows) != 1 {
		t.Fatalf("%d rows remain, want exactly the prune's own row", len(repo.rows))
	}
	row := repo.rows[0]
	if row.Action != observability.ActionAuditRetentionPrune {
		t.Errorf("action = %q, want %q", row.Action, observability.ActionAuditRetentionPrune)
	}
	if row.Result != observability.ResultSuccess {
		t.Errorf("result = %q, want success", row.Result)
	}
	if row.ActorKind != "system" {
		t.Errorf("actor_kind = %q, want system — nobody asked for this sweep", row.ActorKind)
	}
	var md map[string]string
	if err := json.Unmarshal(row.Metadata, &md); err != nil {
		t.Fatalf("metadata is not a JSON object: %v (%s)", err, row.Metadata)
	}
	if md["count"] != "7" {
		t.Errorf("metadata.count = %q, want \"7\"", md["count"])
	}
	if row.Reason != "" {
		t.Errorf("reason = %q; the count belongs in the envelope, not in prose", row.Reason)
	}
}

// TestPruneWritesNoRowWhenNothingExpired: a daily tick over a fresh trail must
// leave no trace, or the trail's own bookkeeping becomes the bulk of the trail
// on a quiet instance — rows which would then need pruning themselves.
func TestPruneWritesNoRowWhenNothingExpired(t *testing.T) {
	now := time.Now()
	repo := &fakeRepo{}
	repo.seed(3, now.Add(-10*24*time.Hour))
	svc := NewService(repo)

	deleted, err := svc.Prune(context.Background(), now, DefaultRetention)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted %d, want 0", deleted)
	}
	if len(repo.rows) != 3 {
		t.Errorf("%d rows, want the 3 seeded and no bookkeeping row", len(repo.rows))
	}
}

// TestPruneZeroRetentionKeepsForever is the house 0-means-unlimited convention.
// It must do NO work at all, not "delete everything older than now".
func TestPruneZeroRetentionKeepsForever(t *testing.T) {
	now := time.Now()
	for _, retention := range []time.Duration{0, -time.Hour} {
		repo := &fakeRepo{}
		repo.seed(4, now.Add(-5000*24*time.Hour))
		svc := NewService(repo)

		deleted, err := svc.Prune(context.Background(), now, retention)
		if err != nil {
			t.Fatalf("Prune(%v): %v", retention, err)
		}
		if deleted != 0 || len(repo.rows) != 4 {
			t.Errorf("retention %v: deleted %d and left %d rows; want 0 deleted, 4 left", retention, deleted, len(repo.rows))
		}
		if repo.pruneCalls != 0 {
			t.Errorf("retention %v: hit the database %d times; keep-forever must do no work", retention, repo.pruneCalls)
		}
	}
}

// TestPruneBatchesRatherThanOneStatement: a table nobody has ever swept can hold
// far more than one batch, and the sweep must not become one unbounded DELETE
// against the table every security-sensitive write appends to.
func TestPruneBatchesRatherThanOneStatement(t *testing.T) {
	now := time.Now()
	repo := &fakeRepo{}
	rows := int(pruneBatchSize)*2 + 1
	repo.seed(rows, now.Add(-500*24*time.Hour))
	svc := NewService(repo)

	deleted, err := svc.Prune(context.Background(), now, DefaultRetention)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != int64(rows) {
		t.Errorf("deleted %d, want %d", deleted, rows)
	}
	if repo.pruneCalls != 3 {
		t.Errorf("%d batches, want 3 (two full + one short that ends the loop)", repo.pruneCalls)
	}
	if repo.maxBatch > pruneBatchSize {
		t.Errorf("a batch asked for %d rows, above the %d cap", repo.maxBatch, pruneBatchSize)
	}
}

// TestPruneWithoutRepositoryIsAnError: a service wired without storage must say
// so rather than answer "0 rows pruned", which reads as a healthy sweep.
func TestPruneWithoutRepositoryIsAnError(t *testing.T) {
	svc := NewService(nil)
	if _, err := svc.Prune(context.Background(), time.Now(), DefaultRetention); err == nil {
		t.Fatal("Prune with no repository returned nil error")
	}
}
