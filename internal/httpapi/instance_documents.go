package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/instancedocs"
	"github.com/vidra/vidra-core/internal/observability"
)

// instanceDocumentView is the admin projection of one instance document: the
// raw body plus its content hash ("" when the document is unset).
type instanceDocumentView struct {
	Name string `json:"name"`
	Body string `json:"body"`
	Hash string `json:"hash"`
}

// instanceHomepageDocResponse is the public homepage payload: raw markdown +
// the content hash (the client renders/sanitises).
type instanceHomepageDocResponse struct {
	Body string `json:"body"`
	Hash string `json:"hash"`
}

// handleGetInstanceDocumentAdmin returns one instance document's stored state
// (empty body/hash when never set — a stable shape for the admin editor).
// Behind requireRole(admin). Unknown names are 404.
func (s *Server) handleGetInstanceDocumentAdmin(c echo.Context) error {
	name := c.Param("name")
	if !instancedocs.IsName(name) {
		return echo.NewHTTPError(http.StatusNotFound, "unknown instance document")
	}
	doc, _ := s.instancedocssvc.Get(name)
	return c.JSON(http.StatusOK, instanceDocumentView{Name: name, Body: doc.Body, Hash: doc.SHA256})
}

// handlePutInstanceDocument stores an instance document (homepage 100KB cap,
// custom CSS/JS 200KB) or clears it with an empty body. Behind
// requireRole(admin); every write is audit-enveloped with the document name +
// new content hash (never the body — custom JS/CSS is operator code that runs
// in every visitor's browser, so changes must be traceable).
func (s *Server) handlePutInstanceDocument(c echo.Context) error {
	callerID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	name := c.Param("name")
	if !instancedocs.IsName(name) {
		return echo.NewHTTPError(http.StatusNotFound, "unknown instance document")
	}
	var req struct {
		Body *string `json:"body"`
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil || req.Body == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "malformed or invalid request body")
	}
	doc, err := s.instancedocssvc.Set(c.Request().Context(), name, *req.Body, callerID)
	if err != nil {
		var ve *instancedocs.ValidationError
		if errors.As(err, &ve) {
			s.audit(c, observability.ActionAdminInstanceDocumentUpdate, observability.ResultFailure, callerID.String(), "invalid:"+name)
			return &ValidationError{Fields: []FieldError{{Field: "body", Message: ve.Message}}}
		}
		return err
	}
	reason := "name=" + name
	if doc.SHA256 != "" {
		reason += ",sha256=" + doc.SHA256
	} else {
		reason += ",cleared"
	}
	s.audit(c, observability.ActionAdminInstanceDocumentUpdate, observability.ResultSuccess, callerID.String(), reason)
	return c.JSON(http.StatusOK, instanceDocumentView{Name: name, Body: doc.Body, Hash: doc.SHA256})
}

// handleGetInstanceHomepage serves the admin-authored homepage document as raw
// markdown JSON. Public; 404 while no homepage is set.
func (s *Server) handleGetInstanceHomepage(c echo.Context) error {
	doc, ok := s.instancedocssvc.Get(instancedocs.NameHomepage)
	if !ok || doc.Body == "" {
		return echo.NewHTTPError(http.StatusNotFound, "homepage not set")
	}
	return c.JSON(http.StatusOK, instanceHomepageDocResponse{Body: doc.Body, Hash: doc.SHA256})
}

// handleGetInstanceCustomCSS serves the operator's custom stylesheet as a
// same-origin text/css file. Public; 404 while unset. Consumed hash-busted
// (<link href="/instance/custom.css?v={css_hash}">), so responses carry the
// content hash as a strong ETag.
func (s *Server) handleGetInstanceCustomCSS(c echo.Context) error {
	return s.serveInstanceDocumentRaw(c, instancedocs.NameCustomCSS, "text/css; charset=utf-8")
}

// handleGetInstanceCustomJS serves the operator's custom script as a
// same-origin application/javascript file (external-file delivery keeps a
// future script-src 'self' CSP viable — architecture note 6). Public; 404
// while unset.
func (s *Server) handleGetInstanceCustomJS(c echo.Context) error {
	return s.serveInstanceDocumentRaw(c, instancedocs.NameCustomJS, "application/javascript; charset=utf-8")
}

// serveInstanceDocumentRaw streams a document body with content-type
// discipline, a strong content-hash ETag (with If-None-Match short-circuit),
// and a modest shared-cache lifetime — the ?v={hash} query param from GET
// /instance is the real cache buster.
func (s *Server) serveInstanceDocumentRaw(c echo.Context, name, contentType string) error {
	doc, ok := s.instancedocssvc.Get(name)
	if !ok || doc.Body == "" {
		return echo.NewHTTPError(http.StatusNotFound, "document not set")
	}
	etag := `"` + doc.SHA256 + `"`
	h := c.Response().Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", "public, max-age=300")
	if match := c.Request().Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		return c.NoContent(http.StatusNotModified)
	}
	return c.Blob(http.StatusOK, contentType, []byte(doc.Body))
}
