package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"

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
// existed, and what was skipped.
//
// A real export bundles the video files (multi-GB). The body cap is
// PEERTUBE_IMPORT_MAX_SIZE, which defaults to the global HTTP_BODY_LIMIT; an
// operator who wants full exports raises it deliberately. The file part is
// STREAMED to a temp file under PEERTUBE_IMPORT_TMPDIR (never held in memory;
// see spoolPeerTubeArchive for admission and disk accounting), the zip is
// opened on that file with random access, and only the peertube/*.json entries
// are ever opened; media entries are validated by name and left unread. The
// temp file is removed on every path (success, refusal, client abort, panic).
//
// MALWARE SCAN: deliberately NOT applied, decision recorded here so a later
// change revisits it knowingly. The archive's bytes touch local disk, but only
// transiently and only under the spool dir: nothing is put in object
// storage, served, transcoded or executed, and the only bytes ever decoded are
// the capped JSON entries, parsed by Go's own decoder. Scanning would mean
// streaming up to the whole cap through the scanner (clamd rejects streams of
// that size), for no reachable victim. Any change that persists the archive or a
// file inside it (e.g. importing the bundled videos) must add requireScannerGate
// and scanBeforeStore, as POST /me/import does.
func (s *Server) handlePeerTubeArchiveImport(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if !s.userImportEnabled() {
		return &FeatureDisabledError{Feature: "user_import"}
	}
	tmp, size, err := s.spoolPeerTubeArchive(c)
	if err != nil {
		return err
	}
	// A panic below unwinds through this defer too; the file never outlives the request.
	defer func() { _ = os.Remove(tmp.Name()); _ = tmp.Close() }()
	if size == 0 {
		return peertubeArchiveInvalid()
	}
	archive, err := peertubearchive.ReadAt(tmp, size)
	if err != nil {
		if errors.Is(err, peertubearchive.ErrInvalid) {
			// The reason is deliberately not echoed: it names attacker-chosen content.
			return peertubeArchiveInvalid()
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

func peertubeArchiveInvalid() error {
	return &ValidationError{Fields: []FieldError{{Field: "archive", Message: "not a valid PeerTube user-export archive"}}}
}

// nextArchivePart advances to the file part named "archive", discarding a
// bounded number of small preceding form fields.
func nextArchivePart(mr *multipart.Reader) (*multipart.Part, error) {
	for i := 0; i < 8; i++ {
		p, err := mr.NextPart()
		if err != nil {
			var he *echo.HTTPError
			if errors.As(err, &he) {
				return nil, he
			}
			break
		}
		if p.FormName() == "archive" && p.FileName() != "" {
			return p, nil
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(p, 1<<20))
	}
	return nil, &ValidationError{Fields: []FieldError{{Field: "archive", Message: "a zip file is required (multipart field \"archive\")"}}}
}
