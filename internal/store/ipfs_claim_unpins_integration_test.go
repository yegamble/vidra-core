//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestClaimDueIPFSUnpinsClaimsOnlyRemovals: the F3 over-budget claim returns
// 'unpinning' rows and leaves 'pending' ones unleased.
func TestClaimDueIPFSUnpinsClaimsOnlyRemovals(t *testing.T) {
	ctx := context.Background()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	pend, gone := "claimtest/"+uuid.NewString(), "claimtest/"+uuid.NewString()
	for key, state := range map[string]string{pend: "pending", gone: "unpinning"} {
		if _, err := tx.Exec(ctx, "INSERT INTO media_ipfs_pins(object_key,media_class,state,next_attempt_at) VALUES($1,'caption',$2, now() - interval '1 hour')", key, state); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.Queries().WithTx(tx).ClaimDueIPFSUnpins(ctx, sqlcgen.ClaimDueIPFSUnpinsParams{Network: "public", LeaseSeconds: 120, BatchSize: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var sawGone bool
	for _, r := range rows {
		if r.ObjectKey == pend {
			t.Fatal("a pending row was claimed by the removals-only query")
		}
		if r.ObjectKey == gone {
			sawGone = true
		}
	}
	if !sawGone {
		t.Fatal("the unpinning row was not claimed")
	}
	var leased bool
	if err := tx.QueryRow(ctx, "SELECT next_attempt_at < now() FROM media_ipfs_pins WHERE object_key=$1", pend).Scan(&leased); err != nil || !leased {
		t.Fatalf("pending row lease touched: leased=%v err=%v", leased, err)
	}
}
