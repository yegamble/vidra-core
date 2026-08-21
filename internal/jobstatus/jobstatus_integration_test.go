//go:build integration

// Integration smoke for the job-status aggregation: proves every per-queue
// stat/failure query is valid against the LIVE schema (column names, states,
// casts) and that Overview/Depths assemble without error. Run with:
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration ./internal/jobstatus/
package jobstatus

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store"
)

func TestJobStatusOverviewIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	svc := NewService(st.Queries())
	ov, err := svc.Overview(ctx)
	if err != nil {
		t.Fatalf("Overview against live schema: %v", err)
	}
	if len(ov.Queues) != 7 {
		t.Fatalf("want 7 queues, got %d", len(ov.Queues))
	}
	// Every queue reports non-negative counts; on an empty DB oldest age is 0.
	for _, q := range ov.Queues {
		if q.Pending < 0 || q.Running < 0 || q.Done < 0 || q.Failed < 0 || q.OldestPendingAgeSeconds < 0 {
			t.Errorf("negative count in %+v", q)
		}
	}
	depths, err := svc.Depths(ctx)
	if err != nil {
		t.Fatalf("Depths: %v", err)
	}
	if len(depths) != 28 {
		t.Errorf("want 28 depth samples, got %d", len(depths))
	}
}

// TestOperationalJobProjectionTrigger proves the migration's transactional
// projection at the real PostgreSQL boundary. The source queue remains
// authoritative, while secret-bearing source fields are omitted and state
// transitions append resumable event records.
func TestOperationalJobProjectionTrigger(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	suffix := uuid.NewString()[:8]
	var userID uuid.UUID
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"ops-"+suffix, "ops-"+suffix+"@example.test",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	const sourceSecret = "exports/private-user-archive.json"
	var sourceID uuid.UUID
	if err := tx.QueryRow(ctx,
		`INSERT INTO account_exports (user_id, storage_key) VALUES ($1, $2) RETURNING id`,
		userID, sourceSecret,
	).Scan(&sourceID); err != nil {
		t.Fatalf("enqueue export: %v", err)
	}
	for _, update := range []string{
		`UPDATE account_exports SET state = 'running', updated_at = clock_timestamp() WHERE id = $1`,
		`UPDATE account_exports SET state = 'pending', attempts = 1, last_error = 'Bearer secret-retry', updated_at = clock_timestamp() WHERE id = $1`,
		`UPDATE account_exports SET state = 'failed', attempts = 5, last_error = 'https://signed.test/a?sig=secret', updated_at = clock_timestamp() WHERE id = $1`,
	} {
		if _, err := tx.Exec(ctx, update, sourceID); err != nil {
			t.Fatalf("transition export: %v", err)
		}
	}

	var state, queue, resourceType, resourceID, projectedJSON string
	var actorID uuid.UUID
	var attempt int32
	var finished bool
	if err := tx.QueryRow(ctx, `
		SELECT state, queue, resource_type, resource_id, actor_id, attempt,
		       finished_at IS NOT NULL, to_jsonb(job_runs)::text
		FROM job_runs WHERE queue = 'account_exports' AND source_id = $1`, sourceID.String(),
	).Scan(&state, &queue, &resourceType, &resourceID, &actorID, &attempt, &finished, &projectedJSON); err != nil {
		t.Fatalf("read projected run: %v", err)
	}
	if state != StateDeadLettered || queue != "account_exports" || resourceType != "user" ||
		resourceID != userID.String() || actorID != userID || attempt != 5 || !finished {
		t.Fatalf("projected run = state:%s queue:%s resource:%s/%s actor:%s attempt:%d finished:%v",
			state, queue, resourceType, resourceID, actorID, attempt, finished)
	}
	for _, secret := range []string{sourceSecret, "Bearer secret-retry", "https://signed.test", "sig=secret"} {
		if strings.Contains(projectedJSON, secret) {
			t.Errorf("projection leaked source secret %q: %s", secret, projectedJSON)
		}
	}
	if !strings.Contains(projectedJSON, "inspect correlated system logs") {
		t.Errorf("projection missing bounded diagnostic guidance: %s", projectedJSON)
	}

	rows, err := tx.Query(ctx, `
		SELECT e.kind, e.state
		FROM job_events e
		JOIN job_runs j ON j.id = e.job_id
		WHERE j.queue = 'account_exports' AND j.source_id = $1
		ORDER BY e.cursor`, sourceID.String())
	if err != nil {
		t.Fatalf("read projected events: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var kind, eventState string
		if err := rows.Scan(&kind, &eventState); err != nil {
			t.Fatalf("scan projected event: %v", err)
		}
		got = append(got, kind+":"+eventState)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate projected events: %v", err)
	}
	want := []string{
		"enqueued:queued", "started:running", "retry_scheduled:retry_scheduled", "dead_lettered:dead_lettered",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("projected events = %v, want %v", got, want)
	}
}
