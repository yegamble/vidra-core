package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// approvalCast is the visibility matrix for comment approval (0161): bob owns a
// requires_approval video, ed edits his channel, eve is the commenter whose
// comment is held, carl is an unrelated signed-in viewer, mia is a moderator
// and ada (the first account) is the admin.
type approvalCast struct {
	srv                          *Server
	vid                          string
	ada, bob, ed, eve, carl, mia string
	edID                         string
	ledger                       *httpAuditFakeRepo
	logs                         *bytes.Buffer
}

func newApprovalCast(t *testing.T) approvalCast {
	t.Helper()
	srv := videoServer(t)
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))
	ledger := &httpAuditFakeRepo{}
	srv.auditLog = audit.NewService(ledger)
	c := approvalCast{srv: srv, ledger: ledger, logs: &buf}
	c.ada = createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	c.bob = createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	c.vid = createPublishedVideo(t, srv, c.bob, "bob",
		`{"title":"v","privacy":"public","comments_policy":"requires_approval"}`)
	c.ed, c.edID = inviteEditor(t, srv, c.bob, "bob", "edd", "ed@example.test")
	c.eve = registerAndToken(t, srv, `{"username":"eve","email":"eve@example.test","password":"supersecret"}`)
	c.carl = registerAndToken(t, srv, `{"username":"carl","email":"carl@example.test","password":"supersecret"}`)
	var miaID string
	c.mia, miaID = registerAndUser(t, srv, `{"username":"mia","email":"mia@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+miaID, `{"role":"moderator"}`, c.ada); rec.Code != http.StatusOK {
		t.Fatalf("promote moderator = %d; body=%s", rec.Code, rec.Body.String())
	}
	login := postTo(srv, "/api/v1/auth/login", `{"email":"mia@example.test","password":"supersecret"}`)
	var mAuth authResponse
	_ = json.Unmarshal(login.Body.Bytes(), &mAuth)
	c.mia = mAuth.Token
	return c
}

func (c approvalCast) post(t *testing.T, tok, body string) commentView {
	t.Helper()
	rec := sendJSONAuth(c.srv, http.MethodPost, "/api/v1/videos/"+c.vid+"/comments", body, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("post %s = %d; body=%s", body, rec.Code, rec.Body.String())
	}
	var cv commentView
	_ = json.Unmarshal(rec.Body.Bytes(), &cv)
	return cv
}

func (c approvalCast) list(t *testing.T, tok string) commentListResponse {
	t.Helper()
	rec := getWithAuth(c.srv, "/api/v1/videos/"+c.vid+"/comments", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d; body=%s", rec.Code, rec.Body.String())
	}
	var out commentListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func (c approvalCast) approve(tok, commentID string) *httpResponse {
	rec := sendJSONAuth(c.srv, http.MethodPost, "/api/v1/videos/"+c.vid+"/comments/"+commentID+"/approve", "", tok)
	return &httpResponse{code: rec.Code, body: rec.Body.String()}
}

type httpResponse struct {
	code int
	body string
}

func hasComment(list commentListResponse, id string) (commentView, bool) {
	for _, cv := range list.Comments {
		if cv.ID == id {
			return cv, true
		}
	}
	return commentView{}, false
}

// TestCommentApprovalHoldsAndScopesVisibility: on a requires_approval video a
// stranger's comment is held, the video's managers and staff post straight
// through, and a held comment is visible to exactly its author, the managers
// and staff — never to another viewer or an anonymous reader, in rows or total.
func TestCommentApprovalHoldsAndScopesVisibility(t *testing.T) {
	c := newApprovalCast(t)

	held := c.post(t, c.eve, `{"body":"from eve"}`)
	if !held.HeldForReview {
		t.Fatalf("stranger comment held_for_review = false, want true")
	}
	for name, tok := range map[string]string{"owner": c.bob, "editor": c.ed, "moderator": c.mia, "admin": c.ada} {
		if cv := c.post(t, tok, `{"body":"from `+name+`"}`); cv.HeldForReview {
			t.Errorf("%s comment held_for_review = true, want it published", name)
		}
	}

	for name, tok := range map[string]string{"author": c.eve, "owner": c.bob, "editor": c.ed, "moderator": c.mia, "admin": c.ada} {
		got := c.list(t, tok)
		cv, ok := hasComment(got, held.ID)
		if !ok || !cv.HeldForReview {
			t.Errorf("%s: held comment visible=%v held=%v, want visible and flagged", name, ok, cv.HeldForReview)
		}
		if got.Total != 5 {
			t.Errorf("%s: total = %d, want 5", name, got.Total)
		}
	}
	for name, tok := range map[string]string{"stranger": c.carl, "anonymous": ""} {
		got := c.list(t, tok)
		if _, ok := hasComment(got, held.ID); ok {
			t.Errorf("%s sees the held comment", name)
		}
		if got.Total != 4 || len(got.Comments) != 4 {
			t.Errorf("%s: total=%d rows=%d, want 4/4 (the held comment counted nowhere)", name, got.Total, len(got.Comments))
		}
	}

	// A held comment can be neither replied to nor pinned until approved.
	if rec := sendJSONAuth(c.srv, http.MethodPost, "/api/v1/videos/"+c.vid+"/comments",
		`{"body":"re","parent_id":"`+held.ID+`"}`, c.bob); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("reply to held = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(c.srv, http.MethodPut, "/api/v1/comments/"+held.ID+"/pin", "", c.bob); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("pin held = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// TestCommentApprovalReleasesAndNotifiesOnce: nothing about a held reply
// reaches a third party's inbox (nor the owner's "comment" notice) until a
// manager approves it; approval then delivers the withheld reply notification
// exactly once, however often it is repeated, and is audited once.
func TestCommentApprovalReleasesAndNotifiesOnce(t *testing.T) {
	c := newApprovalCast(t)
	parent := c.post(t, c.ed, `{"body":"editor says hi"}`)
	reply := c.post(t, c.eve, `{"body":"eve answers","parent_id":"`+parent.ID+`"}`)
	if !reply.HeldForReview {
		t.Fatalf("reply held_for_review = false, want true")
	}
	countOf := func(tok, typ, commentID string) int {
		n := 0
		for _, nv := range notifTypes(t, c.srv, tok) {
			if nv.Type == typ && nv.CommentID == commentID {
				n++
			}
		}
		return n
	}
	if n := countOf(c.ed, "comment_reply", reply.ID); n != 0 {
		t.Fatalf("parent author told about a HELD reply (%d notices)", n)
	}
	if n := countOf(c.bob, "comment", reply.ID); n != 0 {
		t.Fatalf("owner got a plain comment notice for a HELD comment (%d)", n)
	}

	// Authorization: the author, a stranger and anonymous cannot approve.
	for name, tok := range map[string]string{"author": c.eve, "stranger": c.carl} {
		if r := c.approve(tok, reply.ID); r.code != http.StatusForbidden {
			t.Errorf("%s approve = %d, want 403; body=%s", name, r.code, r.body)
		}
	}
	if r := c.approve("", reply.ID); r.code != http.StatusUnauthorized {
		t.Errorf("anonymous approve = %d, want 401", r.code)
	}
	if r := c.approve(c.bob, "00000000-0000-0000-0000-000000000001"); r.code != http.StatusNotFound {
		t.Errorf("approve unknown comment = %d, want 404", r.code)
	}
	other := createPublishedVideo(t, c.srv, c.bob, "bob", `{"title":"w","privacy":"public"}`)
	if rec := sendJSONAuth(c.srv, http.MethodPost, "/api/v1/videos/"+other+"/comments/"+reply.ID+"/approve", "", c.bob); rec.Code != http.StatusNotFound {
		t.Errorf("approve via another video = %d, want 404", rec.Code)
	}

	// The editor approves; the second approval (by the owner) is a no-op 200.
	for i, tok := range []string{c.ed, c.bob} {
		r := c.approve(tok, reply.ID)
		if r.code != http.StatusOK {
			t.Fatalf("approve #%d = %d; body=%s", i+1, r.code, r.body)
		}
		var cv commentView
		_ = json.Unmarshal([]byte(r.body), &cv)
		if cv.HeldForReview || cv.ID != reply.ID || cv.AuthorUsername != "eve" {
			t.Errorf("approve #%d view = %+v, want eve's comment, no longer held", i+1, cv)
		}
	}
	if _, ok := hasComment(c.list(t, ""), reply.ID); !ok {
		t.Error("approved comment still hidden from anonymous readers")
	}
	if n := countOf(c.ed, "comment_reply", reply.ID); n != 1 {
		t.Errorf("parent author reply notices after approval = %d, want exactly 1", n)
	}
	// An editor approved, so the owner learns the comment went live; when the
	// owner approves, they are the one person who needs no notice.
	if n := countOf(c.bob, "comment", reply.ID); n != 1 {
		t.Errorf("owner comment notices after an editor's approval = %d, want 1", n)
	}
	top := c.post(t, c.eve, `{"body":"top-level, held"}`)
	if r := c.approve(c.bob, top.ID); r.code != http.StatusOK {
		t.Fatalf("owner approve = %d; body=%s", r.code, r.body)
	}
	if n := countOf(c.bob, "comment", top.ID); n != 0 {
		t.Errorf("owner notified about the comment they approved themselves (%d)", n)
	}

	var rows []sqlcgen.ListAuditLogRow
	for _, r := range c.ledger.rows {
		if r.Action == observability.ActionCommentApprove && r.ResourceID == reply.ID {
			rows = append(rows, r)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("approve audit rows = %d, want 1 (refusals and no-op repeats do not audit)", len(rows))
	}
	if rows[0].Result != observability.ResultSuccess || rows[0].ResourceType != "comment" || rows[0].ResourceID != reply.ID {
		t.Errorf("audit row = %+v", rows[0])
	}
	if strings.Contains(c.logs.String(), "eve answers") {
		t.Error("a comment body reached the logs")
	}
}

// TestCommentsPolicyAcceptsRequiresApproval: the policy round-trips through
// the video PATCH, and comments stay open for posting (the composer shows).
func TestCommentsPolicyAcceptsRequiresApproval(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+vid, `{"comments_policy":"requires_approval"}`, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d; body=%s", rec.Code, rec.Body.String())
	}
	var v struct {
		CommentsPolicy  string `json:"comments_policy"`
		CommentsEnabled *bool  `json:"comments_enabled"`
	}
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/videos/"+vid, "").Body.Bytes(), &v)
	if v.CommentsEnabled == nil || !*v.CommentsEnabled {
		t.Errorf("comments_enabled = %v, want true (approval still accepts comments)", v.CommentsEnabled)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+vid, `{"comments_policy":"moderated"}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown policy = %d, want 422", rec.Code)
	}
}
