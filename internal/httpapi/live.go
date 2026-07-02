package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/live"
)

const maxLiveTitleLen = 200

// createLiveStreamRequest is the POST /channels/{handle}/live body.
type createLiveStreamRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Privacy     string `json:"privacy"`
	Permanent   bool   `json:"permanent"`
}

func (r createLiveStreamRequest) Validate() []FieldError {
	var errs []FieldError
	if strings.TrimSpace(r.Title) == "" {
		errs = append(errs, FieldError{Field: "title", Message: "is required"})
	} else if len(r.Title) > maxLiveTitleLen {
		errs = append(errs, FieldError{Field: "title", Message: "must be at most 200 characters"})
	}
	switch r.Privacy {
	case "", "public", "unlisted", "private":
	default:
		errs = append(errs, FieldError{Field: "privacy", Message: "must be public, unlisted, or private"})
	}
	return errs
}

// liveStreamView is the public projection of a live stream. The stream key is
// NEVER included here — it is returned only by create/regenerate.
type liveStreamView struct {
	ID                 string    `json:"id"`
	ChannelID          string    `json:"channel_id"`
	Title              string    `json:"title"`
	Description        string    `json:"description"`
	Privacy            string    `json:"privacy"`
	State              string    `json:"state"`
	Permanent          bool      `json:"permanent"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	ChannelHandle      string    `json:"channel_handle,omitempty"`
	ChannelDisplayName string    `json:"channel_display_name,omitempty"`
}

func newLiveStreamView(s live.Stream) liveStreamView {
	v := liveStreamView{
		ID: s.ID.String(), ChannelID: s.ChannelID.String(), Title: s.Title,
		Description: s.Description, Privacy: s.Privacy, State: s.State, Permanent: s.Permanent,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
		ChannelHandle: s.ChannelHandle, ChannelDisplayName: s.ChannelDisplayName,
	}
	return v
}

// liveStreamKeyView carries the raw stream key + ingest URL, returned only on
// create and key regeneration (the key is shown exactly once).
type liveStreamKeyView struct {
	StreamKey string `json:"stream_key"`
	RTMPURL   string `json:"rtmp_url,omitempty"`
}

type createLiveStreamResponse struct {
	LiveStream liveStreamView `json:"live_stream"`
	StreamKey  string         `json:"stream_key"`
	RTMPURL    string         `json:"rtmp_url,omitempty"`
}

type liveStreamListResponse struct {
	LiveStreams []liveStreamView `json:"live_streams"`
}

// handleCreateLiveStream creates a live stream for a channel the caller owns and
// returns it plus the stream key (once) and the RTMP ingest URL. Behind
// requireAuth; non-owner channel → 403, unknown channel → 404.
func (s *Server) handleCreateLiveStream(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in createLiveStreamRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	ch, err := s.channelsvc.GetByHandle(ctx, c.Param("handle"))
	if err != nil {
		return channelError(err)
	}
	if ch.OwnerID != userID {
		return echo.NewHTTPError(http.StatusForbidden, "you do not own this channel")
	}
	stream, key, err := s.livesvc.Create(ctx, ch.ID, live.CreateInput{
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description),
		Privacy:     in.Privacy,
		Permanent:   in.Permanent,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, createLiveStreamResponse{
		LiveStream: newLiveStreamView(stream),
		StreamKey:  key,
		RTMPURL:    s.cfg.LiveRTMPURL,
	})
}

// handleListLiveStreams lists the caller's live streams for a channel they own.
// Behind requireAuth; non-owner → 403, unknown channel → 404. No stream keys.
func (s *Server) handleListLiveStreams(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	ctx := c.Request().Context()
	ch, err := s.channelsvc.GetByHandle(ctx, c.Param("handle"))
	if err != nil {
		return channelError(err)
	}
	if ch.OwnerID != userID {
		return echo.NewHTTPError(http.StatusForbidden, "you do not own this channel")
	}
	streams, err := s.livesvc.ListByChannel(ctx, ch.ID)
	if err != nil {
		return err
	}
	views := make([]liveStreamView, 0, len(streams))
	for _, st := range streams {
		views = append(views, newLiveStreamView(st))
	}
	return c.JSON(http.StatusOK, liveStreamListResponse{LiveStreams: views})
}

// handleGetLiveStream returns a live stream's public metadata. Behind
// optionalAuth: a private stream is visible only to its channel owner (else 404,
// so its existence is not leaked). Never returns the stream key.
func (s *Server) handleGetLiveStream(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	stream, err := s.livesvc.Get(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	if stream.Privacy == "private" {
		userID, _, ok := principalFromContext(c)
		if !ok || userID != stream.OwnerID {
			return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
		}
	}
	return c.JSON(http.StatusOK, newLiveStreamView(stream))
}

// handleRegenerateLiveStreamKey rotates a live stream's key and returns the new
// one (once). Behind requireAuth; non-owner/unknown → 404 (existence not leaked).
func (s *Server) handleRegenerateLiveStreamKey(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	ctx := c.Request().Context()
	stream, err := s.livesvc.Get(ctx, id)
	if err != nil || stream.OwnerID != userID {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	key, err := s.livesvc.RegenerateKey(ctx, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, liveStreamKeyView{StreamKey: key, RTMPURL: s.cfg.LiveRTMPURL})
}

// liveIngestRequest is the body of the internal RTMP ingest hooks — the media
// server presents the raw stream key of the publisher.
type liveIngestRequest struct {
	StreamKey string `json:"stream_key"`
}

const ingestSecretHeader = "X-Ingest-Secret"

// ingestAuthorized gates the ingest hooks: they are disabled (404) unless an
// ingest secret is configured, and require the media server to present it
// (constant-time compared). Returns an *echo.HTTPError to send, or nil to proceed.
func (s *Server) ingestAuthorized(c echo.Context) *echo.HTTPError {
	if s.cfg.LiveIngestSecret == "" {
		return echo.NewHTTPError(http.StatusNotFound, "live ingest is not enabled")
	}
	presented := c.Request().Header.Get(ingestSecretHeader)
	if subtle.ConstantTimeCompare([]byte(presented), []byte(s.cfg.LiveIngestSecret)) != 1 {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid ingest secret")
	}
	return nil
}

// handleLiveIngestStart is the RTMP ingest "on-publish" hook: the media server
// posts the publisher's stream key; a matching stream is flipped to live (allow
// the publish). Authenticated by the ingest shared secret, NOT a user token. An
// unknown/invalid key is 404 (deny). Not enabled (404) without LIVE_INGEST_SECRET.
func (s *Server) handleLiveIngestStart(c echo.Context) error {
	if err := s.ingestAuthorized(c); err != nil {
		return err
	}
	var in liveIngestRequest
	if err := c.Bind(&in); err != nil || strings.TrimSpace(in.StreamKey) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "stream_key is required")
	}
	id, err := s.livesvc.StartIngest(c.Request().Context(), strings.TrimSpace(in.StreamKey))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "no live stream for that key")
	}
	return c.JSON(http.StatusOK, map[string]string{"id": id.String(), "state": live.StateLive})
}

// handleLiveIngestStop is the RTMP "on-publish-done" hook: the stream identified
// by the key returns to offline (permanent) or ended (one-shot). Same auth as
// start.
func (s *Server) handleLiveIngestStop(c echo.Context) error {
	if err := s.ingestAuthorized(c); err != nil {
		return err
	}
	var in liveIngestRequest
	if err := c.Bind(&in); err != nil || strings.TrimSpace(in.StreamKey) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "stream_key is required")
	}
	if _, err := s.livesvc.StopIngest(c.Request().Context(), strings.TrimSpace(in.StreamKey)); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "no live stream for that key")
	}
	return c.NoContent(http.StatusNoContent)
}

// handleDeleteLiveStream deletes a live stream. Behind requireAuth;
// non-owner/unknown → 404. Idempotent for the owner.
func (s *Server) handleDeleteLiveStream(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	ctx := c.Request().Context()
	stream, err := s.livesvc.Get(ctx, id)
	if err != nil || stream.OwnerID != userID {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	if err := s.livesvc.Delete(ctx, id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
