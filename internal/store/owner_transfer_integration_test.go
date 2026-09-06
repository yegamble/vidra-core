//go:build integration

package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/pgconv"
)

// seedOwnerTransferUsers inserts a marked owner and n other live admins, plus
// one ordinary user, and returns their ids. It cleans up after itself: this
// suite shares one database with every other integration test, and a stray
// is_owner row would make the next one's single-owner assertions lie.
func seedOwnerTransferUsers(t *testing.T, st *Store, n int) (owner uuid.UUID, admins []uuid.UUID, plain uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	ins := func(name, role string, isOwner bool) uuid.UUID {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash, role, is_owner)
			 VALUES ($1, $2, 'x', $3, $4) RETURNING id`,
			name+suffix, name+suffix+"@example.test", role, isOwner,
		).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	// Any owner left behind by an earlier run would make this instance look
	// already-owned; clear the marker (not the rows) before seeding ours.
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET is_owner = FALSE WHERE is_owner`); err != nil {
		t.Fatalf("clear stray owner markers: %v", err)
	}
	owner = ins("owner", "admin", true)
	for i := 0; i < n; i++ {
		admins = append(admins, ins("admin"+string(rune('a'+i)), "admin", false))
	}
	plain = ins("plain", "user", false)
	return owner, admins, plain
}

func ownerRow(t *testing.T, st *Store) (uuid.UUID, int) {
	t.Helper()
	rows, err := st.Pool.Query(context.Background(), `SELECT id FROM users WHERE is_owner`)
	if err != nil {
		t.Fatalf("read owner: %v", err)
	}
	defer rows.Close()
	var owner uuid.UUID
	n := 0
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan owner: %v", err)
		}
		owner, n = id, n+1
	}
	return owner, n
}

// TestTransferInstanceOwnerIsOneAtomicSwap runs the ownership transfer against
// real PostgreSQL, which is the only place its claims can be checked: the
// statement's correctness rests on the partial unique index
// users_single_owner_idx and on PostgreSQL's own execution order for a
// data-modifying CTE, and an in-memory fake can mirror neither.
func TestTransferInstanceOwnerIsOneAtomicSwap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	owner, admins, plain := seedOwnerTransferUsers(t, st, 2)

	// The happy path: the marker moves, exactly one row holds it afterwards, and
	// the statement reports the one row it cleared.
	row, err := q.TransferInstanceOwner(ctx, admins[0])
	if err != nil {
		t.Fatalf("TransferInstanceOwner: %v", err)
	}
	if row.ID != admins[0] || row.PreviousOwnersCleared != 1 {
		t.Errorf("transfer returned %+v, want the new owner and one cleared marker", row)
	}
	if got, n := ownerRow(t, st); got != admins[0] || n != 1 {
		t.Fatalf("owner after the transfer = %s (%d rows), want %s (1)", got, n, admins[0])
	}
	// The former owner keeps its admin role.
	var role string
	if err := st.Pool.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, owner).Scan(&role); err != nil {
		t.Fatalf("read former owner: %v", err)
	}
	if role != "admin" {
		t.Errorf("former owner's role = %q, want admin — the transfer moves the marker, not the role", role)
	}

	// Ineligible targets change NOTHING — including the clear, which carries the
	// same test. A transfer that cleared and then failed to set would leave the
	// instance unowned, which is the state this route exists to prevent.
	for name, target := range map[string]uuid.UUID{
		"an ordinary user": plain,
		"an unknown id":    uuid.New(),
	} {
		if _, err := q.TransferInstanceOwner(ctx, target); err == nil {
			t.Errorf("transfer to %s succeeded", name)
		}
		if got, n := ownerRow(t, st); got != admins[0] || n != 1 {
			t.Fatalf("owner after a refused transfer to %s = %s (%d rows), want %s (1)", name, got, n, admins[0])
		}
	}
	// A deactivated admin, and a tombstoned one.
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET is_active = FALSE WHERE id = $1`, admins[1]); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if _, err := q.TransferInstanceOwner(ctx, admins[1]); err == nil {
		t.Error("transfer to a deactivated admin succeeded")
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET is_active = TRUE, deleted_at = now() WHERE id = $1`, admins[1]); err != nil {
		t.Fatalf("tombstone: %v", err)
	}
	if _, err := q.TransferInstanceOwner(ctx, admins[1]); err == nil {
		t.Error("transfer to a tombstoned admin succeeded")
	}
	if got, n := ownerRow(t, st); got != admins[0] || n != 1 {
		t.Fatalf("owner after the ineligible-target attempts = %s (%d rows), want %s (1)", got, n, admins[0])
	}
}

// TestConcurrentOwnerTransfersCannotBothWin is the race the ruling asked to see
// closed, forced rather than hoped for: two transfers to DIFFERENT admins are
// interleaved deliberately, with the first held open until the second is
// provably blocked on its row lock. Firing both from goroutines proves nothing —
// they serialize almost every time and both legitimately succeed in order.
//
// What must hold is that the two cannot both END UP owning the instance. The
// second transfer blocks trying to clear the marker the first is still holding;
// when the first commits, the second re-evaluates under READ COMMITTED, finds
// nothing left to clear, and its own set is refused by users_single_owner_idx.
// One winner, one constraint violation, one marked account — no silent overwrite
// and no unowned instance.
func TestConcurrentOwnerTransfersCannotBothWin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	_, admins, _ := seedOwnerTransferUsers(t, st, 2)

	c1, err := st.Pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	defer c1.Release()
	c2, err := st.Pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}
	defer c2.Release()
	tx1, err := c1.Begin(ctx)
	if err != nil {
		t.Fatalf("begin 1: %v", err)
	}
	defer func() { _ = tx1.Rollback(context.Background()) }()
	tx2, err := c2.Begin(ctx)
	if err != nil {
		t.Fatalf("begin 2: %v", err)
	}
	defer func() { _ = tx2.Rollback(context.Background()) }()

	if _, err := q.WithTx(tx1).TransferInstanceOwner(ctx, admins[0]); err != nil {
		t.Fatalf("first transfer: %v", err)
	}

	second := make(chan error, 1)
	go func() {
		_, err := q.WithTx(tx2).TransferInstanceOwner(context.Background(), admins[1])
		second <- err
	}()
	select {
	case err := <-second:
		t.Fatalf("the second transfer did not block on the first's uncommitted marker (err=%v) — the swap is not taking the row lock it relies on", err)
	case <-time.After(500 * time.Millisecond):
		// Blocked, as designed.
	}

	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("commit 1: %v", err)
	}
	err = <-second
	if !pgconv.IsUniqueViolation(err) {
		t.Fatalf("the second transfer returned %v, want a unique violation from users_single_owner_idx", err)
	}
	_ = tx2.Rollback(ctx)

	got, n := ownerRow(t, st)
	if n != 1 || got != admins[0] {
		t.Fatalf("owner after the interleaved transfers = %s (%d rows), want %s (1)", got, n, admins[0])
	}
}
