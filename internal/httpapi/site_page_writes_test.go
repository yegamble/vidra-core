package httpapi

import (
	"bytes"
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"log/slog"
	"testing"
)

func (*pageReaderFixture) CreateSitePage(_ context.Context, a sqlcgen.CreateSitePageParams) (sqlcgen.SitePage, error) {
	return sqlcgen.SitePage{Slug: a.Slug, Title: a.Title, Body: a.Body, Revision: uuid.New()}, nil
}
func (*pageReaderFixture) UpdateSitePage(context.Context, sqlcgen.UpdateSitePageParams) (sqlcgen.SitePage, error) {
	return sqlcgen.SitePage{}, pgx.ErrNoRows
}
func (*pageReaderFixture) DeleteSitePage(context.Context, sqlcgen.DeleteSitePageParams) (int64, error) {
	return 0, nil
}

func TestAdminSitePageWrites(t *testing.T) {
	srv := instanceConfigServer(t)
	admin := registerAndToken(t, srv, `{"username":"pagesadmin","email":"pagesadmin@example.test","password":"supersecret"}`)
	user := registerAndToken(t, srv, `{"username":"pagesviewer","email":"pagesviewer@example.test","password":"supersecret"}`)
	var logs bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	path := "/api/v1/admin/pages/privacy"
	valid := `{"title":"Title","body":"Private test fixture Markdown","in_menu":true}`
	if w := sendJSONAuth(srv, "PUT", path, valid, user); w.Code != 403 {
		t.Fatalf("viewer write = %d", w.Code)
	}
	if w := sendJSONAuth(srv, "PUT", path, `{"title":"Title","body":""}`, admin); w.Code != 422 {
		t.Fatalf("empty body = %d", w.Code)
	}
	if w := sendJSONAuth(srv, "PUT", path, valid, admin); w.Code != 200 {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	if w := sendJSONAuth(srv, "PUT", path, `{"title":"Title","body":"edit","revision":"52f1cb68-ce90-4885-adee-51d05c42db94"}`, admin); w.Code != 409 {
		t.Fatalf("stale edit = %d", w.Code)
	}
	if w := sendJSONAuth(srv, "DELETE", path, "", admin); w.Code != 400 {
		t.Fatalf("unconditional delete = %d", w.Code)
	}
	if ev := findAudit(auditEvents(t, &logs), observability.ActionAdminSitePageSave, observability.ResultSuccess); ev == nil || bytes.Contains(logs.Bytes(), []byte("Private test fixture Markdown")) {
		t.Fatal("missing save audit or leaked body")
	}
}
