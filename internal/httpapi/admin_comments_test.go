package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// adminCommentsBody parses GET /admin/comments.
type adminCommentsBody struct {
	Comments []struct {
		ID             string `json:"id"`
		VideoTitle     string `json:"video_title"`
		Body           string `json:"body"`
		AuthorUsername string `json:"author_username"`
	} `json:"comments"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func parseAdminComments(t *testing.T, rec *httptest.ResponseRecorder) adminCommentsBody {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("admin comments = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body adminCommentsBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body
}

func TestAdminCommentsOverview(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	charlie := registerAndToken(t, srv, `{"username":"charlie","email":"charlie@example.test","password":"supersecret"}`)

	for _, c := range []struct{ tok, body string }{{bob, "hello from bob"}, {charlie, "spam from charlie"}} {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"`+c.body+`"}`, c.tok); rec.Code != http.StatusCreated {
			t.Fatalf("comment = %d; body=%s", rec.Code, rec.Body.String())
		}
	}

	// The admin sees all comments with author + video context.
	body := parseAdminComments(t, getWithAuth(srv, "/api/v1/admin/comments", admin))
	if len(body.Comments) != 2 {
		t.Fatalf("admin comments = %d, want 2; body=%+v", len(body.Comments), body)
	}
	for _, c := range body.Comments {
		if c.VideoTitle != "Clip" {
			t.Errorf("comment video_title = %q, want Clip", c.VideoTitle)
		}
	}

	// The q filter matches on body.
	if got := parseAdminComments(t, getWithAuth(srv, "/api/v1/admin/comments?q=spam", admin)); len(got.Comments) != 1 || got.Comments[0].AuthorUsername != "charlie" {
		t.Errorf("q=spam = %+v, want only charlie's", got.Comments)
	}

	// Regular users are forbidden; anonymous is unauthorized.
	if rec := getWithAuth(srv, "/api/v1/admin/comments", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-mod = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/comments", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon = %d, want 401", rec.Code)
	}
}

func TestModeratorDeletesAnyComment(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	crec := postJSONAuth(srv, "/api/v1/videos/"+vid+"/comments", `{"body":"bad comment"}`, bob)
	if crec.Code != http.StatusCreated {
		t.Fatalf("comment = %d; body=%s", crec.Code, crec.Body.String())
	}
	var cv commentView
	_ = json.Unmarshal(crec.Body.Bytes(), &cv)

	// A regular non-author still cannot delete it.
	other := registerAndToken(t, srv, `{"username":"eve","email":"eve@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/comments/"+cv.ID, "", other); rec.Code != http.StatusForbidden {
		t.Errorf("non-author non-mod delete = %d, want 403", rec.Code)
	}

	// The admin (moderator/admin) deletes bob's comment.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/comments/"+cv.ID, "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := parseAdminComments(t, getWithAuth(srv, "/api/v1/admin/comments", admin)); len(got.Comments) != 0 {
		t.Errorf("comments after admin delete = %d, want 0", len(got.Comments))
	}
}

func TestBulkRemoveCommentsOf(t *testing.T) {
	srv := videoServer(t)
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))
	ledger := &httpAuditFakeRepo{}
	srv.auditLog = audit.NewService(ledger)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	spam := registerAndToken(t, srv, `{"username":"spammer","email":"spam@example.test","password":"supersecret"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	for _, c := range []struct{ tok, body string }{{spam, "buy pills"}, {spam, "buy more pills"}, {bob, "nice clip"}} {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"`+c.body+`"}`, c.tok); rec.Code != http.StatusCreated {
			t.Fatalf("comment = %d; body=%s", rec.Code, rec.Body.String())
		}
	}
	const path = "/api/v1/admin/comments/bulk-remove"

	// Role gate: users 403, anonymous 401, and nothing is removed by a refusal.
	if rec := sendJSONAuth(srv, http.MethodPost, path, `{"account":"spammer"}`, bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-mod = %d, want 403", rec.Code)
	}
	// The real removal below runs as a MODERATOR (not only an admin).
	modTok, modID := registerAndUser(t, srv, `{"username":"mia","email":"mia@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+modID, `{"role":"moderator"}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("promote moderator = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPost, path, `{"account":"spammer"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon = %d, want 401", rec.Code)
	}
	if got := parseAdminComments(t, getWithAuth(srv, "/api/v1/admin/comments", admin)); len(got.Comments) != 3 {
		t.Fatalf("comments after refusals = %d, want 3", len(got.Comments))
	}

	// Validation and unknown account.
	if rec := sendJSONAuth(srv, http.MethodPost, path, `{"account":"  "}`, admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("blank account = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPost, path, `{"account":"ghost"}`, admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown account = %d, want 404", rec.Code)
	}

	rec := sendJSONAuth(srv, http.MethodPost, path, `{"account":"Spammer"}`, modTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk remove = %d; body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Removed int `json:"removed"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Removed != 2 {
		t.Errorf("removed = %d, want 2", out.Removed)
	}
	got := parseAdminComments(t, getWithAuth(srv, "/api/v1/admin/comments", admin))
	if len(got.Comments) != 1 || got.Comments[0].AuthorUsername != "bob" {
		t.Errorf("remaining = %+v, want only bob's", got.Comments)
	}

	// One audit row: actor, target account id and count; no comment bodies.
	var rows []sqlcgen.ListAuditLogRow
	for _, r := range ledger.rows {
		if r.Action == observability.ActionAdminCommentBulkRemove {
			rows = append(rows, r)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("bulk_remove audit rows = %d, want 1 (refusals must not audit)", len(rows))
	}
	row := rows[0]
	if row.Result != observability.ResultSuccess || row.ResourceType != "account" || row.ResourceID == "" {
		t.Errorf("audit row = %+v", row)
	}
	if !strings.Contains(string(row.Metadata), `"count"`) || !strings.Contains(string(row.Metadata), "2") {
		t.Errorf("metadata = %s, want count 2", row.Metadata)
	}
	if all := string(row.Metadata) + row.Reason + buf.String(); strings.Contains(all, "pills") {
		t.Error("a comment body reached the audit trail")
	}
}
