//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The ban explanation (0154): upsert replaces, ListUsers joins it onto the row
// (and leaves un-banned rows null), delete clears it, and the CHECK refuses an
// over-long reason.
func TestUserBanExplanationRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := sqlcgen.New(st.Pool)

	suffix := uuid.NewString()[:8]
	seed := func(name string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash, role) VALUES ($1, $2, '', 'user') RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	mod, target, bystander := seed("qmod"), seed("qtarget"), seed("qbystander")

	by := pgtype.UUID{Bytes: mod, Valid: true}
	if err := q.UpsertUserBan(ctx, sqlcgen.UpsertUserBanParams{UserID: target, Reason: "first", BannedBy: by}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := q.UpsertUserBan(ctx, sqlcgen.UpsertUserBanParams{UserID: target, Reason: "second", BannedBy: by}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	b, err := q.GetUserBan(ctx, target)
	if err != nil || b.Reason != "second" || !b.BannedBy.Valid {
		t.Fatalf("GetUserBan = (%+v, %v), want reason second", b, err)
	}

	rows, err := q.ListUsers(ctx, sqlcgen.ListUsersParams{Query: "-" + suffix, ResultLimit: 10})
	if err != nil || len(rows) != 3 {
		t.Fatalf("ListUsers = (%d rows, %v), want 3", len(rows), err)
	}
	for _, r := range rows {
		switch r.ID {
		case target:
			if r.BanReason == nil || *r.BanReason != "second" || !r.BannedAt.Valid || r.BannedBy != by {
				t.Errorf("banned row = %+v", r)
			}
		case bystander, mod:
			if r.BanReason != nil || r.BannedAt.Valid {
				t.Errorf("un-banned row carries a ban: %+v", r)
			}
		}
	}

	long := make([]byte, 501)
	for i := range long {
		long[i] = 'x'
	}
	if err := q.UpsertUserBan(ctx, sqlcgen.UpsertUserBanParams{UserID: bystander, Reason: string(long)}); err == nil {
		t.Error("a 501-character reason was accepted")
	}

	if err := q.DeleteUserBan(ctx, target); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := q.GetUserBan(ctx, target); err == nil {
		t.Error("ban survived DeleteUserBan")
	}
}
