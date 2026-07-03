package watchword

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory watchword.Repository enforcing the case-insensitive
// uniqueness of the real unique index.
type fakeRepo struct {
	words   map[uuid.UUID]sqlcgen.WatchedWord
	order   []uuid.UUID
	present map[string]bool // lower(word) -> exists
	matches []sqlcgen.ListWatchedWordMatchesRow
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{words: map[uuid.UUID]sqlcgen.WatchedWord{}, present: map[string]bool{}}
}

func (f *fakeRepo) CreateWatchedWord(_ context.Context, a sqlcgen.CreateWatchedWordParams) (sqlcgen.WatchedWord, error) {
	if f.present[strings.ToLower(a.Word)] {
		return sqlcgen.WatchedWord{}, &pgconn.PgError{Code: "23505"} // unique violation
	}
	w := sqlcgen.WatchedWord{ID: uuid.New(), Word: a.Word, CreatedBy: a.CreatedBy, CreatedAt: time.Now()}
	f.words[w.ID] = w
	f.order = append(f.order, w.ID)
	f.present[strings.ToLower(a.Word)] = true
	return w, nil
}

func (f *fakeRepo) ListWatchedWords(_ context.Context, _ sqlcgen.ListWatchedWordsParams) ([]sqlcgen.ListWatchedWordsRow, error) {
	var rows []sqlcgen.ListWatchedWordsRow
	for i := len(f.order) - 1; i >= 0; i-- { // newest first
		w, ok := f.words[f.order[i]]
		if !ok {
			continue
		}
		rows = append(rows, sqlcgen.ListWatchedWordsRow{ID: w.ID, Word: w.Word, CreatedAt: w.CreatedAt})
	}
	return rows, nil
}

func (f *fakeRepo) DeleteWatchedWord(_ context.Context, id uuid.UUID) (int64, error) {
	w, ok := f.words[id]
	if !ok {
		return 0, nil
	}
	delete(f.words, id)
	delete(f.present, strings.ToLower(w.Word))
	return 1, nil
}

func (f *fakeRepo) MatchWatchedWords(_ context.Context, text string) ([]sqlcgen.MatchWatchedWordsRow, error) {
	var out []sqlcgen.MatchWatchedWordsRow
	for _, id := range f.order {
		w, ok := f.words[id]
		if ok && strings.Contains(strings.ToLower(text), strings.ToLower(w.Word)) {
			out = append(out, sqlcgen.MatchWatchedWordsRow{ID: w.ID, Word: w.Word})
		}
	}
	return out, nil
}

func (f *fakeRepo) RecordWatchedWordMatch(_ context.Context, a sqlcgen.RecordWatchedWordMatchParams) error {
	for _, m := range f.matches {
		if m.CommentID == a.CommentID && m.Word == f.words[a.WatchedWordID].Word {
			return nil // idempotent
		}
	}
	f.matches = append(f.matches, sqlcgen.ListWatchedWordMatchesRow{
		ID: uuid.New(), Word: f.words[a.WatchedWordID].Word, CommentID: a.CommentID, CreatedAt: time.Now(),
	})
	return nil
}

func (f *fakeRepo) RecordWatchedWordVideoMatch(_ context.Context, a sqlcgen.RecordWatchedWordVideoMatchParams) error {
	for _, m := range f.matches {
		if !m.CommentID.Valid && m.VideoID == uuid.UUID(a.VideoID.Bytes) && m.Word == f.words[a.WatchedWordID].Word {
			return nil // idempotent (mirrors the partial unique index)
		}
	}
	f.matches = append(f.matches, sqlcgen.ListWatchedWordMatchesRow{
		ID: uuid.New(), Word: f.words[a.WatchedWordID].Word, VideoID: uuid.UUID(a.VideoID.Bytes), CreatedAt: time.Now(),
	})
	return nil
}

func (f *fakeRepo) ListWatchedWordMatches(_ context.Context, _ sqlcgen.ListWatchedWordMatchesParams) ([]sqlcgen.ListWatchedWordMatchesRow, error) {
	out := make([]sqlcgen.ListWatchedWordMatchesRow, 0, len(f.matches))
	for i := len(f.matches) - 1; i >= 0; i-- { // newest first
		out = append(out, f.matches[i])
	}
	return out, nil
}

func TestFlagCommentAndListMatches(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo())
	admin := uuid.New()
	if _, err := svc.Add(ctx, "spam", admin); err != nil {
		t.Fatalf("Add spam: %v", err)
	}
	if _, err := svc.Add(ctx, "scam", admin); err != nil {
		t.Fatalf("Add scam: %v", err)
	}

	c1 := uuid.New()
	n, err := svc.FlagComment(ctx, c1, "this is SPAM and a scam")
	if err != nil {
		t.Fatalf("FlagComment: %v", err)
	}
	if n != 2 {
		t.Errorf("matched terms = %d, want 2 (spam + scam, case-insensitive)", n)
	}

	// A clean comment matches nothing.
	if n, _ := svc.FlagComment(ctx, uuid.New(), "a perfectly nice comment"); n != 0 {
		t.Errorf("clean comment matched = %d, want 0", n)
	}

	// Re-flagging the same comment is idempotent (no duplicate rows).
	if _, err := svc.FlagComment(ctx, c1, "this is SPAM and a scam"); err != nil {
		t.Fatalf("re-FlagComment: %v", err)
	}

	matches, err := svc.ListMatches(ctx, 20, 0)
	if err != nil {
		t.Fatalf("ListMatches: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2 (the two terms on c1, dedup'd)", len(matches))
	}
	for _, m := range matches {
		if m.CommentID != c1 {
			t.Errorf("match for %s, want %s", m.CommentID, c1)
		}
	}
}

// TestFlagVideoAndListMatches proves §12: a video's title+description are
// matched with the same matcher, recorded idempotently against the video
// target, and surface in the review queue typed "video" with the video context
// (no comment fields).
func TestFlagVideoAndListMatches(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo())
	admin := uuid.New()
	if _, err := svc.Add(ctx, "spam", admin); err != nil {
		t.Fatalf("Add spam: %v", err)
	}
	if _, err := svc.Add(ctx, "scam", admin); err != nil {
		t.Fatalf("Add scam: %v", err)
	}

	v1 := uuid.New()
	n, err := svc.FlagVideo(ctx, v1, "SPAM title\na scam description")
	if err != nil {
		t.Fatalf("FlagVideo: %v", err)
	}
	if n != 2 {
		t.Errorf("matched terms = %d, want 2 (title + description, case-insensitive)", n)
	}

	// A clean video matches nothing; re-flagging is idempotent.
	if n, _ := svc.FlagVideo(ctx, uuid.New(), "wholesome cooking video"); n != 0 {
		t.Errorf("clean video matched = %d, want 0", n)
	}
	if _, err := svc.FlagVideo(ctx, v1, "SPAM title\na scam description"); err != nil {
		t.Fatalf("re-FlagVideo: %v", err)
	}

	matches, err := svc.ListMatches(ctx, 20, 0)
	if err != nil {
		t.Fatalf("ListMatches: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2 (the two terms on v1, dedup'd)", len(matches))
	}
	for _, m := range matches {
		if m.Type != MatchTargetVideo || m.VideoID != v1 {
			t.Errorf("match = %+v, want type=video for %s", m, v1)
		}
		if m.CommentID != uuid.Nil || m.CommentBody != "" {
			t.Errorf("video match carries comment fields: %+v", m)
		}
	}

	// Comment and video matches share one queue, each typed.
	c1 := uuid.New()
	if _, err := svc.FlagComment(ctx, c1, "more spam"); err != nil {
		t.Fatalf("FlagComment: %v", err)
	}
	mixed, _ := svc.ListMatches(ctx, 20, 0)
	if len(mixed) != 3 || mixed[0].Type != MatchTargetComment || mixed[0].CommentID != c1 {
		t.Fatalf("mixed queue = %+v, want the comment match newest-first", mixed)
	}
}

func TestAddListDelete(t *testing.T) {
	svc := NewService(newFakeRepo())
	ctx := context.Background()
	mod := uuid.New()

	w1, err := svc.Add(ctx, "spam", mod)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := svc.Add(ctx, "abuse", mod); err != nil {
		t.Fatalf("Add abuse: %v", err)
	}

	items, err := svc.List(ctx, 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 || items[0].Word != "abuse" || items[1].Word != "spam" {
		t.Fatalf("list = %+v, want [abuse, spam] newest-first", items)
	}

	if err := svc.Delete(ctx, w1.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Idempotent delete of an absent id.
	if err := svc.Delete(ctx, uuid.New()); err != nil {
		t.Errorf("idempotent Delete: %v", err)
	}
	if items, _ := svc.List(ctx, 20, 0); len(items) != 1 || items[0].Word != "abuse" {
		t.Errorf("list after delete = %+v, want [abuse]", items)
	}
}

func TestAddDuplicate(t *testing.T) {
	svc := NewService(newFakeRepo())
	ctx := context.Background()
	if _, err := svc.Add(ctx, "Spam", uuid.New()); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// A case-insensitive duplicate → ErrAlreadyExists.
	if _, err := svc.Add(ctx, "spam", uuid.New()); err != ErrAlreadyExists {
		t.Errorf("duplicate Add = %v, want ErrAlreadyExists", err)
	}
}
