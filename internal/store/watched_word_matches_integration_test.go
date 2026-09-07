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

// TestWatchedWordMatchSnapshotOnRealPG holds the A16 ruling in the only place
// that can hold it: the SQL and the schema.
//
// A16 slice 3 measured the defect — ListWatchedWordMatches read `c.body`, a LIVE
// join, so a flagged comment edited to remove the term kept its match and the
// queue then quoted a body with no term in it. A moderator could not see what
// was flagged and an author could edit the evidence away while the flag stood.
// This asserts the whole snapshot contract end to end on real PostgreSQL:
//
//   - the snapshot is written at flag time and does NOT move when the target is
//     edited (the defect, inverted);
//   - the live excerpt is still reported beside it, and target_status says the
//     two now differ;
//   - a term the edit newly introduces creates its OWN match with its own
//     snapshot, while the term already recorded keeps the original;
//   - deleting the WORD keeps the match (0132 relaxed that FK) with the term
//     readable from the snapshot and term_active false;
//   - deleting the COMMENT still takes the match with it — the recorded gap,
//     asserted so it is a known behaviour rather than a surprise.
func TestWatchedWordMatchSnapshotOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	var authorID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"wwsnap-"+suffix, "wwsnap-"+suffix+"@example.test",
	).Scan(&authorID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, authorID) })

	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'WW Snap') RETURNING id`,
		authorID, "wwsnap-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	var videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'Snap Clip', 'public', 'published') RETURNING id`,
		channelID,
	).Scan(&videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	word, err := q.CreateWatchedWord(ctx, sqlcgen.CreateWatchedWordParams{
		Word: "pineapple-" + suffix, CreatedBy: pgtype.UUID{Bytes: authorID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateWatchedWord: %v", err)
	}
	second, err := q.CreateWatchedWord(ctx, sqlcgen.CreateWatchedWordParams{
		Word: "anchovy-" + suffix, CreatedBy: pgtype.UUID{Bytes: authorID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateWatchedWord second: %v", err)
	}

	original := "I want " + word.Word + " on it"
	var commentID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO comments (video_id, user_id, body) VALUES ($1, $2, $3) RETURNING id`,
		videoID, authorID, original,
	).Scan(&commentID); err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	if err := q.RecordWatchedWordMatch(ctx, sqlcgen.RecordWatchedWordMatchParams{
		WatchedWordID: pgtype.UUID{Bytes: word.ID, Valid: true},
		CommentID:     pgtype.UUID{Bytes: commentID, Valid: true},
		MatchedText:   original,
		MatchedTerm:   word.Word,
		MatchOffset:   7,
		MatchLength:   int32(len([]rune(word.Word))),
	}); err != nil {
		t.Fatalf("RecordWatchedWordMatch: %v", err)
	}

	// listAll returns every match for this fixture's comment, newest first, and
	// asserts the LIST and the COUNT agree — a queue that paged a total it could
	// not serve would promise a page that does not exist.
	listAll := func(status *string) []sqlcgen.ListWatchedWordMatchesRow {
		t.Helper()
		rows, err := q.ListWatchedWordMatches(ctx, sqlcgen.ListWatchedWordMatchesParams{
			Status: status, ResultLimit: 100,
		})
		if err != nil {
			t.Fatalf("ListWatchedWordMatches: %v", err)
		}
		total, err := q.CountWatchedWordMatches(ctx, status)
		if err != nil {
			t.Fatalf("CountWatchedWordMatches: %v", err)
		}
		if int64(len(rows)) != total {
			t.Errorf("list returned %d rows while the count promised %d", len(rows), total)
		}
		mine := make([]sqlcgen.ListWatchedWordMatchesRow, 0, len(rows))
		for _, r := range rows {
			if r.CommentID.Valid && uuid.UUID(r.CommentID.Bytes) == commentID {
				mine = append(mine, r)
			}
		}
		return mine
	}

	got := listAll(nil)
	if len(got) != 1 {
		t.Fatalf("after flagging, matches = %d, want 1", len(got))
	}
	first := got[0]
	if first.MatchedText != original {
		t.Errorf("snapshot = %q, want the flag-time body %q", first.MatchedText, original)
	}
	if first.TargetStatus != "present" {
		t.Errorf("target_status = %q, want present (nothing has been edited yet)", first.TargetStatus)
	}
	if first.SnapshotBackfilled {
		t.Error("a freshly captured snapshot is marked backfilled")
	}
	if !first.TermActive || first.Word != word.Word || first.Status != "open" {
		t.Errorf("row = (term_active %v, word %q, status %q), want (true, %q, open)",
			first.TermActive, first.Word, first.Status, word.Word)
	}

	// THE DEFECT, INVERTED. The author edits the term away and introduces the
	// second one. Re-flagging is what the handler does on an edit.
	edited := "never mind, " + second.Word + " instead"
	if _, err := st.Pool.Exec(ctx, `UPDATE comments SET body = $2 WHERE id = $1`, commentID, edited); err != nil {
		t.Fatalf("edit comment: %v", err)
	}
	// The term already recorded hits ON CONFLICT and keeps its ORIGINAL snapshot.
	if err := q.RecordWatchedWordMatch(ctx, sqlcgen.RecordWatchedWordMatchParams{
		WatchedWordID: pgtype.UUID{Bytes: word.ID, Valid: true},
		CommentID:     pgtype.UUID{Bytes: commentID, Valid: true},
		MatchedText:   edited, MatchedTerm: word.Word, MatchOffset: -1,
	}); err != nil {
		t.Fatalf("re-record existing term: %v", err)
	}
	// The term the edit newly introduced gets its own row and its own snapshot.
	if err := q.RecordWatchedWordMatch(ctx, sqlcgen.RecordWatchedWordMatchParams{
		WatchedWordID: pgtype.UUID{Bytes: second.ID, Valid: true},
		CommentID:     pgtype.UUID{Bytes: commentID, Valid: true},
		MatchedText:   edited, MatchedTerm: second.Word, MatchOffset: 12,
		MatchLength: int32(len([]rune(second.Word))),
	}); err != nil {
		t.Fatalf("record new term: %v", err)
	}

	got = listAll(nil)
	if len(got) != 2 {
		t.Fatalf("after the edit, matches = %d, want 2 (the old flag survives, the new term is added)", len(got))
	}
	byTerm := map[string]sqlcgen.ListWatchedWordMatchesRow{}
	for _, r := range got {
		byTerm[r.Word] = r
	}
	old, ok := byTerm[word.Word]
	if !ok {
		t.Fatalf("the original term's match is gone; got %v", byTerm)
	}
	if old.MatchedText != original {
		t.Errorf("the original snapshot moved to %q; it must stay %q — this is the whole defect", old.MatchedText, original)
	}
	if old.TargetStatus != "edited_away" {
		t.Errorf("target_status = %q, want edited_away (the live body no longer holds the term)", old.TargetStatus)
	}
	if old.CommentBody == nil || *old.CommentBody != edited {
		t.Errorf("live excerpt = %v, want the edited body %q beside the snapshot", old.CommentBody, edited)
	}
	fresh, ok := byTerm[second.Word]
	if !ok {
		t.Fatalf("the newly introduced term raised no match; got %v", byTerm)
	}
	if fresh.MatchedText != edited || fresh.TargetStatus != "present" {
		t.Errorf("new match = (%q, %q), want the edited body and present", fresh.MatchedText, fresh.TargetStatus)
	}

	// Triage: resolve one, dismiss the other, and prove the filter and its total.
	n, err := q.ResolveWatchedWordMatch(ctx, sqlcgen.ResolveWatchedWordMatchParams{
		ID: old.ID, Status: "resolved", ModeratorNote: "hid the comment",
		ResolvedBy: pgtype.UUID{Bytes: authorID, Valid: true},
	})
	if err != nil || n != 1 {
		t.Fatalf("ResolveWatchedWordMatch = (%d, %v), want (1, nil)", n, err)
	}
	// Idempotent, exactly like ResolveReport: a repeat still reports one row.
	if n, err := q.ResolveWatchedWordMatch(ctx, sqlcgen.ResolveWatchedWordMatchParams{
		ID: old.ID, Status: "dismissed", ModeratorNote: "on reflection, fine",
		ResolvedBy: pgtype.UUID{Bytes: authorID, Valid: true},
	}); err != nil || n != 1 {
		t.Fatalf("repeat resolve = (%d, %v), want (1, nil) — triage is idempotent", n, err)
	}
	// An unknown id affects nothing, which is what the service maps to 404.
	if n, err := q.ResolveWatchedWordMatch(ctx, sqlcgen.ResolveWatchedWordMatchParams{
		ID: uuid.New(), Status: "resolved", ResolvedBy: pgtype.UUID{Bytes: authorID, Valid: true},
	}); err != nil || n != 0 {
		t.Fatalf("resolve unknown = (%d, %v), want (0, nil)", n, err)
	}

	open := "open"
	if rows := listAll(&open); len(rows) != 1 || rows[0].ID != fresh.ID {
		t.Errorf("?status=open returns %d rows, want just the untriaged one", len(rows))
	}
	dismissed := "dismissed"
	rows := listAll(&dismissed)
	if len(rows) != 1 || rows[0].ID != old.ID {
		t.Fatalf("?status=dismissed returns %d rows, want the triaged one", len(rows))
	}
	if rows[0].ModeratorNote != "on reflection, fine" {
		t.Errorf("moderator_note = %q, want the second note (a repeat overwrites)", rows[0].ModeratorNote)
	}
	if rows[0].ResolvedByUsername == nil || !rows[0].ResolvedAt.Valid {
		t.Errorf("triaged row carries no resolver/timestamp: %+v", rows[0])
	}

	// Deleting the WORD keeps its review history now. Before 0132 the FK
	// cascaded and a moderator pruning the term list silently discarded it.
	if _, err := q.DeleteWatchedWord(ctx, second.ID); err != nil {
		t.Fatalf("DeleteWatchedWord: %v", err)
	}
	got = listAll(nil)
	if len(got) != 2 {
		t.Fatalf("after deleting the word, matches = %d, want still 2", len(got))
	}
	var orphan *sqlcgen.ListWatchedWordMatchesRow
	for i := range got {
		if got[i].ID == fresh.ID {
			orphan = &got[i]
		}
	}
	if orphan == nil {
		t.Fatalf("the deleted word took its match with it")
	}
	if orphan.TermActive {
		t.Error("term_active is true for a deleted word")
	}
	if orphan.Word != second.Word {
		t.Errorf("word = %q, want %q read back from the snapshot", orphan.Word, second.Word)
	}

	// The recorded GAP, asserted rather than assumed: deleting the COMMENT still
	// cascades the match away. 0132 says why it must (release N-1's queue query
	// projects the target ids as NOT NULL and would fail to scan a widowed row).
	if _, err := st.Pool.Exec(ctx, `DELETE FROM comments WHERE id = $1`, commentID); err != nil {
		t.Fatalf("delete comment: %v", err)
	}
	if got := listAll(nil); len(got) != 0 {
		t.Errorf("after deleting the comment, matches = %d, want 0 (cascade is the recorded gap)", len(got))
	}
}

// TestWatchedWordVideoMatchSnapshotOnRealPG is the video arm of the same
// contract (§12 matches title+description on create and edit): the snapshot is
// the joined text as it read at flag time, and a later retitle leaves it alone.
func TestWatchedWordVideoMatchSnapshotOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	var ownerID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"wwvid-"+suffix, "wwvid-"+suffix+"@example.test",
	).Scan(&ownerID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, ownerID) })

	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'WW Vid') RETURNING id`,
		ownerID, "wwvid-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	word, err := q.CreateWatchedWord(ctx, sqlcgen.CreateWatchedWordParams{
		Word: "mixtape-" + suffix, CreatedBy: pgtype.UUID{Bytes: ownerID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateWatchedWord: %v", err)
	}

	title, desc := "my "+word.Word, "buy it now"
	var videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, description, privacy, state)
		 VALUES ($1, $2, $3, 'public', 'published') RETURNING id`,
		channelID, title, desc,
	).Scan(&videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	// The flagger matches title + "\n" + description and snapshots exactly that.
	snapshot := title + "\n" + desc
	if err := q.RecordWatchedWordVideoMatch(ctx, sqlcgen.RecordWatchedWordVideoMatchParams{
		WatchedWordID: pgtype.UUID{Bytes: word.ID, Valid: true},
		VideoID:       pgtype.UUID{Bytes: videoID, Valid: true},
		MatchedText:   snapshot, MatchedTerm: word.Word, MatchOffset: 3,
		MatchLength: int32(len([]rune(word.Word))),
	}); err != nil {
		t.Fatalf("RecordWatchedWordVideoMatch: %v", err)
	}

	mine := func() sqlcgen.ListWatchedWordMatchesRow {
		t.Helper()
		rows, err := q.ListWatchedWordMatches(ctx, sqlcgen.ListWatchedWordMatchesParams{ResultLimit: 100})
		if err != nil {
			t.Fatalf("ListWatchedWordMatches: %v", err)
		}
		for _, r := range rows {
			if !r.CommentID.Valid && r.VideoID == videoID {
				return r
			}
		}
		t.Fatalf("no video match for %s", videoID)
		return sqlcgen.ListWatchedWordMatchesRow{}
	}

	got := mine()
	if got.MatchedText != snapshot || got.TargetStatus != "present" {
		t.Fatalf("video match = (%q, %q), want the joined snapshot and present", got.MatchedText, got.TargetStatus)
	}
	if got.VideoTitle == nil || *got.VideoTitle != title || got.AuthorUsername != "wwvid-"+suffix {
		t.Errorf("context = (%v, %q), want the title and the owner", got.VideoTitle, got.AuthorUsername)
	}

	// The creator retitles the video away from the term. The flag and its
	// snapshot stand; only target_status moves.
	if _, err := st.Pool.Exec(ctx,
		`UPDATE videos SET title = 'wholesome cooking', description = '' WHERE id = $1`, videoID,
	); err != nil {
		t.Fatalf("retitle: %v", err)
	}
	got = mine()
	if got.MatchedText != snapshot {
		t.Errorf("snapshot moved to %q after a retitle; it must stay %q", got.MatchedText, snapshot)
	}
	if got.TargetStatus != "edited_away" {
		t.Errorf("target_status = %q, want edited_away", got.TargetStatus)
	}
	if got.VideoTitle == nil || *got.VideoTitle != "wholesome cooking" {
		t.Errorf("live title = %v, want the new one beside the snapshot", got.VideoTitle)
	}
}
