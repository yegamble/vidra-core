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

// One handle namespace for accounts and channels (migration 0142, A29 parity).
//
// These are DATABASE tests on purpose. The reservation is a table with a primary
// key maintained by triggers, and that is the whole point of the design: two of
// the four surfaces SC1 names — a username change and a channel handle change —
// have no endpoint today, so an application-layer guard would be a rule that
// covers the two paths that exist and none of the paths that will. Proving the
// refusal at the trigger proves it for every writer, including future ones.

// TestChannelCannotTakeAnAccountHandle and its sibling are the two directions of
// the same rule, and they are separate tests because a single one passing while
// the other silently regressed is exactly the shape of the bug the rehearsal
// found (WebFinger resolved the account first and stopped).
func TestChannelCannotTakeAnAccountHandle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	userID, _, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	var username string
	if err := st.Pool.QueryRow(ctx, `SELECT username FROM users WHERE id = $1`, userID).Scan(&username); err != nil {
		t.Fatalf("read username: %v", err)
	}

	// The CASE of the handle must not matter: usernames are unique under
	// lower(), and a namespace that were case-sensitive would let `OwnerA` the
	// channel shadow `ownera` the account on a WebFinger that lower-cases.
	_, err = st.Pool.Exec(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'x')`,
		userID, strings.ToUpper(username))
	if !pgconv.IsHandleReserved(err) {
		t.Fatalf("a channel taking an account's handle must be refused by the reservation, got %v (constraint %q)",
			err, pgconv.ConstraintName(err))
	}
}

func TestAccountCannotTakeAChannelHandle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	_, channelID, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	var handle string
	if err := st.Pool.QueryRow(ctx, `SELECT handle FROM channels WHERE id = $1`, channelID).Scan(&handle); err != nil {
		t.Fatalf("read handle: %v", err)
	}

	_, err = st.Pool.Exec(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x')`,
		strings.ToUpper(handle), uuid.NewString()+"@example.test")
	if !pgconv.IsHandleReserved(err) {
		t.Fatalf("an account taking a channel's handle must be refused by the reservation, got %v (constraint %q)",
			err, pgconv.ConstraintName(err))
	}
}

// TestRenamingIntoTheOtherNamespaceIsRefused covers the two surfaces that have
// no endpoint yet — a username change and a channel handle change. The trigger
// fires on UPDATE OF, so the guard is in place BEFORE the endpoint that would
// need it, which is the only ordering that cannot ship a hole.
func TestRenamingIntoTheOtherNamespaceIsRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	userID, channelID, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	var username, handle string
	if err := st.Pool.QueryRow(ctx,
		`SELECT u.username, c.handle FROM users u JOIN channels c ON c.id = $2 WHERE u.id = $1`,
		userID, channelID).Scan(&username, &handle); err != nil {
		t.Fatalf("read names: %v", err)
	}

	if _, err := st.Pool.Exec(ctx, `UPDATE users SET username = $2 WHERE id = $1`, userID, handle); !pgconv.IsHandleReserved(err) {
		t.Fatalf("a username change onto a channel handle must be refused, got %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE channels SET handle = $2 WHERE id = $1`, channelID, username); !pgconv.IsHandleReserved(err) {
		t.Fatalf("a channel handle change onto a username must be refused, got %v", err)
	}

	// A rename that does NOT cross namespaces still works, and MOVES the
	// reservation rather than leaving the old name held forever.
	fresh := "moved-" + uuid.NewString()[:8]
	if _, err := st.Pool.Exec(ctx, `UPDATE channels SET handle = $2 WHERE id = $1`, channelID, fresh); err != nil {
		t.Fatalf("an ordinary channel rename must succeed: %v", err)
	}
	var held string
	if err := st.Pool.QueryRow(ctx, `SELECT handle_lower FROM actor_handles WHERE channel_id = $1`, channelID).Scan(&held); err != nil {
		t.Fatalf("read reservation: %v", err)
	}
	if held != fresh {
		t.Fatalf("the reservation must follow the rename: got %q, want %q", held, fresh)
	}
	var stale int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM actor_handles WHERE handle_lower = $1`, strings.ToLower(handle)).Scan(&stale); err != nil {
		t.Fatalf("count stale: %v", err)
	}
	if stale != 0 {
		t.Fatalf("the old handle must be released, %d rows still hold it", stale)
	}
}

// TestSameKindDuplicatesKeepTheirOwnConstraint pins the reason the triggers are
// AFTER triggers: a user taking another user's username must still be refused by
// users_username_lower_idx, so every 409 message that shipped before this
// migration is byte-identical after it. Only a CROSS-kind collision is the new
// refusal.
func TestSameKindDuplicatesKeepTheirOwnConstraint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	userID, channelID, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	var username, handle string
	if err := st.Pool.QueryRow(ctx,
		`SELECT u.username, c.handle FROM users u JOIN channels c ON c.id = $2 WHERE u.id = $1`,
		userID, channelID).Scan(&username, &handle); err != nil {
		t.Fatalf("read names: %v", err)
	}

	_, err = st.Pool.Exec(ctx, `INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x')`,
		username, uuid.NewString()+"@example.test")
	if !pgconv.IsUniqueViolation(err) || pgconv.IsHandleReserved(err) {
		t.Fatalf("a duplicate username must still violate users_username_lower_idx, got %v (constraint %q)",
			err, pgconv.ConstraintName(err))
	}
	_, err = st.Pool.Exec(ctx, `INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'x')`,
		userID, handle)
	if !pgconv.IsUniqueViolation(err) || pgconv.IsHandleReserved(err) {
		t.Fatalf("a duplicate channel handle must still violate channels_handle_lower_idx, got %v (constraint %q)",
			err, pgconv.ConstraintName(err))
	}
}

// TestDeletingASubjectReleasesItsHandle proves the reservation cannot outlive the
// thing it reserves. It is a cascading foreign key rather than a trigger arm on
// purpose: a DELETE path that forgot to release would leave a name permanently
// unusable, and nothing would ever notice.
func TestDeletingASubjectReleasesItsHandle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	userID, channelID, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	var handle string
	if err := st.Pool.QueryRow(ctx, `SELECT handle FROM channels WHERE id = $1`, channelID).Scan(&handle); err != nil {
		t.Fatalf("read handle: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM channels WHERE id = $1`, channelID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	var held int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM actor_handles WHERE handle_lower = lower($1)`, handle).Scan(&held); err != nil {
		t.Fatalf("count: %v", err)
	}
	if held != 0 {
		t.Fatalf("a deleted channel must release its handle, %d rows still hold it", held)
	}
	// And the freed name is now takeable by the OTHER kind, which is what
	// "released" has to mean to be worth anything.
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET username = $2 WHERE id = $1`, userID, handle); err != nil {
		t.Fatalf("a released handle must be takeable by an account: %v", err)
	}
}

// TestBackfillRenamesTheChannelSideOfACollision exercises the migration's own
// rule on a SEEDED collision — the case a fresh database can never produce,
// because the triggers now prevent it. The triggers are disabled for the seed
// (which is exactly how the pre-0142 rows this backfill exists for came to be)
// and the backfill function is re-run.
//
// What it asserts is the whole documented rule: the ACCOUNT keeps the name, the
// channel takes the next free `-channel` suffix, the old handle survives as an
// alias, that alias is the FROZEN ActivityPub identity, and an audit row names
// both sides.
func TestBackfillRenamesTheChannelSideOfACollision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	name := "clash" + uuid.NewString()[:8]
	var userID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		name, name+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) }()

	// Seed the collision the way history made it: with the reservation asleep.
	if _, err := st.Pool.Exec(ctx, `ALTER TABLE channels DISABLE TRIGGER channels_reserve_handle`); err != nil {
		t.Skipf("cannot disable the reservation trigger as this role: %v", err)
	}
	var clashID, decoyID uuid.UUID
	err = st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Clash') RETURNING id`,
		userID, name).Scan(&clashID)
	if err == nil {
		// A decoy on the first candidate, so the suffix loop has to advance.
		err = st.Pool.QueryRow(ctx,
			`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Decoy') RETURNING id`,
			userID, name+"-channel").Scan(&decoyID)
	}
	if _, rerr := st.Pool.Exec(ctx, `ALTER TABLE channels ENABLE TRIGGER channels_reserve_handle`); rerr != nil {
		t.Fatalf("re-enable trigger: %v", rerr)
	}
	if err != nil {
		t.Fatalf("seed colliding channels: %v", err)
	}

	var renamed int
	if err := st.Pool.QueryRow(ctx, `SELECT backfill_actor_handles()`).Scan(&renamed); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if renamed < 1 {
		t.Fatalf("the backfill must rename the colliding channel, renamed=%d", renamed)
	}

	var newHandle string
	if err := st.Pool.QueryRow(ctx, `SELECT handle FROM channels WHERE id = $1`, clashID).Scan(&newHandle); err != nil {
		t.Fatalf("read renamed handle: %v", err)
	}
	if want := name + "-channel-2"; newHandle != want {
		t.Fatalf("deterministic rename: got %q, want %q (the first candidate was taken)", newHandle, want)
	}

	// The ACCOUNT keeps the contested name.
	var kind string
	if err := st.Pool.QueryRow(ctx,
		`SELECT CASE WHEN user_id IS NOT NULL THEN 'account' ELSE 'channel' END
		   FROM actor_handles WHERE handle_lower = lower($1)`, name).Scan(&kind); err != nil {
		t.Fatalf("read reservation: %v", err)
	}
	if kind != "account" {
		t.Fatalf("the account must keep the contested handle, it is held by a %s", kind)
	}

	// The old handle keeps resolving, and it is the frozen federated identity.
	var aliasChannel uuid.UUID
	var isActorID bool
	if err := st.Pool.QueryRow(ctx,
		`SELECT channel_id, is_actor_id FROM channel_handle_aliases WHERE handle_lower = lower($1)`,
		name).Scan(&aliasChannel, &isActorID); err != nil {
		t.Fatalf("read alias: %v", err)
	}
	if aliasChannel != clashID || !isActorID {
		t.Fatalf("the alias must point at the renamed channel and carry its frozen actor id: channel=%v is_actor_id=%v", aliasChannel, isActorID)
	}

	// And the rename is on the record, with both names.
	var reason string
	if err := st.Pool.QueryRow(ctx,
		`SELECT reason FROM audit_log
		  WHERE action = 'content.channel.handle_renamed' AND resource_id = $1
		  ORDER BY occurred_at DESC LIMIT 1`, clashID.String()).Scan(&reason); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if want := "from=" + name + " to=" + newHandle; reason != want {
		t.Fatalf("audit reason: got %q, want %q", reason, want)
	}
	_ = decoyID
}
