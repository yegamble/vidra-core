package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

var sitePageSlug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type sitePageInput struct {
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	MenuLabel string     `json:"menu_label"`
	InMenu    bool       `json:"in_menu"`
	MenuOrder int32      `json:"menu_order"`
	Revision  *uuid.UUID `json:"revision"`
}

func (s *Server) saveSitePage(c *echo.Context) error {
	caller, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in sitePageInput
	// A JSON escape can occupy six bytes per byte of Markdown.
	if err := decodeLimitedAdminDocument(c, &in, 640<<10); err != nil {
		return err
	}
	slug := c.Param("slug")
	in.Title = strings.TrimSpace(in.Title)
	in.MenuLabel = strings.TrimSpace(in.MenuLabel)
	var fields []FieldError
	if len(slug) > 80 || !sitePageSlug.MatchString(slug) {
		fields = append(fields, FieldError{Field: "slug", Message: "use up to 80 lowercase letters, digits and single hyphens"})
	}
	if in.Title == "" || utf8.RuneCountInString(in.Title) > 150 {
		fields = append(fields, FieldError{Field: "title", Message: "enter a title of up to 150 characters"})
	}
	if strings.TrimSpace(in.Body) == "" || len(in.Body) > 100<<10 {
		fields = append(fields, FieldError{Field: "body", Message: "enter Markdown of up to 100 KiB"})
	}
	if utf8.RuneCountInString(in.MenuLabel) > 150 {
		fields = append(fields, FieldError{Field: "menu_label", Message: "use up to 150 characters"})
	}
	if in.MenuOrder < 0 {
		fields = append(fields, FieldError{Field: "menu_order", Message: "use a nonnegative integer"})
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	var page sqlcgen.SitePage
	if in.Revision == nil {
		page, err = s.sitePages.CreateSitePage(c.Request().Context(), sqlcgen.CreateSitePageParams{Slug: slug, Title: in.Title, Body: in.Body, MenuLabel: in.MenuLabel, InMenu: in.InMenu, MenuOrder: in.MenuOrder})
	} else {
		page, err = s.sitePages.UpdateSitePage(c.Request().Context(), sqlcgen.UpdateSitePageParams{Slug: slug, Revision: *in.Revision, Title: in.Title, Body: in.Body, MenuLabel: in.MenuLabel, InMenu: in.InMenu, MenuOrder: in.MenuOrder})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusConflict, "page changed; reload before saving")
	}
	if err != nil {
		return err
	}
	s.audit(c, observability.ActionAdminSitePageSave, observability.ResultSuccess, caller.String(), slug)
	return c.JSON(http.StatusOK, page)
}

func (s *Server) deleteSitePage(c *echo.Context) error {
	caller, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	revision, err := uuid.Parse(c.QueryParam("revision"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "page revision required")
	}
	slug := c.Param("slug")
	n, err := s.sitePages.DeleteSitePage(c.Request().Context(), sqlcgen.DeleteSitePageParams{Slug: slug, Revision: revision})
	if err != nil {
		return err
	}
	if n == 0 {
		return echo.NewHTTPError(http.StatusConflict, "page changed; reload before deleting")
	}
	s.audit(c, observability.ActionAdminSitePageDelete, observability.ResultSuccess, caller.String(), slug)
	return c.NoContent(http.StatusNoContent)
}
