//go:build integration

package store

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func legacyCatalogFixture(t *testing.T) (context.Context, *Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	base := dsn(t)
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := "catalog_legacy_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if _, err := admin.Exec(cleanup, `DROP DATABASE `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`); err != nil {
			t.Error(err)
		}
		_ = admin.Close(cleanup)
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range loadUpMigrations(t) {
		applyMigration(ctx, t, conn, migration)
	}
	_ = conn.Close(ctx)
	st, err := New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return ctx, st
}

func TestLegacyCatalogConversionPreservesObligationsAndOtherEvents(t *testing.T) {
	ctx, st := legacyCatalogFixture(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	count := func(table string) int64 {
		t.Helper()
		var n int64
		if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	convert := func(batch int32, want int64) {
		t.Helper()
		n, err := st.ConvertLegacyCatalog(ctx, batch)
		if err != nil || n != want {
			t.Fatalf("converted %d, %v; want %d", n, err, want)
		}
	}
	for _, batch := range []int32{-1, 0, 1001} {
		if _, err := st.ConvertLegacyCatalog(ctx, batch); err == nil {
			t.Fatalf("invalid batch %d accepted", batch)
		}
	}
	kinds := []string{"video.upsert", "video.suppress", "channel.upsert", "channel.delete", "user.suppress", "reconcile.begin", "reconcile.page", "reconcile.end"}
	for i, kind := range kinds {
		state := "pending"
		if i%2 == 1 {
			state = "dead"
		}
		exec(`INSERT INTO search_outbox(event_type,payload,state,next_attempt_at) VALUES ($1,'{"legacy":true}',$2,now()+interval '1 day')`, kind, state)
	}
	for _, kind := range append(append([]string{}, kinds...), "video.stats", "search.config_updated", "user.history_deleted", "video.watch_progress", "future.unknown") {
		exec(`INSERT INTO search_outbox(event_type,payload,state,attempts,last_error,next_attempt_at) VALUES ($1,'{"preserve":"all fields"}','delivered',7,'synthetic',now()+interval '2 days')`, kind)
		if kind == "video.stats" || kind == "search.config_updated" || kind == "user.history_deleted" || kind == "video.watch_progress" || kind == "future.unknown" {
			exec(`INSERT INTO search_outbox(event_type,payload,state,attempts,last_error,next_attempt_at) VALUES ($1,'{"preserve":42}','dead',9,'synthetic',now()+interval '3 days')`, kind)
		}
	}
	preserved := func() string {
		t.Helper()
		var value string
		err := st.Pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(o) ORDER BY id)::text FROM search_outbox o WHERE state='delivered' OR payload ? 'preserve'`).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := preserved()
	convert(1000, 0)
	if count("search_outbox") != 26 || count("search_catalog_dirty") != 0 {
		t.Fatal("uninitialized conversion retired work")
	}
	exec(`WITH e AS (INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1) RETURNING epoch) INSERT INTO search_catalog_state(scope,epoch) SELECT 'local',epoch FROM e`, uuid.New())
	convert(3, 3)
	if count("search_catalog_dirty") != 1 {
		t.Fatal("nonempty batch must insert one hint")
	}
	var first uuid.UUID
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM search_catalog_dirty`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	convert(1000, 5)
	convert(1000, 0)
	if count("search_catalog_dirty") != 2 || before != preserved() {
		t.Fatal("hints duplicated or preserved fields changed")
	}
	var distinct int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM search_catalog_dirty WHERE id<>$1`, first).Scan(&distinct); err != nil || distinct != 1 {
		t.Fatal("hint identity was reused", err)
	}

	// The older sequence reservation commits after a higher-ID batch. No high-water may skip it.
	lowTx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lowTx.Rollback(context.Background()) }()
	var low, high int64
	if err := lowTx.QueryRow(ctx, `INSERT INTO search_outbox(event_type,payload) VALUES ('video.upsert','{}') RETURNING id`).Scan(&low); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `INSERT INTO search_outbox(event_type,payload,next_attempt_at) VALUES ('channel.delete','{}',now()+interval '1 day') RETURNING id`).Scan(&high); err != nil || low >= high {
		t.Fatal("sequence fixture", err)
	}
	convert(1000, 1)
	if err := lowTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	convert(1000, 1)
	// An old in-flight sender's UPDATE cannot recreate retired work or clear its hint.
	exec(`UPDATE search_outbox SET state='pending',next_attempt_at=now()+interval '1 day',last_error='old retry' WHERE id=$1`, high)
	if count("search_catalog_dirty") != 4 || before != preserved() {
		t.Fatal("late writer/reschedule lost durable intent")
	}

	// Refusing the hint must roll back DELETE too, and the method must return no acknowledged count.
	exec(`INSERT INTO search_outbox(event_type,payload) VALUES ('video.suppress','{}')`)
	exec(`CREATE FUNCTION legacy_hint_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic hint refusal'; END;$$; CREATE TRIGGER legacy_hint_failure BEFORE INSERT ON search_catalog_dirty FOR EACH STATEMENT EXECUTE FUNCTION legacy_hint_failure()`)
	if n, err := st.ConvertLegacyCatalog(ctx, 1000); err == nil || n != 0 {
		t.Fatalf("failed hint acknowledged %d: %v", n, err)
	}
	if count("search_outbox") != 19 || count("search_catalog_dirty") != 4 {
		t.Fatal("failed hint retired the event")
	}
	exec(`DROP TRIGGER legacy_hint_failure ON search_catalog_dirty; DROP FUNCTION legacy_hint_failure()`)
	convert(1000, 1)
	convert(1000, 0)
	if count("search_catalog_dirty") != 5 || count("search_outbox") != 18 || before != preserved() {
		t.Fatal("retry lost hint or noncatalog work")
	}

	// A sender holding a row lock must neither block conversion nor lose work.
	exec(`INSERT INTO search_outbox(event_type,payload) VALUES ('reconcile.end','{}')`)
	locked, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locked.Rollback(context.Background()) }()
	if _, err := locked.Exec(ctx, `SELECT id FROM search_outbox WHERE event_type='reconcile.end' AND state='pending' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	convert(1000, 0)
	if count("search_catalog_dirty") != 5 || count("search_outbox") != 19 {
		t.Fatal("locked obligation changed")
	}
	if err := locked.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	convert(1000, 1)
	if count("search_catalog_dirty") != 6 || count("search_outbox") != 18 || before != preserved() {
		t.Fatal("unlocked obligation was lost")
	}
}
