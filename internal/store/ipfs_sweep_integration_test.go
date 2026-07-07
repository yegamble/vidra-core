//go:build integration

// Integration coverage for SweepIneligibleIPFSPins — the periodic eligibility
// backstop's real SQL join against videos/channels/users (fix_plan P19.10). The
// fake-repo unit test in internal/ipfsmirror covers the sweep→unpin WIRING; this
// exercises the actual join logic (which the fake cannot), so a mistake in the WHERE
// predicates or the joins is caught when a database is available. Self-skips without
// DATABASE_URL, matching the other integration tests.
package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSweepIneligibleIPFSPinsJoin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	var owner uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"sweep-"+suffix, "sweep-"+suffix+"@example.test",
	).Scan(&owner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, owner) }()

	var chID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Sweep') RETURNING id`,
		owner, "sweep_"+suffix,
	).Scan(&chID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	var vidID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, description, privacy, state, category)
		 VALUES ($1, 'Sweep Probe', '', 'public', 'published', 'science') RETURNING id`,
		chID,
	).Scan(&vidID); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	const cid = "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"
	vidKey := "web-videos/" + vidID.String() + ".mp4"
	imgKey := "avatars/users/" + owner.String() + ".png"
	coverKey := "playlist-thumbnails/" + uuid.NewString() + ".jpg"

	seedPin := func(key, class string, videoID, ownerID *uuid.UUID) {
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO media_ipfs_pins (object_key, media_class, cid, state, video_id, owner_user_id)
			 VALUES ($1, $2, $3, 'pinned', $4, $5)`,
			key, class, cid, videoID, ownerID,
		); err != nil {
			t.Fatalf("seed pin %s: %v", key, err)
		}
	}
	defer func() {
		_, _ = st.Pool.Exec(context.Background(),
			`DELETE FROM media_ipfs_pins WHERE object_key = ANY($1)`, []string{vidKey, imgKey, coverKey})
	}()
	seedPin(vidKey, "video_original", &vidID, nil) // video-derived
	seedPin(imgKey, "user_avatar", nil, &owner)    // identity image
	seedPin(coverKey, "playlist_cover", nil, nil)  // no provenance ⇒ out of sweep scope

	state := func(key string) string {
		var s string
		if err := st.Pool.QueryRow(ctx, `SELECT state FROM media_ipfs_pins WHERE object_key = $1`, key).Scan(&s); err != nil {
			t.Fatalf("read state %s: %v", key, err)
		}
		return s
	}
	repin := func() {
		if _, err := st.Pool.Exec(ctx,
			`UPDATE media_ipfs_pins SET state = 'pinned' WHERE object_key = ANY($1)`,
			[]string{vidKey, imgKey, coverKey}); err != nil {
			t.Fatalf("repin: %v", err)
		}
	}

	sweep := func() int64 {
		n, err := q.SweepIneligibleIPFSPins(ctx, 100)
		if err != nil {
			t.Fatalf("SweepIneligibleIPFSPins: %v", err)
		}
		return n
	}

	// Case 1: everything eligible (public+published video, listed+active owner) ⇒ the
	// sweep must touch NOTHING.
	if n := sweep(); n != 0 {
		t.Fatalf("eligible sweep re-armed %d rows, want 0", n)
	}
	for _, key := range []string{vidKey, imgKey, coverKey} {
		if got := state(key); got != "pinned" {
			t.Errorf("case 1: %s state = %q, want pinned", key, got)
		}
	}

	// Case 2: owner goes unlisted ⇒ BOTH the video-derived row and the identity image
	// flip to 'unpinning'; the provenance-less playlist cover is untouched.
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET unlisted = true WHERE id = $1`, owner); err != nil {
		t.Fatalf("set unlisted: %v", err)
	}
	if n := sweep(); n != 2 {
		t.Fatalf("unlisted sweep re-armed %d rows, want 2", n)
	}
	if got := state(vidKey); got != "unpinning" {
		t.Errorf("case 2: video row state = %q, want unpinning", got)
	}
	if got := state(imgKey); got != "unpinning" {
		t.Errorf("case 2: image row state = %q, want unpinning", got)
	}
	if got := state(coverKey); got != "pinned" {
		t.Errorf("case 2: playlist cover state = %q, want pinned (out of sweep scope)", got)
	}

	// Case 3: owner re-listed but the VIDEO goes private ⇒ only the video-derived row
	// flips; the identity image stays pinned.
	repin()
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET unlisted = false WHERE id = $1`, owner); err != nil {
		t.Fatalf("clear unlisted: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE videos SET privacy = 'private' WHERE id = $1`, vidID); err != nil {
		t.Fatalf("set private: %v", err)
	}
	if n := sweep(); n != 1 {
		t.Fatalf("private-video sweep re-armed %d rows, want 1", n)
	}
	if got := state(vidKey); got != "unpinning" {
		t.Errorf("case 3: video row state = %q, want unpinning", got)
	}
	if got := state(imgKey); got != "pinned" {
		t.Errorf("case 3: image row state = %q, want pinned (owner listed+active)", got)
	}

	// A second sweep with the states already 'unpinning' is a no-op (idempotent).
	if n := sweep(); n != 0 {
		t.Errorf("idempotent sweep re-armed %d rows, want 0 (already unpinning)", n)
	}
}
