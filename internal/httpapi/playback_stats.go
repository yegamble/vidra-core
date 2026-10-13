package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/playbackstats"
	"github.com/vidra/vidra-core/internal/ratelimit"
)

// playbackStatsService is the seam to internal/playbackstats.
type playbackStatsService interface {
	Record(ctx context.Context, in playbackstats.Sample) error
	Report(ctx context.Context, videoID uuid.UUID, duration int, now time.Time) (playbackstats.Report, error)
}

// WithPlaybackStats mounts POST /videos/{id}/playback (the anonymous watch-time
// beacon) and GET /videos/{id}/playback-stats (owner/editor analytics). Without
// it neither route exists.
func WithPlaybackStats(svc playbackStatsService) Option {
	return func(s *Server) { s.playbackStats = svc }
}

// Beacon budget per client: a player samples every ~10 s, so 60/min allows
// several tabs while capping what one client can inflate. Keyed on a hash of
// the client IP, held only as a short-lived counter key, never persisted.
const (
	defaultPlaybackRateLimit  = 60
	defaultPlaybackRateWindow = time.Minute
	// maxPlaybackPosition bounds position_seconds (48 h) before duration checks.
	maxPlaybackPosition = 48 * 3600
)

func (s *Server) playbackRateLimit() echo.MiddlewareFunc {
	return s.limitBy(limitRule{
		resolve: func(c echo.Context) (*ratelimit.Limiter, string, bool) {
			if s.playbackLimit == nil {
				return nil, "", false
			}
			return s.playbackLimit, "playback:" + viewerKey(c, uuid.Nil, false), true
		},
		unavailable: "playback beacon rate limiter unavailable, failing open",
		denied:      "too many playback samples",
	})
}

type playbackBeaconRequest struct {
	PositionSeconds int  `json:"position_seconds"`
	WatchedSeconds  int  `json:"watched_seconds"`
	Started         bool `json:"started"`
}

// handlePlaybackBeacon folds one anonymous playback sample into the video's
// daily aggregates. It stores no IP, User-Agent, session or viewer id (see
// internal/playbackstats). Best-effort: a storage failure answers 204 so
// telemetry can never degrade playback. The owner's own plays are ignored so
// previews do not skew their numbers.
func (s *Server) handlePlaybackBeacon(c echo.Context) error {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	var in playbackBeaconRequest
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "malformed or invalid request body")
	}
	var fes []FieldError
	if in.PositionSeconds < 0 || in.PositionSeconds > maxPlaybackPosition {
		fes = append(fes, FieldError{Field: "position_seconds", Message: "must be between 0 and 172800"})
	}
	if in.WatchedSeconds < 0 || in.WatchedSeconds > playbackstats.MaxWatchedPerSample {
		fes = append(fes, FieldError{Field: "watched_seconds", Message: "must be between 0 and 30"})
	}
	if len(fes) > 0 {
		return &ValidationError{Fields: fes}
	}
	v, err := s.videoVisibleForRead(c, id)
	if err != nil {
		return err
	}
	userID, _, authed := principalFromContext(c)
	if v.State != "published" || (authed && userID == v.OwnerID) {
		return c.NoContent(http.StatusNoContent)
	}
	duration := s.videoDurationSeconds(c.Request().Context(), id)
	if duration > 0 && in.PositionSeconds > duration+5 {
		return &ValidationError{Fields: []FieldError{{Field: "position_seconds", Message: "beyond the end of the video"}}}
	}
	device, osFamily := playbackstats.ClassifyUA(c.Request().UserAgent())
	if err := s.playbackStats.Record(c.Request().Context(), playbackstats.Sample{
		VideoID: id, PositionSeconds: in.PositionSeconds, WatchedSeconds: in.WatchedSeconds,
		DurationSeconds: duration, Started: in.Started, Device: device, OS: osFamily,
	}); err != nil {
		s.logger.Warn("playback beacon not recorded", "error", err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) videoDurationSeconds(ctx context.Context, id uuid.UUID) int {
	md, ok, err := s.videosvc.GetMetadata(ctx, id)
	if err != nil || !ok || md.DurationSeconds == nil || *md.DurationSeconds <= 0 {
		return 0
	}
	return int(*md.DurationSeconds)
}

type playbackShareView struct {
	Key          string  `json:"key"`
	WatchSeconds int64   `json:"watch_seconds"`
	Share        float64 `json:"share"`
}

type playbackDayView struct {
	Day          string `json:"day"`
	Plays        int64  `json:"plays"`
	WatchSeconds int64  `json:"watch_seconds"`
}

type playbackStatsResponse struct {
	Plays                  int64               `json:"plays"`
	WatchSeconds           int64               `json:"watch_seconds"`
	AvgViewDurationSeconds float64             `json:"avg_view_duration_seconds"`
	DurationSeconds        int                 `json:"duration_seconds"`
	Retention              []float64           `json:"retention"`
	Devices                []playbackShareView `json:"devices"`
	OS                     []playbackShareView `json:"os"`
	Daily                  []playbackDayView   `json:"daily"`
}

func shareViews(in []playbackstats.Share) []playbackShareView {
	out := make([]playbackShareView, 0, len(in))
	for _, s := range in {
		out = append(out, playbackShareView{Key: s.Key, WatchSeconds: s.Seconds, Share: s.Share})
	}
	return out
}

// handleGetPlaybackStats returns the last 30 days of playback aggregates to the
// video's owner or a channel editor; everyone else (and an unknown id) gets 404
// so existence is not leaked — the same authz as the channel stats surface.
func (s *Server) handleGetPlaybackStats(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	v, err := s.videosvc.GetByID(ctx, id)
	if err != nil || (v.OwnerID != userID && !s.canManageChannelContent(ctx, userID, v.ChannelID)) {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	duration := s.videoDurationSeconds(ctx, id)
	rep, err := s.playbackStats.Report(ctx, id, duration, time.Now())
	if err != nil {
		return err
	}
	resp := playbackStatsResponse{
		Plays: rep.Plays, WatchSeconds: rep.WatchSeconds,
		AvgViewDurationSeconds: rep.AvgViewDurationSeconds, DurationSeconds: duration,
		Retention: rep.Retention, Devices: shareViews(rep.Devices), OS: shareViews(rep.OS),
		Daily: make([]playbackDayView, 0, len(rep.Daily)),
	}
	for _, d := range rep.Daily {
		resp.Daily = append(resp.Daily, playbackDayView{Day: d.Day.UTC().Format("2006-01-02"), Plays: d.Plays, WatchSeconds: d.WatchSeconds})
	}
	return c.JSON(http.StatusOK, resp)
}
