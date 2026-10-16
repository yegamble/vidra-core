//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The per-user daily quota override (0171): upsert replaces, absence is
// pgx.ErrNoRows (= instance default), delete resets, a negative value is
// refused by the CHECK, ListUsers joins the override and the trailing-24h
// ledger sum, and deleting the user cascades the row.
func TestUserDailyQuotaRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := sqlcgen.New(st.Pool)

	suffix := uuid.NewString()[:8]
	var withQuota, plain uuid.UUID
	for name, dst := range map[string]*uuid.UUID{"dqa": &withQuota, "dqb": &plain} {
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash, role) VALUES ($1, $2, '', 'user') RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test").Scan(dst); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		id := *dst
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
	}

	if _, err := q.GetUserDailyQuota(ctx, withQuota); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("no override: err = %v, want pgx.ErrNoRows", err)
	}
	for _, v := range []int64{100, 40} {
		if err := q.UpsertUserDailyQuota(ctx, sqlcgen.UpsertUserDailyQuotaParams{UserID: withQuota, DailyQuotaBytes: v}); err != nil {
			t.Fatalf("upsert %d: %v", v, err)
		}
	}
	if v, err := q.GetUserDailyQuota(ctx, withQuota); err != nil || v != 40 {
		t.Fatalf("GetUserDailyQuota = (%d, %v), want 40", v, err)
	}
	if err := q.UpsertUserDailyQuota(ctx, sqlcgen.UpsertUserDailyQuotaParams{UserID: withQuota, DailyQuotaBytes: -1}); err == nil {
		t.Fatal("negative override accepted; CHECK should refuse it")
	}

	if err := q.RecordUploadUsageEvent(ctx, sqlcgen.RecordUploadUsageEventParams{UserID: withQuota, Bytes: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO upload_usage_events (user_id, bytes, created_at) VALUES ($1, 1000, now() - interval '25 hours')`, withQuota); err != nil {
		t.Fatal(err)
	}
	rows, err := q.ListUsers(ctx, sqlcgen.ListUsersParams{Query: "-" + suffix, ResultLimit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("ListUsers = (%d rows, %v), want 2", len(rows), err)
	}
	for _, r := range rows {
		switch r.ID {
		case withQuota:
			if r.DailyQuotaBytes == nil || *r.DailyQuotaBytes != 40 || r.DailyUsedBytes != 7 {
				t.Errorf("row = quota %v used %d, want 40/7 (25h-old event excluded)", r.DailyQuotaBytes, r.DailyUsedBytes)
			}
		case plain:
			if r.DailyQuotaBytes != nil || r.DailyUsedBytes != 0 {
				t.Errorf("plain row = quota %v used %d, want null/0", r.DailyQuotaBytes, r.DailyUsedBytes)
			}
		}
	}

	if err := q.DeleteUserDailyQuota(ctx, withQuota); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetUserDailyQuota(ctx, withQuota); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("after delete: err = %v, want pgx.ErrNoRows", err)
	}
}
