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

// The data-loss path: a re-armed row whose re-add dead-letters must fall back to its
// still-valid old root, and Reconcile's re-arm of failed rows must leave it pinned
// (it used to read cid <> ” as a failed UNPIN and flip the row to 'unpinning').
func TestRepinDeadLetterKeepsTheOldRoot(t *testing.T) {
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
	key := "repintest/" + uuid.NewString() + "/"
	if _, err := tx.Exec(ctx, "INSERT INTO media_ipfs_pins(object_key,media_class,state,cid,car_root) VALUES($1,'hls','pinned','bafyold','bafyold')", key); err != nil {
		t.Fatal(err)
	}
	if n, err := q.RearmIPFSPinForRepin(ctx, sqlcgen.RearmIPFSPinForRepinParams{ObjectKey: key, Cid: "bafyold"}); err != nil || n != 1 {
		t.Fatalf("re-arm = %d %v", n, err)
	}
	if err := q.MarkIPFSPinFailed(ctx, sqlcgen.MarkIPFSPinFailedParams{ObjectKey: key, LastError: "add failed", ExpectedState: "pending"}); err != nil {
		t.Fatal(err)
	}
	// The reconcile re-arm of dead letters, over every failed row in the table.
	if _, err := q.RearmFailedIPFSPins(ctx, 1000); err != nil {
		t.Fatal(err)
	}
	got, err := q.GetIPFSPinByObjectKey(ctx, key)
	if err != nil || got.State != "pinned" || got.Cid != "bafyold" || got.CarRoot != "bafyold" || got.LastError != "add failed" {
		t.Fatalf("row = %+v err=%v, want pinned on the old root with last_error kept", got, err)
	}
	// A first pin that never had a root still dead-letters.
	fresh := "repintest/" + uuid.NewString() + "/"
	if _, err := tx.Exec(ctx, "INSERT INTO media_ipfs_pins(object_key,media_class,state) VALUES($1,'hls','pending')", fresh); err != nil {
		t.Fatal(err)
	}
	_ = q.MarkIPFSPinFailed(ctx, sqlcgen.MarkIPFSPinFailedParams{ObjectKey: fresh, LastError: "x", ExpectedState: "pending"})
	if g, _ := q.GetIPFSPinByObjectKey(ctx, fresh); g.State != "failed" {
		t.Fatalf("a rootless first pin = %q, want failed", g.State)
	}
}

// StartIPFSRepinRun is a real compare-and-set: refused while running or paused.
func TestStartIPFSRepinRunIsRefusedWhileRunning(t *testing.T) {
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
	k := "repintest.state." + uuid.NewString()
	start := func(phase string) int64 {
		n, err := q.StartIPFSRepinRun(ctx, sqlcgen.StartIPFSRepinRunParams{Key: k, Value: `{"phase":"` + phase + `"}`})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if start("running") != 1 || start("running") != 0 {
		t.Fatal("second start not refused while running")
	}
	if n, _ := q.SaveIPFSRepinRun(ctx, sqlcgen.SaveIPFSRepinRunParams{Key: k, Value: `{"phase":"paused"}`, ExpectedPhase: "running"}); n != 1 {
		t.Fatal("save from the loaded phase failed")
	}
	if start("running") != 0 {
		t.Fatal("start allowed while paused")
	}
	if n, _ := q.SaveIPFSRepinRun(ctx, sqlcgen.SaveIPFSRepinRunParams{Key: k, Value: `{"phase":"completed"}`, ExpectedPhase: "running"}); n != 0 {
		t.Fatal("stale save succeeded")
	}
	q.SaveIPFSRepinRun(ctx, sqlcgen.SaveIPFSRepinRunParams{Key: k, Value: `{"phase":"cancelled"}`, ExpectedPhase: "paused"})
	if start("running") != 1 {
		t.Fatal("start refused after cancel")
	}
}
