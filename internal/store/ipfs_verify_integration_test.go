//go:build integration

// Integration coverage for the ledger<->node reconciliation's SQL half and for
// the moderation-block branch of the eligibility sweep (A31 follow-ups). The
// fake-repo unit tests in internal/ipfsmirror cover the sweep and comparison
// WIRING; this exercises the real predicates — the keyset page, the DISTINCT live
// CID set, the guarded re-arm, and the video_blocks semi-join the block ruling
// added — which a fake cannot. Self-skips without DATABASE_URL.
package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// verifyFixture seeds one owner, channel and public+published video, plus the
// helpers each test needs, and registers its own cleanup.
type verifyFixture struct {
	st      *Store
	q       *sqlcgen.Queries
	ctx     context.Context
	owner   uuid.UUID
	videoID uuid.UUID
	keys    []string
}

func newVerifyFixture(t *testing.T) *verifyFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)

	f := &verifyFixture{st: st, q: st.Queries(), ctx: ctx}
	suffix := uuid.NewString()[:8]
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"verify-"+suffix, "verify-"+suffix+"@example.test",
	).Scan(&f.owner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM media_ipfs_pins WHERE object_key = ANY($1)`, f.keys)
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, f.owner)
	})

	var chID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Verify') RETURNING id`,
		f.owner, "verify_"+suffix,
	).Scan(&chID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, description, privacy, state, category)
		 VALUES ($1, 'Verify Probe', '', 'public', 'published', 'science') RETURNING id`,
		chID,
	).Scan(&f.videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	return f
}

func (f *verifyFixture) seedPin(t *testing.T, key, class, cid, state, network string) {
	t.Helper()
	f.keys = append(f.keys, key)
	if _, err := f.st.Pool.Exec(f.ctx,
		`INSERT INTO media_ipfs_pins (object_key, media_class, cid, state, network, video_id)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		key, class, cid, state, network, f.videoID,
	); err != nil {
		t.Fatalf("seed pin %s: %v", key, err)
	}
}

func (f *verifyFixture) state(t *testing.T, key string) string {
	t.Helper()
	var state string
	if err := f.st.Pool.QueryRow(f.ctx, `SELECT state FROM media_ipfs_pins WHERE object_key = $1`, key).Scan(&state); err != nil {
		t.Fatalf("read state %s: %v", key, err)
	}
	return state
}

// Two CIDv1s. Fake fixtures — no bytes behind either, anywhere.
const (
	verifyCIDA = "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"
	verifyCIDB = "bafybeibml5jsepzcgep3iowsxlcz5oc5fzfjx3ozc7hbgobht46hpqxvym"
)

// The verify page is keyset-ordered, scoped to one swarm, and returns only rows
// this instance claims are pinned with a real CID.
func TestListIPFSPinsForVerifyPagesByObjectKey(t *testing.T) {
	f := newVerifyFixture(t)
	prefix := "verify/" + f.videoID.String() + "/"
	f.seedPin(t, prefix+"a.jpg", "thumbnail", verifyCIDA, "pinned", "public")
	f.seedPin(t, prefix+"b.jpg", "thumbnail", verifyCIDB, "pinned", "public")
	// Excluded three ways: wrong swarm, not pinned, and pinned with no CID.
	f.seedPin(t, prefix+"c.jpg", "thumbnail", verifyCIDA, "pinned", "private")
	f.seedPin(t, prefix+"d.jpg", "thumbnail", verifyCIDA, "pending", "public")
	f.seedPin(t, prefix+"e.jpg", "thumbnail", "", "pinned", "public")

	page, err := f.q.ListIPFSPinsForVerify(f.ctx, sqlcgen.ListIPFSPinsForVerifyParams{
		Network: "public", AfterObjectKey: prefix, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("ListIPFSPinsForVerify: %v", err)
	}
	var got []string
	for _, r := range page {
		got = append(got, r.ObjectKey)
	}
	if len(got) != 2 || got[0] != prefix+"a.jpg" || got[1] != prefix+"b.jpg" {
		t.Fatalf("page = %v, want exactly the two pinned public rows in key order", got)
	}

	// The cursor advances past what it already returned.
	next, err := f.q.ListIPFSPinsForVerify(f.ctx, sqlcgen.ListIPFSPinsForVerifyParams{
		Network: "public", AfterObjectKey: prefix + "a.jpg", BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("ListIPFSPinsForVerify (cursor): %v", err)
	}
	for _, r := range next {
		if r.ObjectKey == prefix+"a.jpg" {
			t.Error("the keyset cursor returned a row it had already yielded")
		}
	}
}

// The live-CID set is DISTINCT and deliberately wider than the verify page: a
// CID a pending or unpinning row still claims must not be reported as a stray.
func TestListLiveIPFSPinCIDsCoversInFlightRows(t *testing.T) {
	f := newVerifyFixture(t)
	prefix := "verifycids/" + f.videoID.String() + "/"
	f.seedPin(t, prefix+"a.jpg", "thumbnail", verifyCIDA, "pinned", "public")
	f.seedPin(t, prefix+"b.jpg", "thumbnail", verifyCIDA, "pending", "public") // same CID, deduped
	f.seedPin(t, prefix+"c.jpg", "thumbnail", verifyCIDB, "unpinning", "public")
	f.seedPin(t, prefix+"d.jpg", "thumbnail", verifyCIDB, "unpinned", "public") // terminal: not a claim

	cids, err := f.q.ListLiveIPFSPinCIDs(f.ctx, sqlcgen.ListLiveIPFSPinCIDsParams{Network: "public", MaxRows: 1000})
	if err != nil {
		t.Fatalf("ListLiveIPFSPinCIDs: %v", err)
	}
	seen := map[string]int{}
	for _, cid := range cids {
		seen[cid]++
	}
	if seen[verifyCIDA] != 1 {
		t.Errorf("CID A appears %d times, want exactly 1 (DISTINCT)", seen[verifyCIDA])
	}
	if seen[verifyCIDB] != 1 {
		t.Errorf("CID B (claimed only by an unpinning row) appears %d times, want 1", seen[verifyCIDB])
	}
}

// The re-arm is guarded on BOTH the state and the CID the sweep verified: a row
// a better-informed writer moved in between matches nothing.
func TestRearmLostIPFSPinIsGuarded(t *testing.T) {
	f := newVerifyFixture(t)
	key := "verifyrearm/" + f.videoID.String() + ".jpg"
	f.seedPin(t, key, "thumbnail", verifyCIDA, "pinned", "public")

	n, err := f.q.RearmLostIPFSPin(f.ctx, sqlcgen.RearmLostIPFSPinParams{ObjectKey: key, Cid: verifyCIDB})
	if err != nil {
		t.Fatalf("RearmLostIPFSPin (wrong cid): %v", err)
	}
	if n != 0 || f.state(t, key) != "pinned" {
		t.Fatalf("a CID mismatch re-armed %d rows; state=%s", n, f.state(t, key))
	}

	if n, err = f.q.RearmLostIPFSPin(f.ctx, sqlcgen.RearmLostIPFSPinParams{ObjectKey: key, Cid: verifyCIDA}); err != nil {
		t.Fatalf("RearmLostIPFSPin: %v", err)
	}
	if n != 1 || f.state(t, key) != "pending" {
		t.Fatalf("re-armed %d rows, state=%s; want 1 and pending", n, f.state(t, key))
	}

	// Now the row is 'pending', so the same statement must be a no-op: the sweep
	// must never fight the worker that already claimed it.
	if n, err = f.q.RearmLostIPFSPin(f.ctx, sqlcgen.RearmLostIPFSPinParams{ObjectKey: key, Cid: verifyCIDA}); err != nil {
		t.Fatalf("RearmLostIPFSPin (already pending): %v", err)
	}
	if n != 0 {
		t.Errorf("re-armed an already-pending row (%d)", n)
	}
}

// THE BLOCK RULING, in the real join. The video stays public+published and its
// owner stays listed — the block is a video_blocks row and nothing else — and the
// sweep must still flip its rows toward removal on BOTH swarms.
func TestSweepIneligibleIPFSPinsUnpinsABlockedVideo(t *testing.T) {
	f := newVerifyFixture(t)
	pubKey := "verifyblock/" + f.videoID.String() + ".mp4"
	privKey := "verifyblockpriv/" + f.videoID.String() + ".mp4"
	f.seedPin(t, pubKey, "video_original", verifyCIDA, "pinned", "public")
	f.seedPin(t, privKey, "video_original", verifyCIDB, "pinned", "private")

	// Not blocked yet: a public+published video with a listed owner is eligible on
	// the public swarm, and a published video is eligible on the private one.
	if _, err := f.q.SweepIneligibleIPFSPins(f.ctx, 500); err != nil {
		t.Fatalf("SweepIneligibleIPFSPins (before): %v", err)
	}
	if got := f.state(t, pubKey); got != "pinned" {
		t.Fatalf("an eligible public row was swept to %q before any block", got)
	}

	if _, err := f.st.Pool.Exec(f.ctx,
		`INSERT INTO video_blocks (video_id, reason) VALUES ($1, 'tos')`, f.videoID,
	); err != nil {
		t.Fatalf("seed block: %v", err)
	}

	// Privacy and state are untouched — that is the whole reason the fence needed
	// a branch of its own.
	var privacy, state string
	if err := f.st.Pool.QueryRow(f.ctx, `SELECT privacy, state FROM videos WHERE id = $1`, f.videoID).Scan(&privacy, &state); err != nil {
		t.Fatalf("read video: %v", err)
	}
	if privacy != "public" || state != "published" {
		t.Fatalf("the block changed the video row (%s/%s); this test no longer proves anything", privacy, state)
	}

	if _, err := f.q.SweepIneligibleIPFSPins(f.ctx, 500); err != nil {
		t.Fatalf("SweepIneligibleIPFSPins (after): %v", err)
	}
	if got := f.state(t, pubKey); got != "unpinning" {
		t.Errorf("the blocked video's PUBLIC row is %q, want unpinning", got)
	}
	if got := f.state(t, privKey); got != "unpinning" {
		t.Errorf("the blocked video's PRIVATE row is %q, want unpinning: a block is a removal, not a visibility change", got)
	}
}

// The ledger-health facts the `ipfs` component states behind its verdict, per
// swarm, on an empty-for-that-swarm ledger too (the NULL-safety the age columns
// were reshaped for).
func TestIPFSPinLedgerHealthCountsPerNetwork(t *testing.T) {
	f := newVerifyFixture(t)
	prefix := "verifyhealth/" + f.videoID.String() + "/"
	f.seedPin(t, prefix+"a.jpg", "thumbnail", verifyCIDA, "pinned", "public")
	f.seedPin(t, prefix+"b.jpg", "thumbnail", "", "pending", "public")
	f.seedPin(t, prefix+"c.jpg", "thumbnail", "", "failed", "public")

	pub, err := f.q.IPFSPinLedgerHealth(f.ctx, "public")
	if err != nil {
		t.Fatalf("IPFSPinLedgerHealth(public): %v", err)
	}
	if pub.Pinned < 1 || pub.Backlog < 1 || pub.DeadLettered < 1 {
		t.Errorf("public health = %+v, want at least one of each seeded state", pub)
	}

	// A swarm with no rows at all must still answer, and must not scan a NULL into
	// a non-nullable column.
	if _, err := f.q.IPFSPinLedgerHealth(f.ctx, "nosuchswarm"); err != nil {
		t.Fatalf("IPFSPinLedgerHealth on an empty swarm: %v", err)
	}
	if _, err := f.q.IPFSPinJobStats(f.ctx); err != nil {
		t.Fatalf("IPFSPinJobStats: %v", err)
	}
}
