package httpapi

import (
	"errors"
	"io"
	"log/slog"
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
//
// The malware scan is intentionally NOT applied here: the zip's bytes are
// parsed in memory and discarded, never stored or served. Any change that
// persists the archive (or any file inside it) must add requireScannerGate and
// scanBeforeStore, as POST /me/import does.
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
		var he *echo.HTTPError
		if errors.As(err, &he) && he.Code == http.StatusRequestEntityTooLarge {
			return he
		}
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
		// Rows created before the failure stay (the import is additive), so the
		// caller is told what was done rather than left to guess.
		slog.ErrorContext(c.Request().Context(), "peertube archive import failed part-way", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, map[string]any{
			"error":   map[string]string{"code": "internal_error", "message": "the import stopped part-way"},
			"summary": summary,
		})
	}
	return c.JSON(http.StatusOK, summary)
}
