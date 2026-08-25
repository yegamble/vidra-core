//go:build integration

// Integration coverage for ListVideosNeedingStoryboard — the storyboard
// backfill's real eligibility SQL (migration 0117). The fake-repo unit tests in
// internal/storyboardbackfill cover the retry/give-up DECISIONS; this exercises
// the join and the WHERE predicates, which the fake models but cannot prove.
// Every term of the predicate gets a video that differs by that term alone.
// Self-skips without DATABASE_URL, matching the other integration tests.
package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestListVideosNeedingStoryboardEligibility(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		"sbbf-"+suffix, "sbbf-"+suffix+"@example.test",
	).Scan(&owner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// Everything below cascades from the user.
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, owner) }()

	var chID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'SB') RETURNING id`,
		owner, "sbbf_"+suffix,
	).Scan(&chID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	seedVideo := func(title, state string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, $2, 'public', $3) RETURNING id`,
			chID, title, state,
		).Scan(&id); err != nil {
			t.Fatalf("seed video %q: %v", title, err)
		}
		return id
	}
	seedFile := func(videoID uuid.UUID, kind, key string) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO video_files (video_id, kind, storage_key, size_bytes) VALUES ($1, $2, $3, 1)`,
			videoID, kind, key,
		); err != nil {
			t.Fatalf("seed %s file: %v", kind, err)
		}
	}

	// The one video that should come back, plus its recorded duration.
	wanted := seedVideo("needs one", "published")
	seedFile(wanted, "original", "web-videos/"+wanted.String()+".mp4")
	if _, err := q.UpsertVideoMetadata(ctx, sqlcgen.UpsertVideoMetadataParams{
		VideoID: wanted, DurationSeconds: ptrInt32(613),
	}); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}

	// Ineligible, one reason each.
	hasSheet := seedVideo("already has one", "published")
	seedFile(hasSheet, "original", "web-videos/"+hasSheet.String()+".mp4")
	seedFile(hasSheet, "storyboard", "storyboards/"+hasSheet.String()+".jpg")

	noOriginal := seedVideo("hls only", "published")
	seedFile(noOriginal, "rendition", "streaming-playlists/"+noOriginal.String()+"/720.m3u8")

	draft := seedVideo("not published", "draft")
	seedFile(draft, "original", "web-videos/"+draft.String()+".mp4")

	gaveUp := seedVideo("given up on", "published")
	seedFile(gaveUp, "original", "web-videos/"+gaveUp.String()+".mp4")
	if err := q.GiveUpOnStoryboard(ctx, sqlcgen.GiveUpOnStoryboardParams{
		VideoID: gaveUp, LastError: "no measurable duration",
	}); err != nil {
		t.Fatalf("give up: %v", err)
	}

	backedOff := seedVideo("waiting out a backoff", "published")
	seedFile(backedOff, "original", "web-videos/"+backedOff.String()+".mp4")
	if _, err := q.RecordStoryboardAttemptFailure(ctx, sqlcgen.RecordStoryboardAttemptFailureParams{
		VideoID:       backedOff,
		NextAttemptAt: time.Now().Add(6 * time.Hour),
		LastError:     "object store unavailable",
		MaxAttempts:   5,
	}); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	// A big limit: this query is global, so scope the assertions to this
	// fixture's ids rather than to the row count.
	rows, err := q.ListVideosNeedingStoryboard(ctx, 500)
	if err != nil {
		t.Fatalf("ListVideosNeedingStoryboard: %v", err)
	}
	got := map[uuid.UUID]sqlcgen.ListVideosNeedingStoryboardRow{}
	for _, r := range rows {
		got[r.ID] = r
	}
	for _, tc := range []struct {
		id   uuid.UUID
		want bool
		why  string
	}{
		{wanted, true, "published, has an original, no sheet, no ledger row"},
		{hasSheet, false, "already carries a kind='storyboard' file"},
		{noOriginal, false, "has no kind='original' row to decode"},
		{draft, false, "is not published"},
		{gaveUp, false, "has been permanently given up on"},
		{backedOff, false, "is waiting out its retry backoff"},
	} {
		if _, in := got[tc.id]; in != tc.want {
			t.Errorf("video %q selected = %v, want %v (%s)", tc.id, in, tc.want, tc.why)
		}
	}
	if r := got[wanted]; r.DurationSeconds != 613 || r.StorageKey == "" || r.Attempts != 0 {
		t.Errorf("selected row = %+v, want the recorded duration, the original's key and zero attempts", r)
	}

	// The backoff having elapsed makes it a candidate again — the retry actually
	// retries.
	if _, err := st.Pool.Exec(ctx,
		`UPDATE video_storyboard_attempts SET next_attempt_at = now() - interval '1 minute' WHERE video_id = $1`,
		backedOff,
	); err != nil {
		t.Fatalf("expire the backoff: %v", err)
	}
	rows, err = q.ListVideosNeedingStoryboard(ctx, 500)
	if err != nil {
		t.Fatalf("ListVideosNeedingStoryboard after the backoff: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ID == backedOff {
			found = true
			if r.Attempts != 1 {
				t.Errorf("attempts = %d for a once-failed video, want 1 (it sizes the next backoff)", r.Attempts)
			}
		}
	}
	if !found {
		t.Error("a video whose backoff has elapsed was not re-selected")
	}
}

// TestStoryboardAttemptLedgerGivesUpAtTheThreshold proves the give-up decision is
// made by the UPSERT itself — the worker never reads-modifies-writes it — and
// that a success clears the row.
func TestStoryboardAttemptLedgerGivesUpAtTheThreshold(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		"sbld-"+suffix, "sbld-"+suffix+"@example.test",
	).Scan(&owner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, owner) }()

	var chID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'SB') RETURNING id`,
		owner, "sbld_"+suffix,
	).Scan(&chID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	var vidID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'ledger', 'public', 'published') RETURNING id`,
		chID,
	).Scan(&vidID); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	const maxAttempts = 3
	for i := 1; i <= maxAttempts; i++ {
		out, err := q.RecordStoryboardAttemptFailure(ctx, sqlcgen.RecordStoryboardAttemptFailureParams{
			VideoID:       vidID,
			NextAttemptAt: time.Now().Add(time.Hour),
			LastError:     "boom",
			MaxAttempts:   maxAttempts,
		})
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if out.Attempts != int32(i) {
			t.Errorf("attempt %d recorded attempts = %d", i, out.Attempts)
		}
		if want := i >= maxAttempts; out.GivenUp != want {
			t.Errorf("attempt %d: given_up = %v, want %v", i, out.GivenUp, want)
		}
	}

	n, err := q.CountAbandonedStoryboards(ctx)
	if err != nil {
		t.Fatalf("CountAbandonedStoryboards: %v", err)
	}
	if n < 1 {
		t.Errorf("abandoned count = %d, want at least the one just booked", n)
	}

	if err := q.ClearStoryboardAttempt(ctx, vidID); err != nil {
		t.Fatalf("ClearStoryboardAttempt: %v", err)
	}
	var left int
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM video_storyboard_attempts WHERE video_id = $1`, vidID,
	).Scan(&left); err != nil {
		t.Fatalf("count after clear: %v", err)
	}
	if left != 0 {
		t.Errorf("ledger rows after a success = %d, want 0", left)
	}
}

func ptrInt32(v int32) *int32 { return &v }
