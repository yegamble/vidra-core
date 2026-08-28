package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/video"
)

// chapterView is one seek-bar chapter in the API: a whole-second start offset and
// a short title (CORE-15 / W1.C1). It is both a response row and a request row.
type chapterView struct {
	StartSeconds int32  `json:"start_seconds"`
	Title        string `json:"title"`
}

// chaptersRequest is the PUT /videos/{id}/chapters body: the whole chapter set,
// which replaces any existing set atomically (an empty or omitted array clears
// all chapters). It intentionally does NOT implement Validatable — the semantic
// rules (ordering, bounds vs duration, title length, count cap) live in the
// video service and surface as 400, per the contract.
type chaptersRequest struct {
	Chapters []chapterView `json:"chapters"`
}

// chaptersResponse is the GET/PUT /videos/{id}/chapters response: the stored
// chapters in playback order (ascending start), [] when none.
type chaptersResponse struct {
	Chapters []chapterView `json:"chapters"`
}

// chapterViews projects domain chapters to the API rows (never nil, so the JSON
// is always {"chapters": []} rather than {"chapters": null}).
func chapterViews(chapters []video.Chapter) []chapterView {
	out := make([]chapterView, 0, len(chapters))
	for _, ch := range chapters {
		out = append(out, chapterView{StartSeconds: ch.StartSeconds, Title: ch.Title})
	}
	return out
}

// handleGetVideoChapters returns a video's chapters. Runs behind optionalAuth;
// visibility is identical to GET /videos/{id} (private → owner only, blocked →
// moderators, scheduled/quarantined gated, else 404). 200 with a sorted list
// ({"chapters": []} when none); 404 when the video is not visible.
func (s *Server) handleGetVideoChapters(c echo.Context) error {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	if _, err := s.videoVisibleForRead(c, id); err != nil {
		return err
	}
	chapters, err := s.videosvc.ListChapters(c.Request().Context(), id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, chaptersResponse{Chapters: chapterViews(chapters)})
}

// handleSetVideoChapters replaces a video's whole chapter set. Behind
// requireAuth; owner only (a non-owner or unknown id → 404, matching the
// thumbnail POST convention). The set is validated in full before any write, so
// a rejected request leaves the stored chapters untouched:
//   - 400 on non-ascending/duplicate starts, a start < 0, a start >= the probed
//     duration, a title not 1..120 chars after trim, or more than 100 chapters;
//   - 200 with the stored set (GET shape) on success.
func (s *Server) handleSetVideoChapters(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	var in chaptersRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	chapters := make([]video.Chapter, 0, len(in.Chapters))
	for _, ch := range in.Chapters {
		chapters = append(chapters, video.Chapter{StartSeconds: ch.StartSeconds, Title: ch.Title})
	}
	// Owner OR editor collaborator (migration 0097).
	v, canManage := s.canManageVideo(c.Request().Context(), userID, id)
	if !canManage {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	stored, err := s.videosvc.ReplaceChapters(c.Request().Context(), v.OwnerID, id, chapters)
	if err != nil {
		var ve *video.ChapterValidationError
		if errors.As(err, &ve) {
			return echo.NewHTTPError(http.StatusBadRequest, ve.Message)
		}
		return videoError(err) // ErrNotFound / ErrForbidden → 404
	}
	return c.JSON(http.StatusOK, chaptersResponse{Chapters: chapterViews(stored)})
}
