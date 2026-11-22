package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
	"github.com/vidra/vidra-core/internal/admin"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type sitePageRepository interface {
	ListSitePages(context.Context, bool) ([]sqlcgen.ListSitePagesRow, error)
	GetSitePage(context.Context, string) (sqlcgen.SitePage, error)
}

func WithSitePages(repo sitePageRepository) Option { return func(s *Server) { s.sitePages = repo } }

func (s *Server) registerSitePages(api *echo.Group) {
	if s.sitePages == nil {
		return
	}
	api.GET("/pages", s.listSitePages(true))
	api.GET("/pages/:slug", s.getSitePage)
	api.GET("/admin/pages", s.listSitePages(false), s.requireAuth, s.requireRole(admin.RoleAdmin))
}

func (s *Server) listSitePages(menuOnly bool) echo.HandlerFunc {
	return func(c *echo.Context) error {
		rows, err := s.sitePages.ListSitePages(c.Request().Context(), menuOnly)
		if err != nil {
			return err
		}
		if rows == nil {
			rows = []sqlcgen.ListSitePagesRow{}
		}
		return c.JSON(http.StatusOK, rows)
	}
}

func (s *Server) getSitePage(c *echo.Context) error {
	page, err := s.sitePages.GetSitePage(c.Request().Context(), c.Param("slug"))
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "page not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, page)
}
