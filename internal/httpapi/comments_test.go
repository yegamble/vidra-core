package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// commentFakeRepo is an in-memory comment.Repository. It resolves author identity
// from the shared authFakeRepo (mirroring the ListCommentsByVideo JOIN on users)
// and, like the real query, hides comments from accounts the viewer has muted
// or blocked (§13).
type commentFakeRepo struct {
	users      *authFakeRepo
	mutes      *muteFakeRepo
	userBlocks *blockFakeRepo
	videos     *videoFakeRepo
	comments   map[uuid.UUID]sqlcgen.Comment
	// pinned mirrors videos.pinned_comment_id (0099): video_id -> pinned comment_id.
	pinned map[uuid.UUID]uuid.UUID
}

// isPinned reports whether commentID is videoID's current pinned comment.
func (f *commentFakeRepo) isPinned(videoID, commentID uuid.UUID) bool {
	return f.pinned != nil && f.pinned[videoID] == commentID
}

func (f *commentFakeRepo) CreateComment(_ context.Context, a sqlcgen.CreateCommentParams) (sqlcgen.Comment, error) {
	if f.comments == nil {
		f.comments = map[uuid.UUID]sqlcgen.Comment{}
	}
	c := sqlcgen.Comment{
		ID: uuid.New(), VideoID: a.VideoID, UserID: a.UserID, Body: a.Body,
		ParentID: a.ParentID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
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
		if c.VideoID != a.VideoID {
			continue
		}
		// Mirror the real query: an authenticated viewer's muted OR blocked (§13)
		// authors are hidden (remote-authored rows have no local author id).
		authorID := uuid.UUID(c.UserID.Bytes)
		if c.UserID.Valid && a.ViewerID.Valid && f.mutes != nil && f.mutes.isMuted(uuid.UUID(a.ViewerID.Bytes), authorID) {
			continue
		}
		if c.UserID.Valid && a.ViewerID.Valid && f.userBlocks != nil && f.userBlocks.isBlocked(uuid.UUID(a.ViewerID.Bytes), authorID) {
			continue
		}
		username, display := f.author(authorID)
		rows = append(rows, sqlcgen.ListCommentsByVideoRow{
			ID: c.ID, VideoID: c.VideoID, UserID: c.UserID, Body: c.Body,
			ParentID: c.ParentID, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
			AuthorUsername: username, AuthorDisplayName: display,
			RemoteActorUrl: c.RemoteActorUrl,
			Hearted:        c.Hearted,
			Pinned:         f.isPinned(c.VideoID, c.ID),
		})
	}
	// Mirror the real query's pinned-first, then newest-first ordering (0099).
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Pinned != rows[j].Pinned {
			return rows[i].Pinned
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	return rows, nil
}

func (f *commentFakeRepo) GetCommentWithMeta(_ context.Context, id uuid.UUID) (sqlcgen.GetCommentWithMetaRow, error) {
	c, ok := f.comments[id]
	if !ok {
		return sqlcgen.GetCommentWithMetaRow{}, errors.New("not found")
	}
	username, display := f.author(uuid.UUID(c.UserID.Bytes))
	return sqlcgen.GetCommentWithMetaRow{
		ID: c.ID, VideoID: c.VideoID, UserID: c.UserID, Body: c.Body,
		ParentID: c.ParentID, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		DeletedAt: c.DeletedAt, Hearted: c.Hearted,
		Pinned:         f.isPinned(c.VideoID, c.ID),
		AuthorUsername: username, AuthorDisplayName: display,
		RemoteActorUrl: c.RemoteActorUrl,
	}, nil
}

func (f *commentFakeRepo) SetCommentHearted(_ context.Context, a sqlcgen.SetCommentHeartedParams) (sqlcgen.Comment, error) {
	c, ok := f.comments[a.ID]
	if !ok {
		return sqlcgen.Comment{}, errors.New("not found")
	}
	c.Hearted = a.Hearted
	f.comments[a.ID] = c
	return c, nil
}

func (f *commentFakeRepo) SetVideoPinnedComment(_ context.Context, a sqlcgen.SetVideoPinnedCommentParams) error {
	if f.pinned == nil {
		f.pinned = map[uuid.UUID]uuid.UUID{}
	}
	if a.PinnedCommentID.Valid {
		f.pinned[a.VideoID] = uuid.UUID(a.PinnedCommentID.Bytes)
	} else {
		delete(f.pinned, a.VideoID)
	}
	return nil
}

func (f *commentFakeRepo) ListAdminComments(_ context.Context, a sqlcgen.ListAdminCommentsParams) ([]sqlcgen.ListAdminCommentsRow, error) {
	var rows []sqlcgen.ListAdminCommentsRow
	for _, c := range f.comments {
		if a.Query != nil && !strings.Contains(strings.ToLower(c.Body), strings.ToLower(*a.Query)) {
			continue
		}
		username, display := f.author(uuid.UUID(c.UserID.Bytes))
		title := ""
		if f.videos != nil {
			title = f.videos.videos[c.VideoID].Title
		}
		rows = append(rows, sqlcgen.ListAdminCommentsRow{
			ID: c.ID, VideoID: c.VideoID, Body: c.Body, CreatedAt: c.CreatedAt,
			AuthorUsername: username, AuthorDisplayName: display, VideoTitle: title,
		})
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

func (f *commentFakeRepo) UpdateComment(_ context.Context, a sqlcgen.UpdateCommentParams) (sqlcgen.Comment, error) {
	c, ok := f.comments[a.ID]
	if !ok {
		return sqlcgen.Comment{}, errors.New("not found")
	}
	c.Body = a.Body
	c.UpdatedAt = time.Now()
	f.comments[a.ID] = c
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

func TestCommentReplyThreading(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)
	other := createPublishedVideo(t, srv, tok, "ada", `{"title":"w","privacy":"public"}`)

	// A top-level comment has a null parent_id.
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"top"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create top = %d; body=%s", rec.Code, rec.Body.String())
	}
	var top commentView
	_ = json.Unmarshal(rec.Body.Bytes(), &top)
	if top.ParentID != nil {
		t.Errorf("top-level parent_id = %v, want null", *top.ParentID)
	}

	// A reply carries parent_id pointing at the top-level comment.
	rec = sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"reply","parent_id":"`+top.ID+`"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create reply = %d; body=%s", rec.Code, rec.Body.String())
	}
	var reply commentView
	_ = json.Unmarshal(rec.Body.Bytes(), &reply)
	if reply.ParentID == nil || *reply.ParentID != top.ID {
		t.Errorf("reply parent_id = %v, want %s", reply.ParentID, top.ID)
	}

	// Both surface in the list, and the reply keeps its parent link.
	var list commentListResponse
	_ = json.Unmarshal(listComments(srv, vid).Body.Bytes(), &list)
	if len(list.Comments) != 2 {
		t.Fatalf("list = %d comments, want 2", len(list.Comments))
	}

	// A malformed parent_id is a 422 (field-level validation).
	if bad := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"x","parent_id":"not-a-uuid"}`, tok); bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("malformed parent_id = %d, want 422", bad.Code)
	}

	// A parent on another video is rejected (can't smuggle a reply cross-thread).
	if xrec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+other+"/comments", `{"body":"x","parent_id":"`+top.ID+`"}`, tok); xrec.Code != http.StatusUnprocessableEntity {
		t.Errorf("cross-video parent = %d, want 422", xrec.Code)
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

func TestCommentsHideMutedAuthors(t *testing.T) {
	srv := videoServer(t)
	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, ada, "ada", `{"title":"v","privacy":"public"}`)
	bobTok, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	charlieTok, _ := registerAndUser(t, srv, `{"username":"charlie","email":"charlie@example.test","password":"supersecret"}`)

	parse := func(rec *httptest.ResponseRecorder) []commentView {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("list = %d; body=%s", rec.Code, rec.Body.String())
		}
		var body commentListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body.Comments
	}

	// bob and charlie each comment on ada's video.
	for _, c := range []struct{ tok, body string }{{bobTok, "from bob"}, {charlieTok, "from charlie"}} {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"`+c.body+`"}`, c.tok); rec.Code != http.StatusCreated {
			t.Fatalf("comment %q = %d; body=%s", c.body, rec.Code, rec.Body.String())
		}
	}

	// ada mutes bob.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/mutes/accounts/"+bobID, "", ada); rec.Code != http.StatusNoContent {
		t.Fatalf("mute bob = %d; body=%s", rec.Code, rec.Body.String())
	}

	// ada (authenticated) no longer sees bob's comment; an anonymous viewer still does.
	adaSees := parse(getWithAuth(srv, "/api/v1/videos/"+vid+"/comments", ada))
	if len(adaSees) != 1 || adaSees[0].Body != "from charlie" {
		t.Fatalf("ada (muted bob) sees %+v, want only [from charlie]", adaSees)
	}
	if anon := parse(listComments(srv, vid)); len(anon) != 2 {
		t.Errorf("anon sees %d comments, want 2 (mutes are per-viewer)", len(anon))
	}

	// Unmuting restores bob's comment for ada.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/mutes/accounts/"+bobID, "", ada); rec.Code != http.StatusNoContent {
		t.Fatalf("unmute bob = %d", rec.Code)
	}
	if got := parse(getWithAuth(srv, "/api/v1/videos/"+vid+"/comments", ada)); len(got) != 2 {
		t.Errorf("ada after unmute sees %d comments, want 2", len(got))
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

func TestCommentEdit(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"original"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created commentView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	path := "/api/v1/comments/" + created.ID

	// The author edits their own comment → 200, body updated, edited=true.
	edit := sendJSONAuth(srv, http.MethodPatch, path, `{"body":"revised"}`, tok)
	if edit.Code != http.StatusOK {
		t.Fatalf("edit = %d; body=%s", edit.Code, edit.Body.String())
	}
	var updated commentView
	_ = json.Unmarshal(edit.Body.Bytes(), &updated)
	if updated.Body != "revised" || !updated.Edited {
		t.Errorf("edited comment = %+v, want body 'revised' + edited true", updated)
	}

	// A blank body → 422.
	if bad := sendJSONAuth(srv, http.MethodPatch, path, `{"body":"   "}`, tok); bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("blank edit = %d, want 422", bad.Code)
	}
	// Another user → 403 (moderators delete, not edit — so no exception).
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if bad := sendJSONAuth(srv, http.MethodPatch, path, `{"body":"hacked"}`, otherTok); bad.Code != http.StatusForbidden {
		t.Errorf("non-author edit = %d, want 403", bad.Code)
	}
	// An unknown comment → 404.
	if nf := sendJSONAuth(srv, http.MethodPatch, "/api/v1/comments/"+uuid.NewString(), `{"body":"x"}`, tok); nf.Code != http.StatusNotFound {
		t.Errorf("unknown edit = %d, want 404", nf.Code)
	}
	// Anonymous → 401.
	if anon := sendJSONAuth(srv, http.MethodPatch, path, `{"body":"x"}`, ""); anon.Code != http.StatusUnauthorized {
		t.Errorf("anon edit = %d, want 401", anon.Code)
	}
}

// TestCommentPinHeart exercises the creator pin + heart endpoints (0099): the
// auth matrix (owner OK, non-owner 403, moderator staff-escape OK, unknown 404),
// the pin swap + unpin, and the pinned/hearted flags round-tripping through the
// list and single-comment responses.
func TestCommentPinHeart(t *testing.T) {
	srv := videoServer(t)
	// ada is the first registered user → admin. bob owns the video (not staff).
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	ownerTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	vid := createPublishedVideo(t, srv, ownerTok, "bob", `{"title":"v","privacy":"public"}`)
	eveTok := registerAndToken(t, srv, `{"username":"eve","email":"eve@example.test","password":"supersecret"}`)

	// Promote mia to moderator and re-login so the role rides her token.
	_, miaID := registerAndUser(t, srv, `{"username":"mia","email":"mia@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+miaID, `{"role":"moderator"}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("promote moderator = %d; body=%s", rec.Code, rec.Body.String())
	}
	login := postTo(srv, "/api/v1/auth/login", `{"email":"mia@example.test","password":"supersecret"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("moderator login = %d; body=%s", login.Code, login.Body.String())
	}
	var mAuth authResponse
	_ = json.Unmarshal(login.Body.Bytes(), &mAuth)
	modTok := mAuth.Token

	post := func(tok, body string) commentView {
		t.Helper()
		rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"`+body+`"}`, tok)
		if rec.Code != http.StatusCreated {
			t.Fatalf("post %q = %d; body=%s", body, rec.Code, rec.Body.String())
		}
		var cv commentView
		_ = json.Unmarshal(rec.Body.Bytes(), &cv)
		return cv
	}
	list := func() []commentView {
		t.Helper()
		var body commentListResponse
		_ = json.Unmarshal(listComments(srv, vid).Body.Bytes(), &body)
		return body.Comments
	}

	// eve comments twice on bob's video.
	first := post(eveTok, "first")
	second := post(eveTok, "second")
	// A freshly created comment is never pinned or hearted.
	if first.Pinned || first.Hearted {
		t.Errorf("new comment pinned/hearted = %v/%v, want false/false", first.Pinned, first.Hearted)
	}

	pinFirst := "/api/v1/comments/" + first.ID + "/pin"
	// Non-owner (eve) cannot pin → 403.
	if rec := sendJSONAuth(srv, http.MethodPut, pinFirst, "", eveTok); rec.Code != http.StatusForbidden {
		t.Errorf("non-owner pin = %d, want 403", rec.Code)
	}
	// Anonymous → 401.
	if rec := sendJSONAuth(srv, http.MethodPut, pinFirst, "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon pin = %d, want 401", rec.Code)
	}
	// Unknown comment → 404.
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/comments/"+uuid.NewString()+"/pin", "", ownerTok); rec.Code != http.StatusNotFound {
		t.Errorf("pin unknown = %d, want 404", rec.Code)
	}

	// Owner pins the first comment → 200, pinned true, and list returns it first.
	rec := sendJSONAuth(srv, http.MethodPut, pinFirst, "", ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner pin = %d; body=%s", rec.Code, rec.Body.String())
	}
	var pinned commentView
	_ = json.Unmarshal(rec.Body.Bytes(), &pinned)
	if !pinned.Pinned {
		t.Errorf("pin response pinned = false, want true")
	}
	if got := list(); len(got) != 2 || got[0].ID != first.ID || !got[0].Pinned {
		t.Fatalf("after pin, list = %+v, want first pinned-first", got)
	}

	// Moderator staff-escape: mia pins the second comment, replacing the first.
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/comments/"+second.ID+"/pin", "", modTok); rec.Code != http.StatusOK {
		t.Fatalf("moderator pin = %d; body=%s", rec.Code, rec.Body.String())
	}
	got := list()
	if got[0].ID != second.ID || !got[0].Pinned {
		t.Errorf("after moderator pin, list[0] = %+v, want second pinned", got[0])
	}
	for _, cm := range got {
		if cm.ID == first.ID && cm.Pinned {
			t.Error("pinning second should have replaced first's pin")
		}
	}

	// Owner unpins the second → 200, and nothing is pinned afterward.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/comments/"+second.ID+"/pin", "", ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("owner unpin = %d; body=%s", rec.Code, rec.Body.String())
	}
	for _, cm := range list() {
		if cm.Pinned {
			t.Errorf("comment %s still pinned after unpin", cm.ID)
		}
	}
	// Unpinning again (nothing pinned) is a harmless 200.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/comments/"+second.ID+"/pin", "", ownerTok); rec.Code != http.StatusOK {
		t.Errorf("unpin-when-not-pinned = %d, want 200", rec.Code)
	}

	// Heart round-trip: owner hearts first → hearted true in the response + list;
	// unheart clears it.
	hb := sendJSONAuth(srv, http.MethodPut, "/api/v1/comments/"+first.ID+"/heart", "", ownerTok)
	if hb.Code != http.StatusOK {
		t.Fatalf("owner heart = %d; body=%s", hb.Code, hb.Body.String())
	}
	var hearted commentView
	_ = json.Unmarshal(hb.Body.Bytes(), &hearted)
	if !hearted.Hearted {
		t.Errorf("heart response hearted = false, want true")
	}
	for _, cm := range list() {
		if cm.ID == first.ID && !cm.Hearted {
			t.Error("first should be hearted in the list")
		}
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/comments/"+first.ID+"/heart", "", ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("owner unheart = %d; body=%s", rec.Code, rec.Body.String())
	}
	for _, cm := range list() {
		if cm.ID == first.ID && cm.Hearted {
			t.Error("first should no longer be hearted after unheart")
		}
	}
}

// TestCommentPinRejectsReply: only a top-level comment can be pinned (0099).
func TestCommentPinRejectsReply(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"v","privacy":"public"}`)

	topRec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"top"}`, ownerTok)
	var top commentView
	_ = json.Unmarshal(topRec.Body.Bytes(), &top)
	replyRec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"reply","parent_id":"`+top.ID+`"}`, ownerTok)
	var reply commentView
	_ = json.Unmarshal(replyRec.Body.Bytes(), &reply)

	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/comments/"+reply.ID+"/pin", "", ownerTok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("pin reply = %d, want 422", rec.Code)
	}
	// The owner can still heart a reply (heart is not top-level-only).
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/comments/"+reply.ID+"/heart", "", ownerTok); rec.Code != http.StatusOK {
		t.Errorf("heart reply = %d, want 200", rec.Code)
	}
}

func (f *commentFakeRepo) CountCommentsByVideo(ctx context.Context, a sqlcgen.CountCommentsByVideoParams) (int64, error) {
	rows, err := f.ListCommentsByVideo(ctx, sqlcgen.ListCommentsByVideoParams{
		VideoID: a.VideoID, ViewerID: a.ViewerID, ResultLimit: 1 << 30,
	})
	return int64(len(rows)), err
}

func (f *commentFakeRepo) CountAdminComments(ctx context.Context, query *string) (int64, error) {
	rows, err := f.ListAdminComments(ctx, sqlcgen.ListAdminCommentsParams{Query: query, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}
