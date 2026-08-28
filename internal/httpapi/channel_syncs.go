package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/channelsync"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// createChannelSyncRequest is the POST /api/v1/channel-syncs body. channel_id is
// a LOCAL channel the caller owns; external_channel_url is the remote platform
// channel to mirror.
type createChannelSyncRequest struct {
	ChannelID          string `json:"channel_id"`
	ExternalChannelURL string `json:"external_channel_url"`
}

func (r createChannelSyncRequest) Validate() []FieldError {
	var fes []FieldError
	if strings.TrimSpace(r.ChannelID) == "" {
		fes = append(fes, FieldError{Field: "channel_id", Message: "is required"})
	} else if _, err := uuid.Parse(strings.TrimSpace(r.ChannelID)); err != nil {
		fes = append(fes, FieldError{Field: "channel_id", Message: "must be a valid channel id"})
	}
	if strings.TrimSpace(r.ExternalChannelURL) == "" {
		fes = append(fes, FieldError{Field: "external_channel_url", Message: "is required"})
	}
	return fes
}

// channelSyncView is the public projection of a channel auto-sync. last_error is
// a safe, human-readable reason on the last failed run (never a raw internal
// error or credentials); last_sync_at is omitted until the first run completes.
type channelSyncView struct {
	ID                 string     `json:"id"`
	ChannelID          string     `json:"channel_id"`
	ExternalChannelURL string     `json:"external_channel_url"`
	State              string     `json:"state"`
	LastSyncAt         *time.Time `json:"last_sync_at,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func newChannelSyncView(s sqlcgen.ChannelSync) channelSyncView {
	v := channelSyncView{
		ID:                 s.ID.String(),
		ChannelID:          s.ChannelID.String(),
		ExternalChannelURL: s.ExternalChannelUrl,
		State:              s.State,
		LastError:          s.LastError,
		CreatedAt:          s.CreatedAt,
		UpdatedAt:          s.UpdatedAt,
	}
	if s.LastSyncAt.Valid {
		t := s.LastSyncAt.Time
		v.LastSyncAt = &t
	}
	return v
}

// channelSyncResponse wraps a single sync.
type channelSyncResponse struct {
	ChannelSync channelSyncView `json:"channel_sync"`
}

// channelSyncListResponse wraps the caller's syncs.
type channelSyncListResponse struct {
	ChannelSyncs []channelSyncView `json:"channel_syncs"`
	pageMeta
}

// handleCreateChannelSync binds a local channel to an external channel URL for
// auto-sync (owner only). 403 feature_disabled when the runtime admin setting
// turns the feature off (W8); 503 when the deployment lacks the yt-dlp import
// resolver; 422 for a bad/private URL or when the caller is at the per-user
// cap; 404 when the channel is unknown or not owned; 409 when an identical
// sync already exists.
func (s *Server) handleCreateChannelSync(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if !s.channelSyncEnabled() {
		return &FeatureDisabledError{Feature: "channel_sync"}
	}
	var in createChannelSyncRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	channelID, _ := uuid.Parse(strings.TrimSpace(in.ChannelID)) // validated above
	sync, err := s.channelsyncsvc.Create(c.Request().Context(), userID, channelID, in.ExternalChannelURL)
	if err != nil {
		return channelSyncError(err)
	}
	return c.JSON(http.StatusCreated, channelSyncResponse{ChannelSync: newChannelSyncView(sync)})
}

// handleListChannelSyncs lists the authenticated user's channel syncs,
// paginated via ?limit (1–100, default 20)/?offset with a true total. It
// previously returned every sync unbounded.
func (s *Server) handleListChannelSyncs(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	syncs, total, err := s.channelsyncsvc.ListForUser(c.Request().Context(), userID, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]channelSyncView, 0, len(syncs))
	for _, sync := range syncs {
		views = append(views, newChannelSyncView(sync))
	}
	return c.JSON(http.StatusOK, channelSyncListResponse{ChannelSyncs: views, pageMeta: page.meta(total)})
}

// handleDeleteChannelSync removes a channel sync the caller owns.
func (s *Server) handleDeleteChannelSync(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "channel sync not found")
	}
	if derr := s.channelsyncsvc.Delete(c.Request().Context(), userID, id); derr != nil {
		return channelSyncError(derr)
	}
	return c.NoContent(http.StatusNoContent)
}

// handleSyncChannelNow schedules an owned sync to run on the next worker tick.
// 403 feature_disabled when the runtime admin setting is off (W8); 503 when the
// deployment lacks the yt-dlp import resolver; 404 when unknown or not owned;
// 429 (with Retry-After) when the server-side cooldown since the last completed
// run has not yet elapsed.
func (s *Server) handleSyncChannelNow(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if !s.channelSyncEnabled() {
		return &FeatureDisabledError{Feature: "channel_sync"}
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "channel sync not found")
	}
	if serr := s.channelsyncsvc.SyncNow(c.Request().Context(), userID, id); serr != nil {
		if errors.Is(serr, channelsync.ErrCooldown) {
			if retry := int(s.channelsyncsvc.Cooldown().Seconds()); retry > 0 {
				c.Response().Header().Set("Retry-After", strconv.Itoa(retry))
			}
		}
		return channelSyncError(serr)
	}
	return c.NoContent(http.StatusAccepted)
}

// channelSyncError maps channelsync sentinels to HTTP error envelopes. Ownership
// failures collapse to 404 so an unowned sync stays invisible.
func channelSyncError(err error) error {
	switch {
	case errors.Is(err, channelsync.ErrDisabled):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "channel auto-sync is not enabled on this instance")
	case errors.Is(err, channelsync.ErrInvalidURL):
		return &ValidationError{Fields: []FieldError{{Field: "external_channel_url", Message: "must be a public http(s) URL"}}}
	case errors.Is(err, channelsync.ErrMaxReached):
		return &ValidationError{Fields: []FieldError{{Field: "channel_id", Message: "you have reached the maximum number of channel syncs"}}}
	case errors.Is(err, channelsync.ErrConflict):
		return echo.NewHTTPError(http.StatusConflict, "this channel is already synced with that URL")
	case errors.Is(err, channelsync.ErrCooldown):
		return echo.NewHTTPError(http.StatusTooManyRequests, "this channel was synced too recently; try again shortly")
	case errors.Is(err, channelsync.ErrChannelNotFound), errors.Is(err, channelsync.ErrNotFound), errors.Is(err, channelsync.ErrForbidden):
		return echo.NewHTTPError(http.StatusNotFound, "channel sync not found")
	default:
		return err
	}
}
