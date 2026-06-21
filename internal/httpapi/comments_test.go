package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// commentFakeRepo is an in-memory comment.Repository. It resolves author identity
// from the shared authFakeRepo (mirroring the ListCommentsByVideo JOIN on users).
type commentFakeRepo struct {
	users    *authFakeRepo
	comments map[uuid.UUID]sqlcgen.Comment
}

func (f *commentFakeRepo) CreateComment(_ context.Context, a sqlcgen.CreateCommentParams) (sqlcgen.Comment, error) {
	if f.comments == nil {
		f.comments = map[uuid.UUID]sqlcgen.Comment{}
	}
	c := sqlcgen.Comment{
		ID: uuid.New(), VideoID: a.VideoID, UserID: a.UserID, Body: a.Body,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.comments[c.ID] = c
	return c, nil
}

func (f *commentFakeRepo) author(id uuid.UUID) (string, string) {
	for _, u := range f.users.users {
		if u.ID == id {
			return u.Username, u.DisplayName
		}
	}
	return "", ""
}

func (f *commentFakeRepo) ListCommentsByVideo(_ context.Context, a sqlcgen.ListCommentsByVideoParams) ([]sqlcgen.ListCommentsByVideoRow, error) {
	var rows []sqlcgen.ListCommentsByVideoRow
	for _, c := range f.comments {
		if c.VideoID == a.VideoID {
			username, display := f.author(c.UserID)
			rows = append(rows, sqlcgen.ListCommentsByVideoRow{
				ID: c.ID, VideoID: c.VideoID, UserID: c.UserID, Body: c.Body,
				CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
				AuthorUsername: username, AuthorDisplayName: display,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows, nil
}

func (f *commentFakeRepo) GetComment(_ context.Context, id uuid.UUID) (sqlcgen.Comment, error) {
	c, ok := f.comments[id]
	if !ok {
		return sqlcgen.Comment{}, errors.New("not found")
	}
	return c, nil
}

func (f *commentFakeRepo) DeleteComment(_ context.Context, id uuid.UUID) error {
	delete(f.comments, id)
	return nil
}

func listComments(srv *Server, videoID string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/comments", nil))
	return rec
}

func TestCommentCreateListDelete(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)

	parse := func(rec *httptest.ResponseRecorder) commentListResponse {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("list = %d; body=%s", rec.Code, rec.Body.String())
		}
		var body commentListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body
	}

	if c := parse(listComments(srv, vid)); len(c.Comments) != 0 {
		t.Fatalf("initial comments = %d, want 0", len(c.Comments))
	}

	// Posting requires auth.
	if anon := postTo(srv, "/api/v1/videos/"+vid+"/comments", `{"body":"hi"}`); anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon create = %d, want 401", anon.Code)
	}

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"first comment"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created commentView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Body != "first comment" || created.AuthorUsername != "ada" {
		t.Errorf("unexpected created comment: %+v", created)
	}

	cl := parse(listComments(srv, vid))
	if len(cl.Comments) != 1 || cl.Comments[0].Body != "first comment" || cl.Comments[0].AuthorUsername != "ada" {
		t.Fatalf("list after create = %+v", cl.Comments)
	}

	// A different user cannot delete it.
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if bad := sendJSONAuth(srv, http.MethodDelete, "/api/v1/comments/"+created.ID, "", otherTok); bad.Code != http.StatusForbidden {
		t.Errorf("non-author delete = %d, want 403", bad.Code)
	}
	// The author can.
	if del := sendJSONAuth(srv, http.MethodDelete, "/api/v1/comments/"+created.ID, "", tok); del.Code != http.StatusNoContent {
		t.Errorf("author delete = %d, want 204", del.Code)
	}
	if c := parse(listComments(srv, vid)); len(c.Comments) != 0 {
		t.Errorf("comments after delete = %d, want 0", len(c.Comments))
	}
}

func TestCommentsOnNonPublicVideoAre404(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	// A draft (unpublished) private video is not commentable.
	vid := createVideo(t, srv, tok, "ada", `{"title":"secret","privacy":"private"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"hi"}`, tok); rec.Code != http.StatusNotFound {
		t.Errorf("comment on non-public video = %d, want 404", rec.Code)
	}
	if rec := listComments(srv, vid); rec.Code != http.StatusNotFound {
		t.Errorf("list non-public video comments = %d, want 404", rec.Code)
	}
}

func TestCommentBodyValidation(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"   "}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("blank body = %d, want 422", rec.Code)
	}
}
