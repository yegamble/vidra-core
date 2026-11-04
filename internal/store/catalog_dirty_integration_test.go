//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestCatalogDirtyHintsAreTransactionalStatementChangesBeforeBootstrap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tx, err := st.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	count := func() int64 {
		t.Helper()
		var n int64
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM search_catalog_dirty`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	change := func(want int64, sql string, args ...any) {
		t.Helper()
		before := count()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
		if got := count() - before; got != want {
			t.Fatalf("dirty delta=%d, want %d: %s", got, want, sql)
		}
	}
	owner, channel, video := uuid.New(), uuid.New(), uuid.New()
	change(1, `INSERT INTO users(id,username,email,password_hash) VALUES ($1,$2,$3,'x')`, owner, owner.String(), owner.String()+"@example.test")
	change(1, `INSERT INTO channels(id,owner_id,handle,display_name) VALUES ($1,$2,$3,'channel')`, channel, owner, channel.String())
	// One bulk statement creates one hint, including private drafts. Filtering
	// by present eligibility could miss a concurrent publication transaction.
	started := time.Now()
	change(1, `INSERT INTO videos(id,channel_id,title) SELECT CASE WHEN n=1 THEN $1::uuid ELSE uuid_generate_v4() END,$2,'draft' FROM generate_series(1,15492) n`, video, channel)
	t.Logf("15,492 synthetic video INSERTs coalesced to one hint in %s", time.Since(started))
	change(1, `INSERT INTO video_metadata(video_id,duration_seconds) VALUES ($1,1)`, video)
	change(1, `INSERT INTO video_tags(video_id,tag) VALUES ($1,'first'),($1,'second')`, video)
	change(1, `INSERT INTO video_blocks(video_id) VALUES ($1)`, video)
	for _, statement := range []string{
		`UPDATE videos SET title=title`, `UPDATE videos SET title='absent' WHERE false`,
		`INSERT INTO video_tags(video_id,tag) SELECT video_id,'absent' FROM video_tags WHERE false`,
		`DELETE FROM video_blocks WHERE false`,
		`UPDATE users SET is_active=NOT is_active`,
		`UPDATE video_metadata SET width=123`,
		`UPDATE video_blocks SET reason='changed audit detail'`,
		`INSERT INTO video_view_counts(video_id,views) VALUES ('` + video.String() + `',10)`,
	} {
		change(0, statement)
	}
	for _, statement := range []string{
		`UPDATE videos SET sensitive_flags=ARRAY['sex']`,
		`UPDATE videos SET privacy='public',state='published'`,
		`UPDATE videos SET description='changed',updated_at=clock_timestamp()`,
		`UPDATE channels SET display_name='renamed'`,
		`UPDATE users SET unlisted=NOT unlisted`,
		`UPDATE video_metadata SET duration_seconds=2`,
		`UPDATE video_tags SET tag=tag||'x'`,
	} {
		change(1, statement)
	}
	change(1, `DELETE FROM video_blocks WHERE video_id=$1`, video)
	change(1, `DELETE FROM video_tags WHERE video_id=$1`, video)
	change(1, `DELETE FROM video_metadata WHERE video_id=$1`, video)
	// Empty TRUNCATE still invalidates the catalog; cascades can run it while
	// old rows are not available as transition relations.
	change(1, `TRUNCATE video_tags`)
	change(1, `TRUNCATE video_tags`)
	before := count()
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Exec(ctx, `UPDATE channels SET display_name='rolled back' WHERE id=$1`, channel); err != nil {
		t.Fatal(err)
	}
	_ = sp.Rollback(ctx)
	if count() != before {
		t.Fatal("rolled-back canonical change retained a hint")
	}
	// If durable invalidation cannot be inserted, the business write must fail.
	sp, err = tx.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Exec(ctx, `ALTER TABLE search_catalog_dirty ADD CONSTRAINT catalog_test_reject CHECK(false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Exec(ctx, `UPDATE channels SET display_name='must fail' WHERE id=$1`, channel); err == nil {
		t.Fatal("canonical write succeeded without durable invalidation")
	}
	_ = sp.Rollback(ctx)
	var name string
	if err := tx.QueryRow(ctx, `SELECT display_name FROM channels WHERE id=$1`, channel).Scan(&name); err != nil || name != "renamed" {
		t.Fatalf("failed write changed canonical state: %q, %v", name, err)
	}
	change(1, `DELETE FROM videos WHERE channel_id=$1`, channel)
	change(1, `DELETE FROM channels WHERE id=$1`, channel)
	change(1, `DELETE FROM users WHERE id=$1`, owner)
	var installed, unique int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname LIKE 'search_catalog_dirty_%'`).Scan(&installed); err != nil || installed != 24 {
		t.Fatalf("missing statement triggers: %d, %v", installed, err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(DISTINCT id) FROM search_catalog_dirty`).Scan(&unique); err != nil || unique != count() {
		t.Fatalf("hint identities reused: %d, %v", unique, err)
	}
}
