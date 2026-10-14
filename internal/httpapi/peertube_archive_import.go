package httpapi

import (
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/peertubearchive"
)

// WithPeerTubeArchiveImporter mounts POST /api/v1/me/import/peertube.
func WithPeerTubeArchiveImporter(im *peertubearchive.Importer) Option {
	return func(s *Server) { s.peertubeArchive = im }
}

// handlePeerTubeArchiveImport imports the METADATA of a PeerTube user-export
// zip (multipart field "archive") into the caller's own account: channels,
// playlists and the settings that map 1:1. It runs synchronously because the
// work is bounded — a capped number of rows through the existing services, no
// network and no media — and answers with what was created, what already
// existed, and what was skipped. The zip is parsed in memory and never
// extracted; the body is bounded by the global HTTP body limit (default 8 MB),
// so an export that bundles video files must be trimmed to its peertube/*.json
// entries first.
func (s *Server) handlePeerTubeArchiveImport(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if !s.userImportEnabled() {
		return &FeatureDisabledError{Feature: "user_import"}
	}
	fh, err := c.FormFile("archive")
	if err != nil {
		return &ValidationError{Fields: []FieldError{{Field: "archive", Message: "a zip file is required (multipart field \"archive\")"}}}
	}
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, peertubearchive.MaxUploadBytes+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > peertubearchive.MaxUploadBytes {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "the archive is too large")
	}
	archive, err := peertubearchive.Read(raw)
	if err != nil {
		if errors.Is(err, peertubearchive.ErrInvalid) {
			// The reason is deliberately not echoed: it names attacker-chosen content.
			return &ValidationError{Fields: []FieldError{{Field: "archive", Message: "not a valid PeerTube user-export archive"}}}
		}
		return err
	}
	summary, err := s.peertubeArchive.Import(c.Request().Context(), userID, archive)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, summary)
}
