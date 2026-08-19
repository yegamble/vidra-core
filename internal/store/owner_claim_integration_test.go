//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestOwnerClaimTokenLifecycle proves the owner_claim_tokens schema (0104) and
// its sqlc queries against real PostgreSQL: the single-row upsert (re-mint
// replaces the hash and clears claimed_at), the unclaimed fetch, the atomic
// claim-and-create-admin CTE, and — the property the whole flow rests on —
// that the claimed_at-IS-NULL guard is single-winner under real concurrency
// and that a unique violation rolls the claim back, leaving the token live.
func TestOwnerClaimTokenLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := time.Now().UnixNano()
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM owner_claim_tokens`)
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE username LIKE 'owner-it-%'`)
	})

	// Mint, then re-mint: the single row is replaced (fresh hash, unclaimed).
	if _, err := q.UpsertOwnerClaimToken(ctx, fmt.Sprintf("hash-old-%d", suffix)); err != nil {
		t.Fatalf("mint: %v", err)
	}
	hash := fmt.Sprintf("hash-%d", suffix)
	if _, err := q.UpsertOwnerClaimToken(ctx, hash); err != nil {
		t.Fatalf("re-mint: %v", err)
	}
	row, err := q.GetUnclaimedOwnerClaimToken(ctx)
	if err != nil || row.TokenHash != hash || row.ClaimedAt.Valid {
		t.Fatalf("unclaimed row = %+v err=%v, want the re-minted hash, unclaimed", row, err)
	}

	// A claim keyed by a superseded/wrong hash matches nothing and creates nothing.
	if _, err := q.ClaimOwnerAndCreateAdmin(ctx, sqlcgen.ClaimOwnerAndCreateAdminParams{
		TokenHash: fmt.Sprintf("hash-old-%d", suffix),
		Username:  fmt.Sprintf("owner-it-wrong-%d", suffix),
		Email:     fmt.Sprintf("owner-it-wrong-%d@example.test", suffix),
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim with superseded hash: err = %v, want pgx.ErrNoRows", err)
	}
	if _, err := q.GetUnclaimedOwnerClaimToken(ctx); err != nil {
		t.Fatalf("token must survive a failed claim: %v", err)
	}

	// Concurrent claims: exactly one wins the claimed_at-IS-NULL row lock.
	const claimers = 8
	var wg sync.WaitGroup
	results := make([]error, claimers)
	rows := make([]sqlcgen.ClaimOwnerAndCreateAdminRow, claimers)
	for i := 0; i < claimers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rows[i], results[i] = q.ClaimOwnerAndCreateAdmin(ctx, sqlcgen.ClaimOwnerAndCreateAdminParams{
				TokenHash:      hash,
				Username:       fmt.Sprintf("owner-it-%d-%d", suffix, i),
				Email:          fmt.Sprintf("owner-it-%d-%d@example.test", suffix, i),
				PasswordHash:   "x",
				HistoryEnabled: true,
			})
		}(i)
	}
	wg.Wait()
	winners := 0
	for i, err := range results {
		switch {
		case err == nil:
			winners++
			if rows[i].Role != "admin" {
				t.Errorf("winner role = %q, want admin", rows[i].Role)
			}
		case errors.Is(err, pgx.ErrNoRows):
			// expected loser
		default:
			t.Errorf("claimer %d: unexpected error %v", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}

	// Claimed: no unclaimed row remains, and a replay finds nothing.
	if _, err := q.GetUnclaimedOwnerClaimToken(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("post-claim unclaimed fetch: err = %v, want pgx.ErrNoRows", err)
	}

	// Unique-violation rollback: re-mint, then claim with an already-taken
	// username — the whole statement rolls back, leaving the token unclaimed.
	winnerName := ""
	for i := range results {
		if results[i] == nil {
			winnerName = rows[i].Username
		}
	}
	hash2 := fmt.Sprintf("hash2-%d", suffix)
	if _, err := q.UpsertOwnerClaimToken(ctx, hash2); err != nil {
		t.Fatalf("re-mint after claim: %v", err)
	}
	if _, err := q.ClaimOwnerAndCreateAdmin(ctx, sqlcgen.ClaimOwnerAndCreateAdminParams{
		TokenHash:      hash2,
		Username:       winnerName, // duplicate → unique violation
		Email:          fmt.Sprintf("owner-it-dup-%d@example.test", suffix),
		PasswordHash:   "x",
		HistoryEnabled: true,
	}); err == nil || errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("duplicate-username claim: err = %v, want a unique violation", err)
	}
	if row, err := q.GetUnclaimedOwnerClaimToken(ctx); err != nil || row.TokenHash != hash2 {
		t.Fatalf("token must survive a rolled-back claim: row=%+v err=%v", row, err)
	}
}
