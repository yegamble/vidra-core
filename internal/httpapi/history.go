package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/searchevents"
)

// watchProgressRequest is the PUT /videos/{id}/watch-progress body: the viewer's
// current playback position in whole seconds.
type watchProgressRequest struct {
	PositionSeconds int32 `json:"position_seconds"`
}

func (r watchProgressRequest) Validate() []FieldError {
	if r.PositionSeconds < 0 {
		return []FieldError{{Field: "position_seconds", Message: "must be >= 0"}}
	}
	return nil
}

// watchProgressView is the caller's saved resume position for a video.
type watchProgressView struct {
	VideoID         string `json:"video_id"`
	PositionSeconds int32  `json:"position_seconds"`
}

// handleRecordWatchProgress records (upserts) the caller's resume position for a
// public, published video and bumps it to the top of their history. Behind
// requireAuth. A non-public/unpublished or unknown video is 404.
func (s *Server) handleRecordWatchProgress(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	videoID, err := s.publicVideoID(c)
	if err != nil {
		return err
	}
	var in watchProgressRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	// Per-user history preference (config-parity W7): while history_enabled is
	// off, nothing is written — no resume position, no history entry (matching
	// PeerTube's "do not record history"). Still 204: the client fires this
	// beacon-style and the off state is not an error.
	if u, err := s.authsvc.UserByID(c.Request().Context(), userID); err == nil && !u.HistoryEnabled {
		return c.NoContent(http.StatusNoContent)
	}
	if err := s.videosvc.RecordProgress(c.Request().Context(), videoID, userID, in.PositionSeconds); err != nil {
		return err
	}
	s.emitWatchProgress(c, videoID, userID, in.PositionSeconds)
	return c.NoContent(http.StatusNoContent)
}

// emitWatchProgress enqueues a throttled video.watch_progress event (search-
// service W4): at most one per (user,video) per 30s window, gated by the Redis
// SETNX seam. duration_seconds is omitted (not cheaply available on this path).
// No-op when the enqueuer or throttle is unwired.
//
// Identity follows the same rule as the two search-event paths
// (search_attribution.go): a caller who has switched all three discovery
// controls off is emitted unattributed, with the day-scoped subject instead of
// their account id. The THROTTLE key is not part of that — it keys core's own
// Redis, which already holds the watch-progress row itself under the same
// account, and collapsing it to the subject would let one NAT'd address
// suppress another viewer's events.
func (s *Server) emitWatchProgress(c echo.Context, videoID, userID uuid.UUID, positionSeconds int32) {
	if s.searchEvents == nil || s.searchWatchThrottle == nil {
		return
	}
	key := "search:wp:" + userID.String() + ":" + videoID.String()
	if !s.searchWatchThrottle(c.Request().Context(), key) {
		return
	}
	// Resolved AFTER the throttle so a suppressed beacon costs no preference read.
	ident := s.searchEventIdentity(c)
	wp := searchevents.WatchProgress{
		VideoID:         videoID,
		PositionSeconds: positionSeconds,
	}
	if ident.Attributed() {
		wp.UserID = &ident.UserID
	} else if ident.SubjectID != "" {
		wp.SubjectID = &ident.SubjectID
	}
	if ident.SessionID != "" {
		wp.SessionID = &ident.SessionID
	}
	s.searchEvents.EnqueueWatchProgress(c.Request().Context(), wp)
}

// handleGetWatchProgress returns the caller's saved resume position for a public,
// published video (0 when none recorded). Behind requireAuth.
func (s *Server) handleGetWatchProgress(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	videoID, err := s.publicVideoID(c)
	if err != nil {
		return err
	}
	pos, _, err := s.videosvc.Progress(c.Request().Context(), videoID, userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, watchProgressView{VideoID: videoID.String(), PositionSeconds: pos})
}

// historyItemView is a watch-history card: a video card plus the caller's resume
// position and the time they last watched it. videoView is embedded so its
// fields flatten into the same JSON object.
type historyItemView struct {
	videoView
	PositionSeconds int32     `json:"position_seconds"`
	WatchedAt       time.Time `json:"watched_at"`
}

// historyListResponse is the paginated watch-history list.
type historyListResponse struct {
	Videos []historyItemView `json:"videos"`
	pageMeta
}

// handleListHistory returns the caller's watch history as cards, most-recently
// watched first. Behind requireAuth. Pagination via ?limit (1–100, default 20)
// and ?offset. The optional ?progress=in_progress filter restricts the list to
// the "Continue watching" subset (started, not effectively finished); any other
// value (or absent) returns the full history.
func (s *Server) handleListHistory(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	inProgress := c.QueryParam("progress") == "in_progress"
	items, total, err := s.videosvc.ListHistory(c.Request().Context(), userID, page.Limit32(), page.Offset32(), inProgress)
	if err != nil {
		return err
	}
	views := make([]historyItemView, 0, len(items))
	for _, it := range items {
		views = append(views, historyItemView{
			videoView:       feedItemView(it.FeedItem),
			PositionSeconds: it.PositionSeconds,
			WatchedAt:       it.WatchedAt,
		})
	}
	return c.JSON(http.StatusOK, historyListResponse{Videos: views, pageMeta: page.meta(total)})
}

// handleDeleteHistoryEntry removes a single video from the caller's history
// (idempotent). Behind requireAuth. No public-video check, so a user can always
// clean up an entry.
func (s *Server) handleDeleteHistoryEntry(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	videoID, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	if err := s.videosvc.RemoveHistoryEntry(c.Request().Context(), videoID, userID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleClearHistory removes the caller's entire watch history (idempotent).
// Behind requireAuth.
func (s *Server) handleClearHistory(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if err := s.videosvc.ClearHistory(c.Request().Context(), userID); err != nil {
		return err
	}
	// Search: purge the user's derived watch projection (search-service W4).
	s.searchEvents.EnqueueUserHistoryDeleted(c.Request().Context(), userID, searchevents.HistoryScopeWatch)
	return c.NoContent(http.StatusNoContent)
}
