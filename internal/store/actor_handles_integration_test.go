//go:build integration

package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/channel"
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
// channel takes the next free `_channel` suffix, the old handle survives as an
// alias, that alias is the FROZEN ActivityPub identity, and an audit row names
// both sides.
//
// The suffix is `_channel` and not `-channel` since migration 0143: the A29
// rehearsal-3 lab watched 0142 mint `creatora-channel-2`, a name this
// instance's own POST /channels refuses with 422 "must be 3–30 chars: letters,
// digits, or underscore" — so the migration handed an operator a handle they
// could neither retype nor re-create. The final assertion here is the one that
// keeps the two rules together: whatever the migration mints must pass the
// validator, checked against the validator itself rather than against a second
// copy of its regexp.
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
			userID, name+"_channel").Scan(&decoyID)
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
	if want := name + "_channel2"; newHandle != want {
		t.Fatalf("deterministic rename: got %q, want %q (the first candidate was taken)", newHandle, want)
	}
	if !channel.ValidateChannelHandle(newHandle) {
		t.Fatalf("the migration minted %q, which POST /channels refuses — the rename rule and the create rule disagree again", newHandle)
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

// TestSecondRenameMovesAHyphenHandleIntoTheValidatorAlphabet is migration
// 0143's other half: the instances that already ran 0142 are carrying names the
// new rule would never have minted, and a fix that only reached databases which
// had not upgraded yet would reach nobody who hit the bug.
//
// The seed is exactly what the rehearsal-3 lab produced on instance A: an
// account holding the contested name, a channel renamed to `<name>-channel-2`,
// and the alias row 0142 wrote — the old name, carrying the FROZEN ActivityPub
// identity. What the second rename must do, and must not do:
//
//	the handle moves to `<name>_channel`   — the validator's alphabet
//	`<name>-channel-2` becomes an ALIAS    — 301 for a year, so anyone who
//	                                         learned the interim name in the
//	                                         window between two upgrades is
//	                                         not sent to a 404
//	the FROZEN actor id does NOT move      — peers hold it; 0142 put it on
//	                                         the ORIGINAL name and it stays
//	a SECOND audit row is written          — a ledger that recorded the first
//	                                         rename and not the second would
//	                                         describe a channel under a name
//	                                         it no longer has
func TestSecondRenameMovesAHyphenHandleIntoTheValidatorAlphabet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	name := "hyph" + uuid.NewString()[:8]
	var userID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		name, name+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) }()

	// The post-0142 shape, verbatim: the channel already renamed, the old name
	// held as the frozen actor id.
	interim := name + "-channel-2"
	var chID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Renamed once') RETURNING id`,
		userID, interim).Scan(&chID); err != nil {
		t.Fatalf("seed renamed channel: %v", err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO channel_handle_aliases (handle_lower, channel_id, is_actor_id, expires_at)
		 VALUES (lower($1), $2, TRUE, now() + INTERVAL '365 days')`, name, chID); err != nil {
		t.Fatalf("seed frozen alias: %v", err)
	}

	var renamed int
	if err := st.Pool.QueryRow(ctx,
		`SELECT rename_channel_handles_to_validator_alphabet()`).Scan(&renamed); err != nil {
		t.Fatalf("second rename: %v", err)
	}
	if renamed < 1 {
		t.Fatalf("the migration renamed nothing; a `-channel-2` handle is exactly what it exists for (renamed=%d)", renamed)
	}

	var got string
	if err := st.Pool.QueryRow(ctx, `SELECT handle FROM channels WHERE id = $1`, chID).Scan(&got); err != nil {
		t.Fatalf("read handle: %v", err)
	}
	if want := name + "_channel"; got != want {
		t.Fatalf("handle = %q, want %q (minted from the ORIGINAL name, not from the interim one)", got, want)
	}
	if !channel.ValidateChannelHandle(got) {
		t.Fatalf("the second rename minted %q, which POST /channels still refuses", got)
	}

	// The interim name keeps resolving — as an ordinary alias, NOT as an actor
	// id. Two aliases, one frozen identity.
	rows, err := st.Pool.Query(ctx,
		`SELECT handle_lower, is_actor_id, expires_at > now() + INTERVAL '360 days'
		   FROM channel_handle_aliases WHERE channel_id = $1 ORDER BY handle_lower`, chID)
	if err != nil {
		t.Fatalf("read aliases: %v", err)
	}
	defer rows.Close()
	aliases := map[string]bool{}
	expiry := map[string]bool{}
	for rows.Next() {
		var h string
		var actorID, aYearOut bool
		if err := rows.Scan(&h, &actorID, &aYearOut); err != nil {
			t.Fatalf("scan alias: %v", err)
		}
		aliases[h] = actorID
		expiry[h] = aYearOut
	}
	if len(aliases) != 2 {
		t.Fatalf("aliases = %v, want both the original name and the interim one", aliases)
	}
	if !aliases[strings.ToLower(name)] {
		t.Error("the ORIGINAL name must keep the frozen actor id: peers have been talking to it since before either rename")
	}
	if aliases[strings.ToLower(interim)] {
		t.Error("the interim name became a second actor id; an actor id is single-valued by definition")
	}
	if !expiry[strings.ToLower(interim)] {
		t.Error("the interim alias must redirect for a year, the same courtesy 0142 gave the original name")
	}

	// Both renames are on the record, newest first, each with its own cause.
	causes, err := st.Pool.Query(ctx,
		`SELECT metadata ->> 'cause', reason FROM audit_log
		  WHERE action = 'content.channel.handle_renamed' AND resource_id = $1
		  ORDER BY occurred_at DESC`, chID.String())
	if err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	defer causes.Close()
	var seen []string
	for causes.Next() {
		var cause, reason string
		if err := causes.Scan(&cause, &reason); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		seen = append(seen, cause)
		if cause == "handle_validator_alphabet" {
			if want := "from=" + interim + " to=" + got; reason != want {
				t.Errorf("audit reason = %q, want %q", reason, want)
			}
		}
	}
	if len(seen) != 1 || seen[0] != "handle_validator_alphabet" {
		// The 0142 row is absent here only because this test seeded the
		// post-0142 state by hand rather than by running the backfill.
		t.Errorf("audit causes = %v, want exactly the second rename", seen)
	}

	// Idempotent: the channel no longer matches, so a re-run is a no-op.
	if err := st.Pool.QueryRow(ctx,
		`SELECT rename_channel_handles_to_validator_alphabet()`).Scan(&renamed); err != nil {
		t.Fatalf("second rename, again: %v", err)
	}
	var after string
	if err := st.Pool.QueryRow(ctx, `SELECT handle FROM channels WHERE id = $1`, chID).Scan(&after); err != nil {
		t.Fatalf("read handle: %v", err)
	}
	if after != got {
		t.Errorf("a second run renamed again: %q -> %q", got, after)
	}
}

// TestMintChannelHandleAlwaysPassesTheValidator is the rule itself, pushed at
// the shapes a real database can hold rather than at the tidy ones. The base
// comes from a handle that already EXISTS here — rows predating the current
// validator, or written by the PeerTube importer — so it may carry anything.
func TestMintChannelHandleAlwaysPassesTheValidator(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	bases := []string{
		"creatora",
		"a",                                 // shorter than the floor on its own
		"",                                  // nothing at all
		"has-a-hyphen",                      // the shape 0142 itself minted
		"user.name+tag@thing",               // punctuation an importer can carry
		strings.Repeat("x", 40),             // past the 30-char ceiling
		strings.Repeat("y", 29) + "-suffix", // past it once the suffix is added
		"ünïcøde",                           // outside the alphabet entirely
	}
	for _, base := range bases {
		t.Run(base, func(t *testing.T) {
			var minted string
			if err := st.Pool.QueryRow(ctx, `SELECT mint_channel_handle($1)`, base).Scan(&minted); err != nil {
				t.Fatalf("mint_channel_handle(%q): %v", base, err)
			}
			if !channel.ValidateChannelHandle(minted) {
				t.Fatalf("mint_channel_handle(%q) = %q, which POST /channels refuses", base, minted)
			}
		})
	}
}
