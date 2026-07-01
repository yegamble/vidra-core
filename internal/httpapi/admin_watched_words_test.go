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

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// watchwordFakeRepo is an in-memory watchword.Repository. It enforces the
// case-insensitive uniqueness of the real index and resolves the creator's
// username from the shared auth fake.
type watchwordFakeRepo struct {
	auth    *authFakeRepo
	words   map[uuid.UUID]sqlcgen.WatchedWord
	order   []uuid.UUID
	present map[string]bool
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

func (f *watchwordFakeRepo) DeleteWatchedWord(_ context.Context, id uuid.UUID) (int64, error) {
	w, ok := f.words[id]
	if !ok {
		return 0, nil
	}
	delete(f.words, id)
	delete(f.present, strings.ToLower(w.Word))
	return 1, nil
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
