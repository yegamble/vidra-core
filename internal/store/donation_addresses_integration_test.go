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

// seedUserAndChannel seeds a user and one channel they own, returning both ids
// plus a cleanup that cascades everything via the users ON DELETE CASCADE.
func seedUserAndChannel(t *testing.T, st *Store) (userID, channelID uuid.UUID, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"don-"+suffix, "don-"+suffix+"@example.test",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	cleanup = func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) }
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Don') RETURNING id`,
		userID, "don_"+suffix,
	).Scan(&channelID); err != nil {
		cleanup()
		t.Fatalf("seed channel: %v", err)
	}
	return userID, channelID, cleanup
}

// TestDonationAddressQueriesPersist exercises the donation-address queries
// against real PostgreSQL: account/channel scoping, the COALESCE-based unique
// index (including that a NULL channel_id de-duplicates), the challenge
// round-trip, verification, and the ON DELETE CASCADE from users.
func TestDonationAddressQueriesPersist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	userID, channelID, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	// Account-level address (NULL channel_id).
	acct, err := q.CreateDonationAddress(ctx, sqlcgen.CreateDonationAddressParams{
		OwnerID: userID,
		Network: "ethereum",
		Address: "0x52908400098527886E0F7030069857D2E4169EE7",
		Label:   "Tips",
	})
	if err != nil {
		t.Fatalf("create account-level: %v", err)
	}
	if acct.ChannelID.Valid {
		t.Error("account-level address should have NULL channel_id")
	}

	// Channel-scoped address.
	if _, err := q.CreateDonationAddress(ctx, sqlcgen.CreateDonationAddressParams{
		OwnerID:   userID,
		ChannelID: pgtype.UUID{Bytes: channelID, Valid: true},
		Network:   "bitcoin",
		Address:   "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq",
	}); err != nil {
		t.Fatalf("create channel-scoped: %v", err)
	}

	// The COALESCE unique index must reject a duplicate account-level address
	// (two NULL channel_ids collide via the sentinel, unlike a plain UNIQUE).
	if _, err := q.CreateDonationAddress(ctx, sqlcgen.CreateDonationAddressParams{
		OwnerID: userID,
		Network: "ethereum",
		Address: "0x52908400098527886E0F7030069857D2E4169EE7",
	}); err == nil {
		t.Fatal("expected a unique-violation on a duplicate account-level address")
	}

	// Owner listing sees both; account listing sees only the NULL-scoped one;
	// channel listing sees only the channel-scoped one.
	if all, err := q.ListDonationAddressesByOwner(ctx, userID); err != nil || len(all) != 2 {
		t.Fatalf("ListDonationAddressesByOwner = %d rows (err %v), want 2", len(all), err)
	}
	if acctList, err := q.ListAccountDonationAddresses(ctx, userID); err != nil || len(acctList) != 1 {
		t.Fatalf("ListAccountDonationAddresses = %d rows (err %v), want 1", len(acctList), err)
	}
	chList, err := q.ListChannelDonationAddresses(ctx, pgtype.UUID{Bytes: channelID, Valid: true})
	if err != nil || len(chList) != 1 || chList[0].Network != "bitcoin" {
		t.Fatalf("ListChannelDonationAddresses = %+v (err %v), want 1 bitcoin", chList, err)
	}

	// Challenge round-trip: set nonce/expiry, then MarkVerified clears them.
	nonce := "deadbeefdeadbeefdeadbeefdeadbeef"
	expires := pgtype.Timestamptz{Time: time.Now().Add(10 * time.Minute), Valid: true}
	challenged, err := q.SetDonationAddressChallenge(ctx, sqlcgen.SetDonationAddressChallengeParams{
		ID: acct.ID, VerificationNonce: &nonce, VerificationExpiresAt: expires,
	})
	if err != nil || challenged.VerificationNonce == nil || *challenged.VerificationNonce != nonce {
		t.Fatalf("SetDonationAddressChallenge did not persist the nonce: %+v (err %v)", challenged, err)
	}
	verified, err := q.MarkDonationAddressVerified(ctx, acct.ID)
	if err != nil {
		t.Fatalf("MarkDonationAddressVerified: %v", err)
	}
	if !verified.Verified || verified.VerificationNonce != nil || verified.VerificationExpiresAt.Valid {
		t.Fatalf("verified row should be true with cleared challenge: %+v", verified)
	}

	// Delete one; the other remains.
	if err := q.DeleteDonationAddress(ctx, acct.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if all, err := q.ListDonationAddressesByOwner(ctx, userID); err != nil || len(all) != 1 {
		t.Fatalf("after delete = %d rows (err %v), want 1", len(all), err)
	}

	// Deleting the user cascades the remaining donation address away.
	cleanup()
	if all, err := q.ListDonationAddressesByOwner(ctx, userID); err != nil || len(all) != 0 {
		t.Fatalf("after user cascade = %d rows (err %v), want 0", len(all), err)
	}
}
