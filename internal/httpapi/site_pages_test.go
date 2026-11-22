package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type pageReaderFixture struct{ menuOnly bool }

func (f *pageReaderFixture) ListSitePages(_ context.Context, menuOnly bool) ([]sqlcgen.ListSitePagesRow, error) {
	f.menuOnly = menuOnly
	return nil, nil
}
func (*pageReaderFixture) GetSitePage(_ context.Context, slug string) (sqlcgen.SitePage, error) {
	if slug == "privacy" {
		return sqlcgen.SitePage{Slug: slug, Title: "Privacy", Body: "## Policy"}, nil
	}
	return sqlcgen.SitePage{}, pgx.ErrNoRows
}

func TestPublicSitePageRoutes(t *testing.T) {
	repo := &pageReaderFixture{}
	server := New(testConfig(), nil, nil, WithSitePages(repo))
	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{
		{"/pages", 200, "[]"},
		{"/pages/privacy", 200, "## Policy"},
		{"/pages/missing", 404, ""},
		{"/admin/pages", 401, ""},
	} {
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1"+tc.path, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	if !repo.menuOnly {
		t.Fatal("public navigation must query only menu pages")
	}
}
