//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/processheartbeat"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestProcessHeartbeatUpsertAndStaleness exercises migration 0133 against real
// PostgreSQL: the per-process upsert, the forget window's effect on the fleet
// read, the clean-shutdown flag, and the staleness rule the admin status page
// applies on top. None of this can be proven with a fake — the upsert's
// ON CONFLICT semantics, now(), and the interval comparison ARE the behaviour.
func TestProcessHeartbeatUpsertAndStaleness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	host := "ph-" + uuid.NewString()[:8]
	apiID := processheartbeat.ProcessID(host, 1)
	workerID := processheartbeat.ProcessID(host, 2)
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM process_heartbeats WHERE hostname = $1`, host)
	}()

	started := time.Now().UTC().Add(-time.Hour)
	beat := func(id, role string, pollErr string) {
		t.Helper()
		if err := q.UpsertProcessHeartbeat(ctx, sqlcgen.UpsertProcessHeartbeatParams{
			ProcessID: id, Role: role, Hostname: host, Pid: 1,
			Version: "0.6.3", BuildCommit: "abc1234", StartedAt: started,
			LastSettingsPollSuccessAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			LastSettingsPollError:     pollErr,
		}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	beat(apiID, "api", "")
	beat(workerID, "worker", "")
	// A second tick must UPDATE, never insert: a fleet of two processes ticking
	// every ten seconds would otherwise become a table of thousands of rows.
	beat(apiID, "api", "")

	rows, err := q.ListProcessHeartbeats(ctx, interval(time.Hour))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	mine := onHost(rows, host)
	if len(mine) != 2 {
		t.Fatalf("rows = %d, want 2 (the upsert appended instead of updating)", len(mine))
	}
	for _, r := range mine {
		if r.StartedAt.UTC().Round(time.Second) != started.Round(time.Second) {
			t.Errorf("%s started_at = %v, want the process's own boot time %v", r.ProcessID, r.StartedAt, started)
		}
		if !r.LastSeenAt.After(started) {
			t.Errorf("%s last_seen_at = %v, want now()", r.ProcessID, r.LastSeenAt)
		}
	}

	// A cleanly stopped replica keeps its row and never degrades: that is what a
	// scale-down looks like.
	if err := q.MarkProcessStopped(ctx, workerID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	rows, _ = q.ListProcessHeartbeats(ctx, interval(time.Hour))
	fleet := judge(t, onHost(rows, host))
	if _, degraded := processheartbeat.Judge(fleet); degraded {
		t.Fatal("a cleanly stopped process degraded the fleet")
	}

	// Age the worker back to 'running' with a stale clock — the only report a
	// SIGKILLed process can produce.
	if _, err := st.Pool.Exec(ctx,
		`UPDATE process_heartbeats SET state='running', stopped_at=NULL, last_seen_at = now() - interval '47 seconds' WHERE process_id = $1`,
		workerID); err != nil {
		t.Fatalf("age: %v", err)
	}
	rows, _ = q.ListProcessHeartbeats(ctx, interval(time.Hour))
	fault, degraded := processheartbeat.Judge(judge(t, onHost(rows, host)))
	if !degraded {
		t.Fatal("a worker silent for 47s did not degrade the fleet")
	}
	if fault.Process.ProcessID != workerID || fault.Process.State != "stale" {
		t.Errorf("fault = %+v, want the stale worker", fault.Process)
	}

	// The forget window HIDES a row the fleet should no longer speak for, and
	// the sweep removes it — without this the table grows with the deployment's
	// history rather than its shape.
	if _, err := st.Pool.Exec(ctx,
		`UPDATE process_heartbeats SET last_seen_at = now() - interval '72 hours' WHERE process_id = $1`,
		workerID); err != nil {
		t.Fatalf("age out: %v", err)
	}
	rows, _ = q.ListProcessHeartbeats(ctx, interval(24*time.Hour))
	if len(onHost(rows, host)) != 1 {
		t.Fatalf("a replica gone for three days is still in the fleet answer")
	}
	// Counted straight off the table, NOT through ListProcessHeartbeats: that
	// read already hides the row, so it could never see the sweep remove it.
	rowCount := func() int {
		var n int
		if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM process_heartbeats WHERE hostname = $1`, host).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	before := rowCount()
	if _, err := q.ForgetStaleProcessHeartbeats(ctx, interval(24*time.Hour)); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if after := rowCount(); after >= before {
		t.Fatalf("the forget sweep removed nothing (%d -> %d)", before, after)
	}
}

// TestJobEventAndChildRunIdentityInheritance proves migration 0133's two
// BEFORE INSERT triggers against real PostgreSQL. Both exist because the rows
// they fill are written by OTHER triggers (0083/0094/0107/0120) with no Go
// statement anywhere near them, so nothing else can carry the identity in.
func TestJobEventAndChildRunIdentityInheritance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	src := "ih-" + uuid.NewString()
	var parentID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO job_runs (type, queue, source_id, state, request_id, correlation_id, trace_id, worker_id)
		 VALUES ('video_transcode', 'transcode_jobs', $1, 'running', 'req-1', 'corr-1', 'trace-1', 'box:7')
		 RETURNING id`, src).Scan(&parentID); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM job_runs WHERE source_id LIKE 'ih-%'`) }()

	// An event that carries NOTHING inherits everything from its run.
	var req, corr, trace, worker string
	if err := st.Pool.QueryRow(ctx,
		`WITH e AS (
		     INSERT INTO job_events (job_id, kind, state) VALUES ($1, 'dead_lettered', 'dead_lettered')
		     RETURNING request_id, correlation_id, trace_id, worker_id
		 ) SELECT * FROM e`, parentID).Scan(&req, &corr, &trace, &worker); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if req != "req-1" || corr != "corr-1" || trace != "trace-1" || worker != "box:7" {
		t.Fatalf("event inherited %q/%q/%q/%q, want the run's ids", req, corr, trace, worker)
	}

	// An event that carries its OWN id keeps it: fill only, never overwrite.
	if err := st.Pool.QueryRow(ctx,
		`WITH e AS (
		     INSERT INTO job_events (job_id, kind, state, correlation_id) VALUES ($1, 'started', 'running', 'own-corr')
		     RETURNING correlation_id
		 ) SELECT * FROM e`, parentID).Scan(&corr); err != nil {
		t.Fatalf("insert event with own id: %v", err)
	}
	if corr != "own-corr" {
		t.Fatalf("an event's own correlation id was overwritten with %q", corr)
	}

	// A CHILD run inherits from its parent. This is the row an operator actually
	// sees: 0083's roll-up hides a transcode_jobs run whenever children exist.
	if err := st.Pool.QueryRow(ctx,
		`WITH c AS (
		     INSERT INTO job_runs (type, queue, source_id, state, parent_job_id)
		     VALUES ('video_transcode_hls', 'transcode_steps', $1, 'running', $2)
		     RETURNING request_id, correlation_id, trace_id, worker_id
		 ) SELECT * FROM c`, "ih-child-"+src, parentID).Scan(&req, &corr, &trace, &worker); err != nil {
		t.Fatalf("insert child: %v", err)
	}
	if req != "req-1" || corr != "corr-1" || worker != "box:7" {
		t.Fatalf("child inherited %q/%q/%q, want the parent's ids", req, corr, worker)
	}

	// A run with no parent is left alone rather than given invented ids.
	if err := st.Pool.QueryRow(ctx,
		`WITH o AS (
		     INSERT INTO job_runs (type, queue, source_id, state)
		     VALUES ('video_import', 'import_jobs', $1, 'queued')
		     RETURNING request_id, correlation_id
		 ) SELECT * FROM o`, "ih-orphan-"+src).Scan(&req, &corr); err != nil {
		t.Fatalf("insert orphan: %v", err)
	}
	if req != "" || corr != "" {
		t.Fatalf("an unparented run was given ids %q/%q", req, corr)
	}
}

// onHost keeps only this test's rows: the lab database is shared with whatever
// else has run against it.
func onHost(rows []sqlcgen.ProcessHeartbeat, host string) []sqlcgen.ProcessHeartbeat {
	var out []sqlcgen.ProcessHeartbeat
	for _, r := range rows {
		if r.Hostname == host {
			out = append(out, r)
		}
	}
	return out
}

// judge converts raw rows into the judged shape, the way *Fleet.List does, so
// the test asserts the SAME staleness rule the admin page applies.
func judge(t *testing.T, rows []sqlcgen.ProcessHeartbeat) []processheartbeat.Process {
	t.Helper()
	now := time.Now()
	out := make([]processheartbeat.Process, 0, len(rows))
	for _, r := range rows {
		p := processheartbeat.Process{
			ProcessID: r.ProcessID, Role: r.Role, State: "running",
			LastSeenAt: r.LastSeenAt, LastSeenAgo: now.Sub(r.LastSeenAt).Round(time.Second),
			PollError: r.LastSettingsPollError, SettingsPoll: "ok",
		}
		switch {
		case r.State == "stopped":
			p.State = "stopped"
		case p.LastSeenAgo > 30*time.Second:
			p.State = "stale"
		}
		if r.LastSettingsPollError != "" {
			p.SettingsPoll = "failing"
		}
		out = append(out, p)
	}
	return out
}

func interval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: int64(d / time.Microsecond), Valid: true}
}
