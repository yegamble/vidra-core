//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The re-pin queries: the keyset page lists only pinned HLS directory rows after the
// cursor; the re-arm is guarded on the inspected CID, keeps cid/car_root and the
// policy reason, and refuses a row a newer writer moved.
func TestRepinQueries(t *testing.T) {
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
	q := st.Queries().WithTx(tx)
	pre := "repintest/" + uuid.NewString() + "/"
	hls, other, notDir := pre+"a/", pre+"b/", pre+"c"
	for _, r := range []struct{ key, class, state string }{{hls, "hls", "pinned"}, {other, "hls", "pending"}, {notDir, "hls", "pinned"}, {pre + "d/", "caption", "pinned"}} {
		if _, err := tx.Exec(ctx, "INSERT INTO media_ipfs_pins(object_key,media_class,state,cid,car_root,policy_reason) VALUES($1,$2,$3,'bafyold','bafyold','backfill')", r.key, r.class, r.state); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := q.ListIPFSPinsForRepin(ctx, sqlcgen.ListIPFSPinsForRepinParams{AfterKey: pre, BatchSize: 50})
	if err != nil || len(rows) != 1 || rows[0].ObjectKey != hls {
		t.Fatalf("page = %+v err=%v, want only the pinned hls directory row", rows, err)
	}
	if n, err := q.CountIPFSPinsForRepinAfter(ctx, hls); err != nil || n != 0 {
		t.Fatalf("remaining after the row = %d err=%v", n, err)
	}
	if n, _ := q.RearmIPFSPinForRepin(ctx, sqlcgen.RearmIPFSPinForRepinParams{ObjectKey: hls, Cid: "bafyother"}); n != 0 {
		t.Fatal("re-armed against a CID the caller did not inspect")
	}
	if n, err := q.RearmIPFSPinForRepin(ctx, sqlcgen.RearmIPFSPinForRepinParams{ObjectKey: hls, Cid: "bafyold"}); err != nil || n != 1 {
		t.Fatalf("re-arm = %d err=%v", n, err)
	}
	got, err := q.GetIPFSPinByObjectKey(ctx, hls)
	if err != nil || got.State != "pending" || got.Cid != "bafyold" || got.CarRoot != "bafyold" || got.PolicyReason != "backfill" {
		t.Fatalf("row after re-arm = %+v err=%v", got, err)
	}
}
