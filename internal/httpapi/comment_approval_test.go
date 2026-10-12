package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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
	videos                       *videoFakeRepo
}

func newApprovalCast(t *testing.T) approvalCast {
	t.Helper()
	srv, _, _, _, videos := videoServerFull(t, testConfig())
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))
	ledger := &httpAuditFakeRepo{}
	srv.auditLog = audit.NewService(ledger)
	c := approvalCast{srv: srv, ledger: ledger, logs: &buf, videos: videos}
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

// approve approves the version of the comment the reviewer is looking at now
// (its current updated_at), as the Studio queue and watch page do.
func (c approvalCast) approve(tok, commentID string) *httpResponse {
	version := "2000-01-01T00:00:00Z" // an unknown comment has no version
	if id, err := uuid.Parse(commentID); err == nil {
		if cm, err := c.srv.commentsvc.Get(context.Background(), id); err == nil {
			version = cm.UpdatedAt.Format(time.RFC3339Nano)
		}
	}
	return c.approveAt(tok, commentID, `{"updated_at":"`+version+`"}`)
}

func (c approvalCast) approveAt(tok, commentID, body string) *httpResponse {
	rec := sendJSONAuth(c.srv, http.MethodPost, "/api/v1/videos/"+c.vid+"/comments/"+commentID+"/approve", body, tok)
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

// TestReviewersRejectAHeldCommentByDeletingIt: DELETE /comments/{id} is the
// reject half of approval — a video's reviewers may delete a HELD comment on
// it, while a stranger still may not, and a published comment stays
// author/staff-only exactly as before.
func TestReviewersRejectAHeldCommentByDeletingIt(t *testing.T) {
	c := newApprovalCast(t)
	del := func(tok, id string) int {
		return sendJSONAuth(c.srv, http.MethodDelete, "/api/v1/comments/"+id, "", tok).Code
	}
	first := c.post(t, c.eve, `{"body":"reject me"}`)
	second := c.post(t, c.eve, `{"body":"reject me too"}`)
	if code := del(c.carl, first.ID); code != http.StatusForbidden {
		t.Errorf("stranger delete of a held comment = %d, want 403", code)
	}
	if code := del(c.bob, first.ID); code != http.StatusNoContent {
		t.Errorf("owner reject = %d, want 204", code)
	}
	if code := del(c.ed, second.ID); code != http.StatusNoContent {
		t.Errorf("editor reject = %d, want 204", code)
	}
	if got := c.list(t, c.bob); got.Total != 0 {
		t.Errorf("comments after rejecting both = %d, want 0", got.Total)
	}
	published := c.post(t, c.eve, `{"body":"approved, then kept"}`)
	if r := c.approve(c.bob, published.ID); r.code != http.StatusOK {
		t.Fatalf("approve = %d", r.code)
	}
	if code := del(c.bob, published.ID); code != http.StatusForbidden {
		t.Errorf("owner delete of a PUBLISHED comment = %d, want 403 (unchanged)", code)
	}
}

// managedCommentList is the GET /me/videos/comments page shape.
type managedCommentList struct {
	Comments []struct {
		commentView
		VideoTitle string `json:"video_title"`
	} `json:"comments"`
	Total int64 `json:"total"`
}

// TestCommentHeldNotifiesManagersAndFillsTheirQueue: a held comment pings the
// video's owner and editor (once each, never the commenter or a stranger), and
// sits in their review queue until approved.
func TestCommentHeldNotifiesManagersAndFillsTheirQueue(t *testing.T) {
	c := newApprovalCast(t)
	held := c.post(t, c.eve, `{"body":"from eve"}`)
	published := c.post(t, c.bob, `{"body":"from bob"}`)

	heldNotices := func(tok string) int {
		n := 0
		for _, nv := range notifTypes(t, c.srv, tok) {
			if nv.Type == "comment_held" && nv.CommentID == held.ID {
				n++
			}
		}
		return n
	}
	for name, want := range map[string]struct {
		tok string
		n   int
	}{"owner": {c.bob, 1}, "editor": {c.ed, 1}, "commenter": {c.eve, 0}, "stranger": {c.carl, 0}} {
		if got := heldNotices(want.tok); got != want.n {
			t.Errorf("%s comment_held notices = %d, want %d", name, got, want.n)
		}
	}

	queue := func(tok, query string) (int, managedCommentList) {
		t.Helper()
		rec := getWithAuth(c.srv, "/api/v1/me/videos/comments"+query, tok)
		var out managedCommentList
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	for name, tok := range map[string]string{"owner": c.bob, "editor": c.ed} {
		code, q := queue(tok, "?held=true")
		if code != http.StatusOK || q.Total != 1 || len(q.Comments) != 1 || q.Comments[0].ID != held.ID {
			t.Fatalf("%s held queue = %d %+v, want only eve's held comment", name, code, q)
		}
		if !q.Comments[0].HeldForReview || q.Comments[0].VideoTitle != "v" || q.Comments[0].AuthorUsername != "eve" {
			t.Errorf("%s queue entry = %+v", name, q.Comments[0])
		}
	}
	if _, q := queue(c.bob, ""); q.Total != 2 {
		t.Errorf("unfiltered queue total = %d, want 2", q.Total)
	}
	if _, q := queue(c.bob, "?held=false"); q.Total != 1 || q.Comments[0].ID != published.ID {
		t.Errorf("held=false queue = %+v, want only the published comment", q)
	}
	if _, q := queue(c.carl, "?held=true"); q.Total != 0 {
		t.Errorf("stranger queue total = %d, want 0", q.Total)
	}
	if code, _ := queue("", ""); code != http.StatusUnauthorized {
		t.Errorf("anonymous queue = %d, want 401", code)
	}
	if code, _ := queue(c.bob, "?held=maybe"); code != http.StatusUnprocessableEntity {
		t.Errorf("held=maybe = %d, want 422", code)
	}

	if r := c.approve(c.bob, held.ID); r.code != http.StatusOK {
		t.Fatalf("approve = %d; body=%s", r.code, r.body)
	}
	if _, q := queue(c.bob, "?held=true"); q.Total != 0 {
		t.Errorf("held queue after approval = %d, want 0", q.Total)
	}
}

// TestEditsOnApprovalVideoReholdForNonReviewers: on a requires_approval video
// an approved comment whose author is not a reviewer goes back to held when
// its body changes (and leaves public view); an unchanged body does not, and a
// reviewer's own edit never does.
func TestEditsOnApprovalVideoReholdForNonReviewers(t *testing.T) {
	c := newApprovalCast(t)
	edit := func(tok, id, body string) commentView {
		t.Helper()
		rec := sendJSONAuth(c.srv, http.MethodPatch, "/api/v1/comments/"+id, `{"body":"`+body+`"}`, tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("edit = %d; body=%s", rec.Code, rec.Body.String())
		}
		var cv commentView
		_ = json.Unmarshal(rec.Body.Bytes(), &cv)
		return cv
	}
	eve := c.post(t, c.eve, `{"body":"approved"}`)
	if r := c.approve(c.bob, eve.ID); r.code != http.StatusOK {
		t.Fatalf("approve = %d", r.code)
	}
	if edit(c.eve, eve.ID, "approved").HeldForReview {
		t.Error("an unchanged body re-held the comment")
	}
	if !edit(c.eve, eve.ID, "changed after approval").HeldForReview {
		t.Fatal("a non-reviewer's edit of an approved comment stayed published")
	}
	if _, ok := hasComment(c.list(t, ""), eve.ID); ok {
		t.Error("the re-held comment is still visible to anonymous readers")
	}
	for name, tok := range map[string]string{"owner": c.bob, "editor": c.ed, "moderator": c.mia} {
		own := c.post(t, tok, `{"body":"mine"}`)
		if edit(tok, own.ID, "mine, edited").HeldForReview {
			t.Errorf("%s's own edit was held", name)
		}
	}
}

// TestApprovalOnANonPublicVideoNotifiesNoThirdParty: a comment approved after
// its video stopped being public must not tell the parent's author (a third
// party) about a video they can no longer see.
func TestApprovalOnANonPublicVideoNotifiesNoThirdParty(t *testing.T) {
	c := newApprovalCast(t)
	parent := c.post(t, c.ed, `{"body":"editor says hi"}`)
	reply := c.post(t, c.eve, `{"body":"eve answers","parent_id":"`+parent.ID+`"}`)
	if rec := sendJSONAuth(c.srv, http.MethodPatch, "/api/v1/videos/"+c.vid, `{"privacy":"private"}`, c.bob); rec.Code != http.StatusOK {
		t.Fatalf("make private = %d; body=%s", rec.Code, rec.Body.String())
	}
	if r := c.approve(c.bob, reply.ID); r.code != http.StatusOK {
		t.Fatalf("approve = %d; body=%s", r.code, r.body)
	}
	for _, nv := range notifTypes(t, c.srv, c.ed) {
		// (comment_held reaches the editor as a reviewer; the reply notice must not.)
		if nv.CommentID == reply.ID && nv.Type == "comment_reply" {
			t.Errorf("parent author notified (%s) about a reply on a private video", nv.Type)
		}
	}
}

// TestRejectIsAuditedAndAHeldParentLooksUnknown: a reviewer's delete of a held
// comment is audited like an approval (the author withdrawing their own is
// not), and replying to a held parent answers exactly like an unknown parent.
func TestRejectIsAuditedAndAHeldParentLooksUnknown(t *testing.T) {
	c := newApprovalCast(t)
	reply := func(parentID string) string {
		rec := sendJSONAuth(c.srv, http.MethodPost, "/api/v1/videos/"+c.vid+"/comments",
			`{"body":"re","parent_id":"`+parentID+`"}`, c.carl)
		var e struct {
			Error struct {
				Fields []FieldError `json:"fields"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		return strconv.Itoa(rec.Code) + " " + fmt.Sprint(e.Error.Fields)
	}
	held := c.post(t, c.eve, `{"body":"reject me"}`)
	if got, want := reply(held.ID), reply("00000000-0000-0000-0000-000000000009"); got != want {
		t.Errorf("held parent answers %q, unknown parent %q — want identical", got, want)
	}
	withdrawn := c.post(t, c.eve, `{"body":"never mind"}`)
	for _, del := range []struct{ tok, id string }{{c.bob, held.ID}, {c.eve, withdrawn.ID}} {
		if rec := sendJSONAuth(c.srv, http.MethodDelete, "/api/v1/comments/"+del.id, "", del.tok); rec.Code != http.StatusNoContent {
			t.Fatalf("delete = %d", rec.Code)
		}
	}
	var rows []sqlcgen.ListAuditLogRow
	for _, r := range c.ledger.rows {
		if r.Action == observability.ActionCommentReject {
			rows = append(rows, r)
		}
	}
	if len(rows) != 1 || rows[0].ResourceType != "comment" || rows[0].ResourceID != held.ID {
		t.Errorf("reject audit rows = %+v, want exactly the reviewer's reject of %s", rows, held.ID)
	}
}

// TestReheldEditResurfacesTheReviewersNotice: when an approved comment is
// re-held by its author's edit, the reviewers' comment_held notice for it
// comes back unread — one row per reviewer and comment, never a pile.
func TestReheldEditResurfacesTheReviewersNotice(t *testing.T) {
	c := newApprovalCast(t)
	eve := c.post(t, c.eve, `{"body":"first"}`)
	if rec := sendJSONAuth(c.srv, http.MethodPost, "/api/v1/me/notifications/read-all", "", c.bob); rec.Code != http.StatusNoContent {
		t.Fatalf("read-all = %d", rec.Code)
	}
	if r := c.approve(c.bob, eve.ID); r.code != http.StatusOK {
		t.Fatalf("approve = %d", r.code)
	}
	if rec := sendJSONAuth(c.srv, http.MethodPatch, "/api/v1/comments/"+eve.ID, `{"body":"changed"}`, c.eve); rec.Code != http.StatusOK {
		t.Fatalf("edit = %d; body=%s", rec.Code, rec.Body.String())
	}
	var held []notificationView
	for _, nv := range notifTypes(t, c.srv, c.bob) {
		if nv.Type == "comment_held" && nv.CommentID == eve.ID {
			held = append(held, nv)
		}
	}
	if len(held) != 1 || held[0].Read {
		t.Errorf("owner's comment_held notices for the re-held comment = %+v, want one, unread", held)
	}
}

// TestApproveRequiresTheReviewedVersion: approval names the version the
// reviewer saw (updated_at); an edit that landed since answers 409 and
// publishes nothing, a missing version is a 422, and the current one approves.
func TestApproveRequiresTheReviewedVersion(t *testing.T) {
	c := newApprovalCast(t)
	held := c.post(t, c.eve, `{"body":"as reviewed"}`)
	seen := held.UpdatedAt.Format(time.RFC3339Nano)
	time.Sleep(2 * time.Millisecond) // the edit below must move updated_at
	if rec := sendJSONAuth(c.srv, http.MethodPatch, "/api/v1/comments/"+held.ID, `{"body":"swapped after review"}`, c.eve); rec.Code != http.StatusOK {
		t.Fatalf("edit = %d", rec.Code)
	}
	if r := c.approveAt(c.bob, held.ID, `{"updated_at":"`+seen+`"}`); r.code != http.StatusConflict {
		t.Errorf("approve of a stale version = %d, want 409; body=%s", r.code, r.body)
	}
	if _, ok := hasComment(c.list(t, ""), held.ID); ok {
		t.Error("a stale approval published the comment")
	}
	if r := c.approveAt(c.bob, held.ID, ``); r.code != http.StatusUnprocessableEntity {
		t.Errorf("approve without a version = %d, want 422", r.code)
	}
	if r := c.approve(c.bob, held.ID); r.code != http.StatusOK {
		t.Errorf("approve of the current version = %d, want 200; body=%s", r.code, r.body)
	}
}

// TestEditFailsClosedWhenTheVideoLookupFails: if the edit path cannot tell
// whether the comment's video requires approval, it refuses rather than
// publishing the new body unreviewed.
func TestEditFailsClosedWhenTheVideoLookupFails(t *testing.T) {
	c := newApprovalCast(t)
	cv := c.post(t, c.eve, `{"body":"approved"}`)
	if r := c.approve(c.bob, cv.ID); r.code != http.StatusOK {
		t.Fatalf("approve = %d", r.code)
	}
	vid := uuid.MustParse(c.vid)
	saved := c.videos.videos[vid]
	delete(c.videos.videos, vid) // the lookup now fails
	rec := sendJSONAuth(c.srv, http.MethodPatch, "/api/v1/comments/"+cv.ID, `{"body":"unreviewed"}`, c.eve)
	c.videos.videos[vid] = saved
	if rec.Code < 500 {
		t.Errorf("edit with a failed video lookup = %d, want 5xx", rec.Code)
	}
	if got, _ := c.srv.commentsvc.Get(context.Background(), uuid.MustParse(cv.ID)); got.Body != "approved" {
		t.Errorf("body after the refused edit = %q, want it unchanged", got.Body)
	}
}
