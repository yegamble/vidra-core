package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/watchword"
)

// watchwordFakeRepo is an in-memory watchword.Repository. It enforces the
// case-insensitive uniqueness of the real index and resolves the creator's
// username from the shared auth fake.
//
// Match rows are STORED as the columns 0132 defines and PROJECTED at list time,
// exactly as the SQL does — the snapshot from the stored row, the excerpt and
// target_status from the LIVE comment/video. A fake that echoed the snapshot
// back as the live body could not tell a snapshot from a live join, which is the
// whole defect this slice closes.
type watchwordFakeRepo struct {
	auth     *authFakeRepo
	videos   *videoFakeRepo
	comments *commentFakeRepo
	words    map[uuid.UUID]sqlcgen.WatchedWord
	order    []uuid.UUID
	present  map[string]bool
	matches  []watchwordFakeMatch
}

// watchwordFakeMatch is one stored watched_word_matches row.
type watchwordFakeMatch struct {
	id            uuid.UUID
	watchedWordID pgtype.UUID // NULL once the word is deleted (0132: ON DELETE SET NULL)
	commentID     pgtype.UUID
	videoID       uuid.UUID
	matchedText   string
	matchedTerm   string
	matchOffset   int32
	matchLength   int32
	status        string
	moderatorNote string
	resolvedBy    pgtype.UUID
	resolvedAt    pgtype.Timestamptz
	createdAt     time.Time
}

func (f *watchwordFakeRepo) CreateWatchedWord(_ context.Context, a sqlcgen.CreateWatchedWordParams) (sqlcgen.WatchedWord, error) {
	if f.words == nil {
		f.words = map[uuid.UUID]sqlcgen.WatchedWord{}
		f.present = map[string]bool{}
	}
	if f.present[strings.ToLower(a.Word)] {
		return sqlcgen.WatchedWord{}, &pgconn.PgError{Code: "23505"}
	}
	w := sqlcgen.WatchedWord{ID: uuid.New(), Word: a.Word, CreatedBy: a.CreatedBy, CreatedAt: time.Now()}
	f.words[w.ID] = w
	f.order = append(f.order, w.ID)
	f.present[strings.ToLower(a.Word)] = true
	return w, nil
}

func (f *watchwordFakeRepo) ListWatchedWords(_ context.Context, _ sqlcgen.ListWatchedWordsParams) ([]sqlcgen.ListWatchedWordsRow, error) {
	var rows []sqlcgen.ListWatchedWordsRow
	for i := len(f.order) - 1; i >= 0; i-- {
		w, ok := f.words[f.order[i]]
		if !ok {
			continue
		}
		row := sqlcgen.ListWatchedWordsRow{ID: w.ID, Word: w.Word, CreatedAt: w.CreatedAt}
		if u, err := f.auth.GetUserByID(context.Background(), uuid.UUID(w.CreatedBy.Bytes)); err == nil {
			un := u.Username
			row.CreatedByUsername = &un
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// DeleteWatchedWord removes the term. Its matches SURVIVE with watched_word_id
// NULLed — 0132 relaxed the FK from ON DELETE CASCADE precisely so that pruning
// the term list stops discarding review history.
func (f *watchwordFakeRepo) DeleteWatchedWord(_ context.Context, id uuid.UUID) (int64, error) {
	w, ok := f.words[id]
	if !ok {
		return 0, nil
	}
	delete(f.words, id)
	delete(f.present, strings.ToLower(w.Word))
	for i := range f.matches {
		if f.matches[i].watchedWordID.Valid && uuid.UUID(f.matches[i].watchedWordID.Bytes) == id {
			f.matches[i].watchedWordID = pgtype.UUID{}
		}
	}
	return 1, nil
}

func (f *watchwordFakeRepo) MatchWatchedWords(_ context.Context, text string) ([]sqlcgen.MatchWatchedWordsRow, error) {
	var out []sqlcgen.MatchWatchedWordsRow
	for _, id := range f.order {
		w, ok := f.words[id]
		if ok && strings.Contains(strings.ToLower(text), strings.ToLower(w.Word)) {
			out = append(out, sqlcgen.MatchWatchedWordsRow{ID: w.ID, Word: w.Word})
		}
	}
	return out, nil
}

func (f *watchwordFakeRepo) RecordWatchedWordMatch(_ context.Context, a sqlcgen.RecordWatchedWordMatchParams) error {
	for _, m := range f.matches {
		if m.commentID == a.CommentID && m.watchedWordID == a.WatchedWordID {
			return nil // ON CONFLICT DO NOTHING: the ORIGINAL snapshot stands
		}
	}
	f.matches = append(f.matches, watchwordFakeMatch{
		id: uuid.New(), watchedWordID: a.WatchedWordID, commentID: a.CommentID,
		matchedText: a.MatchedText, matchedTerm: a.MatchedTerm,
		matchOffset: a.MatchOffset, matchLength: a.MatchLength,
		status: watchword.StatusOpen, createdAt: time.Now(),
	})
	return nil
}

func (f *watchwordFakeRepo) RecordWatchedWordVideoMatch(_ context.Context, a sqlcgen.RecordWatchedWordVideoMatchParams) error {
	vid := uuid.UUID(a.VideoID.Bytes)
	for _, m := range f.matches {
		if !m.commentID.Valid && m.videoID == vid && m.watchedWordID == a.WatchedWordID {
			return nil // idempotent (mirrors the partial unique index)
		}
	}
	f.matches = append(f.matches, watchwordFakeMatch{
		id: uuid.New(), watchedWordID: a.WatchedWordID, videoID: vid,
		matchedText: a.MatchedText, matchedTerm: a.MatchedTerm,
		matchOffset: a.MatchOffset, matchLength: a.MatchLength,
		status: watchword.StatusOpen, createdAt: time.Now(),
	})
	return nil
}

func (f *watchwordFakeRepo) ResolveWatchedWordMatch(_ context.Context, a sqlcgen.ResolveWatchedWordMatchParams) (int64, error) {
	for i := range f.matches {
		if f.matches[i].id == a.ID {
			// Idempotent like ResolveReport: a repeat overwrites and still
			// reports one row affected.
			f.matches[i].status = a.Status
			f.matches[i].moderatorNote = a.ModeratorNote
			f.matches[i].resolvedBy = a.ResolvedBy
			f.matches[i].resolvedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return 1, nil
		}
	}
	return 0, nil
}

// liveText returns the match target's CURRENT text — the comment body, or the
// video's title+description joined exactly as the flagger joins them — plus the
// video id, title and author the queue's JOINs resolve.
func (f *watchwordFakeRepo) liveText(m watchwordFakeMatch) (text string, videoID uuid.UUID, title, author string) {
	if m.commentID.Valid && f.comments != nil {
		c, ok := f.comments.comments[uuid.UUID(m.commentID.Bytes)]
		if ok {
			text = c.Body
			videoID = c.VideoID
			if c.UserID.Valid {
				author, _ = f.comments.author(uuid.UUID(c.UserID.Bytes))
			}
		}
	} else {
		videoID = m.videoID
	}
	if f.videos != nil {
		if v, ok := f.videos.videos[videoID]; ok {
			title = v.Title
			if !m.commentID.Valid {
				text = v.Title + "\n" + v.Description
				if f.auth != nil {
					for _, u := range f.auth.users {
						if u.ID == v.OwnerID {
							author = u.Username
						}
					}
				}
			}
		}
	}
	return text, videoID, title, author
}

func (f *watchwordFakeRepo) ListWatchedWordMatches(_ context.Context, a sqlcgen.ListWatchedWordMatchesParams) ([]sqlcgen.ListWatchedWordMatchesRow, error) {
	out := make([]sqlcgen.ListWatchedWordMatchesRow, 0, len(f.matches))
	for i := len(f.matches) - 1; i >= 0; i-- {
		m := f.matches[i]
		if a.Status != nil && m.status != *a.Status {
			continue
		}
		term := m.matchedTerm
		if term == "" && m.watchedWordID.Valid {
			term = f.words[uuid.UUID(m.watchedWordID.Bytes)].Word
		}
		text, videoID, title, author := f.liveText(m)
		targetStatus := watchword.TargetEditedAway
		if term != "" && strings.Contains(strings.ToLower(text), strings.ToLower(term)) {
			targetStatus = watchword.TargetPresent
		}
		row := sqlcgen.ListWatchedWordMatchesRow{
			ID: m.id, CreatedAt: m.createdAt, Word: term,
			MatchedText: m.matchedText, MatchOffset: m.matchOffset, MatchLength: m.matchLength,
			Status: m.status, ModeratorNote: m.moderatorNote, ResolvedAt: m.resolvedAt,
			TermActive: m.watchedWordID.Valid, TargetStatus: targetStatus,
			CommentID: m.commentID, VideoID: videoID, AuthorUsername: author,
		}
		if m.commentID.Valid {
			body := text
			row.CommentBody = &body
		}
		if title != "" {
			row.VideoTitle = &title
		}
		if m.resolvedBy.Valid && f.auth != nil {
			if u, err := f.auth.GetUserByID(context.Background(), uuid.UUID(m.resolvedBy.Bytes)); err == nil {
				un := u.Username
				row.ResolvedByUsername = &un
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// watchedWordsBody parses GET /admin/watched-words.
type watchedWordsBody struct {
	Words []struct {
		ID                string `json:"id"`
		Word              string `json:"word"`
		CreatedByUsername string `json:"created_by_username"`
	} `json:"words"`
}

func TestWatchedWordsFlow(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// Add a word.
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/watched-words", `{"word":"spam"}`, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created watchedWordView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Word != "spam" || created.ID == "" {
		t.Fatalf("created = %+v, want word=spam with id", created)
	}

	// A case-insensitive duplicate → 409.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/watched-words", `{"word":"SPAM"}`, admin); rec.Code != http.StatusConflict {
		t.Errorf("duplicate add = %d, want 409", rec.Code)
	}
	// Blank word → 422.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/watched-words", `{"word":"  "}`, admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("blank word = %d, want 422", rec.Code)
	}

	// List shows the word with the adder's username.
	var body watchedWordsBody
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/admin/watched-words", admin).Body.Bytes(), &body)
	if len(body.Words) != 1 || body.Words[0].Word != "spam" || body.Words[0].CreatedByUsername != "ada" {
		t.Fatalf("list = %+v, want [spam by ada]", body.Words)
	}

	// Delete it (idempotent) → list empty.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/watched-words/"+created.ID, "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/watched-words/"+created.ID, "", admin); rec.Code != http.StatusNoContent {
		t.Errorf("idempotent delete = %d, want 204", rec.Code)
	}
	var after watchedWordsBody
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/admin/watched-words", admin).Body.Bytes(), &after)
	if len(after.Words) != 0 {
		t.Errorf("list after delete = %d, want 0", len(after.Words))
	}
}

func TestWatchedWordsAuth(t *testing.T) {
	srv := videoServer(t)
	_ = createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// A regular user is forbidden on all three routes.
	someID := uuid.New().String()
	forbidden := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/admin/watched-words", ""},
		{http.MethodPost, "/api/v1/admin/watched-words", `{"word":"x"}`},
		{http.MethodDelete, "/api/v1/admin/watched-words/" + someID, ""},
	}
	for _, tc := range forbidden {
		if rec := sendJSONAuth(srv, tc.method, tc.path, tc.body, bob); rec.Code != http.StatusForbidden {
			t.Errorf("non-mod %s %s = %d, want 403", tc.method, tc.path, rec.Code)
		}
		if rec := sendJSONAuth(srv, tc.method, tc.path, tc.body, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anon %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func (f *watchwordFakeRepo) CountWatchedWords(ctx context.Context) (int64, error) {
	rows, err := f.ListWatchedWords(ctx, sqlcgen.ListWatchedWordsParams{ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *watchwordFakeRepo) CountWatchedWordMatches(ctx context.Context, status *string) (int64, error) {
	rows, err := f.ListWatchedWordMatches(ctx, sqlcgen.ListWatchedWordMatchesParams{Status: status, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}
