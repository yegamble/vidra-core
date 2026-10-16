package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/peertubearchive"
	"github.com/vidra/vidra-core/internal/peertubefollows"
)

// WithPeerTubeArchiveImporter mounts POST /api/v1/me/import/peertube.
func WithPeerTubeArchiveImporter(im *peertubearchive.Importer) Option {
	return func(s *Server) {
		s.peertubeArchive = im
		if im.ScanImage == nil {
			// The scan policy of the avatar/banner routes, applied lazily so the
			// scanner option may be given in any order. Stored bytes are the one
			// thing this import persists from the archive, so they are scanned
			// exactly as an upload would be; no ready scanner, no stored image.
			im.ScanImage = func(ctx context.Context, kind string, data []byte) error {
				if !s.scannerReadyForIngestion() {
					return errors.New("scanner not ready")
				}
				return s.scanBeforeStore(ctx, kind, data)
			}
		}
	}
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
// MALWARE SCAN: the archive as a whole is deliberately NOT scanned. Its bytes
// touch local disk only transiently under the spool dir, and the only bytes
// decoded are the capped JSON entries, parsed by Go's own decoder; scanning the
// whole cap would mean streaming it through the scanner (clamd rejects streams
// of that size), for no reachable victim. The exception is the channel
// avatar/banner images, the one thing persisted from inside it: each is read
// through an 8 MiB cap, sniffed and bounds-checked, and passed through the same
// scan policy as POST /channels/{handle}/avatar (ScanImage); with no ready
// scanner it is not stored. Any change that persists anything else from the
// archive (e.g. the bundled videos) must add requireScannerGate and
// scanBeforeStore, as POST /me/import does.
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

// WithPeerTubeFollowStatus mounts GET /api/v1/me/import/peertube/follows/{id}.
func WithPeerTubeFollowStatus(svc *peertubefollows.Service) Option {
	return func(s *Server) { s.peertubeFollows = svc }
}

// handlePeerTubeFollowStatus reports on a background follow import the caller
// started. Another user's id and an unknown id are the same 404.
func (s *Server) handlePeerTubeFollowStatus(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "follow import not found")
	if err != nil {
		return err
	}
	st, err := s.peertubeFollows.Status(c.Request().Context(), userID, id)
	if errors.Is(err, peertubefollows.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "follow import not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, st)
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
