//go:build integration

package store

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// seedMovePlaylist creates a user, channel, n public+published videos and one
// playlist holding them, positioned by pos(i). The users→… ON DELETE CASCADE
// cleans everything up. It returns the playlist id and the videos in the order
// they were inserted.
func seedMovePlaylist(t *testing.T, st *Store, n int, pos func(i int) int) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	var userID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"move-"+suffix, "move-"+suffix+"@example.test",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) })
	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Move') RETURNING id`,
		userID, "move_"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	var playlistID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO playlists (owner_id, title) VALUES ($1, 'Long') RETURNING id`, userID,
	).Scan(&playlistID); err != nil {
		t.Fatalf("seed playlist: %v", err)
	}
	// One fixed added_at so ties on position fall through to the video_id
	// tiebreak, not to insertion time.
	addedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	vids := make([]uuid.UUID, n)
	for i := range vids {
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, $2, 'public', 'published') RETURNING id`,
			channelID, fmt.Sprintf("v%02d", i),
		).Scan(&vids[i]); err != nil {
			t.Fatalf("seed video %d: %v", i, err)
		}
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO playlist_items (playlist_id, video_id, position, added_at) VALUES ($1, $2, $3, $4)`,
			playlistID, vids[i], pos(i), addedAt,
		); err != nil {
			t.Fatalf("seed item %d: %v", i, err)
		}
	}
	return playlistID, vids
}

// itemPositions reads the playlist's stored order and fails unless positions are
// exactly 1..n — no gap, no duplicate.
func itemPositions(t *testing.T, st *Store, playlistID uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := st.Pool.Query(context.Background(),
		`SELECT video_id, position FROM playlist_items WHERE playlist_id = $1 ORDER BY position, video_id`, playlistID)
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	defer rows.Close()
	var order []uuid.UUID
	for rows.Next() {
		var vid uuid.UUID
		var pos int32
		if err := rows.Scan(&vid, &pos); err != nil {
			t.Fatalf("scan: %v", err)
		}
		order = append(order, vid)
		if int(pos) != len(order) {
			t.Fatalf("item %d has position %d: positions must be exactly 1..n", len(order), pos)
		}
	}
	return order
}

func movePtr(id uuid.UUID) *uuid.UUID { return &id }

// TestMovePlaylistItem proves the anchor-based move against real PostgreSQL: an
// item from page 2 moves to the front, after another item, and to the end; the
// statement compacts gapped/duplicated positions to 1..n as it goes; and an
// unknown video or anchor changes nothing and reports 0 rows.
func TestMovePlaylistItem(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	// 25 items (more than one 20-item page) with gaps (×3) and one duplicated
	// position — the shapes RemovePlaylistItem and racing appends leave behind.
	playlistID, vids := seedMovePlaylist(t, st, 25, func(i int) int {
		if i == 6 {
			return 15 // shares position 15 with item 5
		}
		return (i + 1) * 3
	})
	want, err := q.ListPlaylistItemVideoIDs(ctx, playlistID)
	if err != nil || len(want) != 25 {
		t.Fatalf("initial order = %d ids, %v", len(want), err)
	}

	move := func(video uuid.UUID, after *uuid.UUID) int64 {
		t.Helper()
		n, err := q.MovePlaylistItem(ctx, sqlcgen.MovePlaylistItemParams{
			PlaylistID: playlistID, VideoID: video, AfterVideoID: pgconv.UUIDPtr(after),
		})
		if err != nil {
			t.Fatalf("MovePlaylistItem: %v", err)
		}
		return n
	}
	// expect applies the same move to the model order.
	expect := func(video uuid.UUID, after *uuid.UUID) {
		out := make([]uuid.UUID, 0, len(want))
		if after == nil {
			out = append(out, video)
		}
		for _, v := range want {
			if v == video {
				continue
			}
			out = append(out, v)
			if after != nil && v == *after {
				out = append(out, video)
			}
		}
		want = out
	}
	check := func(step string) {
		t.Helper()
		got := itemPositions(t, st, playlistID)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s: order = %v, want %v", step, got, want)
		}
	}

	// Page 2's 23rd item to the very front.
	if n := move(want[22], nil); n != 25 {
		t.Fatalf("move to front affected %d rows, want 25", n)
	}
	expect(want[22], nil)
	check("to front")

	// Page 2's 21st item to directly after the 2nd (an upward move).
	mv, anchor := want[20], want[1]
	move(mv, &anchor)
	expect(mv, &anchor)
	check("after anchor (up)")

	// The front item to directly after the 10th (a downward move).
	mv, anchor = want[0], want[9]
	move(mv, &anchor)
	expect(mv, &anchor)
	check("after anchor (down)")

	// An item to after the last one.
	mv, anchor = want[3], want[24]
	move(mv, &anchor)
	expect(mv, &anchor)
	check("to end")

	// Unknown video, unknown anchor, or a video from another playlist: no-op.
	before := append([]uuid.UUID(nil), want...)
	if n := move(uuid.New(), nil); n != 0 {
		t.Errorf("unknown video affected %d rows, want 0", n)
	}
	if n := move(want[5], movePtr(uuid.New())); n != 0 {
		t.Errorf("unknown anchor affected %d rows, want 0", n)
	}
	want = before
	check("after rejected moves")
	_ = vids
}

// TestMovePlaylistItemConcurrent proves concurrent moves serialize instead of
// renumbering from a stale snapshot. Each worker moves a DIFFERENT item to the
// front at once: in any serial order those items end up as the first K, and
// every other item keeps its relative order. A lost update would leave one of
// them where it was; an unserialized renumber would duplicate a position. A
// second round of random moves checks density and that nothing deadlocks.
func TestMovePlaylistItemConcurrent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t), WithMaxConns(12))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	const n, workers = 30, 8
	playlistID, vids := seedMovePlaylist(t, st, n, func(i int) int { return i + 1 })

	// Round 1: workers move items 10..17 to the front simultaneously.
	moved := map[uuid.UUID]bool{}
	var wg sync.WaitGroup
	errs := make(chan error, workers*20)
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		vid := vids[10+w]
		moved[vid] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := q.MovePlaylistItem(ctx, sqlcgen.MovePlaylistItemParams{
				PlaylistID: playlistID, VideoID: vid,
			}); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatalf("concurrent move: %v", err)
	default:
	}
	got := itemPositions(t, st, playlistID)
	if len(got) != n {
		t.Fatalf("items = %d, want %d", len(got), n)
	}
	for i := 0; i < workers; i++ {
		if !moved[got[i]] {
			t.Fatalf("position %d holds an unmoved item: a concurrent move was lost (order %v)", i+1, got)
		}
	}
	var rest []uuid.UUID
	for _, v := range vids {
		if !moved[v] {
			rest = append(rest, v)
		}
	}
	if fmt.Sprint(got[workers:]) != fmt.Sprint(rest) {
		t.Fatalf("unmoved items lost their relative order: %v, want %v", got[workers:], rest)
	}

	// Round 2: random moves from every worker at once.
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for k := 0; k < 15; k++ {
				vid := vids[r.Intn(n)]
				var after *uuid.UUID
				if a := vids[r.Intn(n)]; a != vid && r.Intn(4) > 0 {
					after = &a
				}
				if _, err := q.MovePlaylistItem(ctx, sqlcgen.MovePlaylistItemParams{
					PlaylistID: playlistID, VideoID: vid, AfterVideoID: pgconv.UUIDPtr(after),
				}); err != nil {
					errs <- err
					return
				}
			}
		}(int64(w))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("random concurrent move: %v", err)
	}
	if got := itemPositions(t, st, playlistID); len(got) != n {
		t.Fatalf("items after random moves = %d, want %d", len(got), n)
	}
}

// TestReorderAndMoveDoNotDeadlock races the full-list PUT reorder against the
// single-item move on one playlist. The move locks every item row in video_id
// order; if the reorder locked in any other order (its plan's, or the client's
// unnest order) the two could each hold a row the other needs, and PostgreSQL
// would abort one with 40P01 — a 500 to whichever user lost. The reorder is fed
// DESCENDING video_id order, the worst case against the move's ascending order.
func TestReorderAndMoveDoNotDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t), WithMaxConns(12))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	const n, rounds, perSide = 60, 6, 4
	playlistID, vids := seedMovePlaylist(t, st, n, func(i int) int { return i + 1 })
	desc := append([]uuid.UUID(nil), vids...)
	sort.Slice(desc, func(i, j int) bool { return bytes.Compare(desc[i][:], desc[j][:]) > 0 })

	var mu sync.Mutex
	var failures []error
	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for w := 0; w < perSide; w++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				if err := q.ReorderPlaylistItems(ctx, sqlcgen.ReorderPlaylistItemsParams{
					PlaylistID: playlistID, VideoIds: desc,
				}); err != nil {
					mu.Lock()
					failures = append(failures, fmt.Errorf("reorder: %w", err))
					mu.Unlock()
				}
			}()
			go func(w int) {
				defer wg.Done()
				<-start
				if _, err := q.MovePlaylistItem(ctx, sqlcgen.MovePlaylistItemParams{
					PlaylistID: playlistID, VideoID: vids[w],
				}); err != nil {
					mu.Lock()
					failures = append(failures, fmt.Errorf("move: %w", err))
					mu.Unlock()
				}
			}(round*perSide + w)
		}
		close(start)
		wg.Wait()
	}
	deadlocks := 0
	for _, err := range failures {
		if pgconv.SQLState(err) == "40P01" {
			deadlocks++
		}
	}
	if len(failures) > 0 {
		t.Fatalf("%d of %d racing statements failed (%d deadlocks); first: %v",
			len(failures), rounds*perSide*2, deadlocks, failures[0])
	}
	if got := itemPositions(t, st, playlistID); len(got) != n {
		t.Fatalf("items = %d, want %d", len(got), n)
	}
}

// TestListPlaylistItemsPagesDoNotOverlap proves the item listing is a total
// order: items tied on position AND added_at page through LIMIT/OFFSET with no
// item shown twice and none skipped.
func TestListPlaylistItemsPagesDoNotOverlap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	const n = 23
	playlistID, _ := seedMovePlaylist(t, st, n, func(int) int { return 1 })
	seen := map[uuid.UUID]bool{}
	for offset := int32(0); offset < n; offset += 4 {
		rows, err := q.ListPlaylistItems(ctx, sqlcgen.ListPlaylistItemsParams{
			PlaylistID: playlistID, ResultLimit: 4, ResultOffset: offset,
		})
		if err != nil {
			t.Fatalf("ListPlaylistItems: %v", err)
		}
		for _, r := range rows {
			if seen[r.ID] {
				t.Fatalf("video %s appeared on two pages", r.ID)
			}
			seen[r.ID] = true
		}
	}
	if len(seen) != n {
		t.Fatalf("paged %d distinct items, want %d", len(seen), n)
	}
}
