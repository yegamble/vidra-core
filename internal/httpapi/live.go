package httpapi

import (
	"context"
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
	Title         string `json:"title"`
	Description   string `json:"description"`
	Privacy       string `json:"privacy"`
	Permanent     bool   `json:"permanent"`
	ReplayEnabled bool   `json:"replay_enabled"`
}

func (r createLiveStreamRequest) Validate() []FieldError {
	return validateLiveFields(r.Title, r.Privacy)
}

// validateLiveFields is the shared create/edit validation for a live stream's
// title and privacy.
func validateLiveFields(title, privacy string) []FieldError {
	var errs []FieldError
	if strings.TrimSpace(title) == "" {
		errs = append(errs, FieldError{Field: "title", Message: "is required"})
	} else if len(title) > maxLiveTitleLen {
		errs = append(errs, FieldError{Field: "title", Message: "must be at most 200 characters"})
	}
	switch privacy {
	case "", "public", "unlisted", "private":
	default:
		errs = append(errs, FieldError{Field: "privacy", Message: "must be public, unlisted, or private"})
	}
	return errs
}

// updateLiveStreamRequest is the PATCH /live/{id} body — the editable metadata of
// an existing live stream (never the key or live state).
type updateLiveStreamRequest struct {
	Title         string `json:"title"`
	Description   string `json:"description"`
	Privacy       string `json:"privacy"`
	Permanent     bool   `json:"permanent"`
	ReplayEnabled bool   `json:"replay_enabled"`
}

func (r updateLiveStreamRequest) Validate() []FieldError {
	return validateLiveFields(r.Title, r.Privacy)
}

// liveStreamView is the public projection of a live stream. The stream key is
// NEVER included here — it is returned only by create/regenerate.
type liveStreamView struct {
	ID                 string     `json:"id"`
	ChannelID          string     `json:"channel_id"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	Privacy            string     `json:"privacy"`
	State              string     `json:"state"`
	Permanent          bool       `json:"permanent"`
	ReplayEnabled      bool       `json:"replay_enabled"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ChannelHandle      string     `json:"channel_handle,omitempty"`
	ChannelDisplayName string     `json:"channel_display_name,omitempty"`
	// HLSURL is the live playlist path, present only while the stream is live and
	// a media server (LIVE_HLS_ROOT) is configured to serve it.
	HLSURL string `json:"hls_url,omitempty"`
}

// newLiveStreamView projects a stream. The server is passed so the view can add
// the live HLS URL when the stream is live and HLS serving is configured.
func (s *Server) newLiveStreamView(st live.Stream) liveStreamView {
	v := liveStreamView{
		ID: st.ID.String(), ChannelID: st.ChannelID.String(), Title: st.Title,
		Description: st.Description, Privacy: st.Privacy, State: st.State, Permanent: st.Permanent,
		ReplayEnabled: st.ReplayEnabled, StartedAt: st.StartedAt,
		CreatedAt: st.CreatedAt, UpdatedAt: st.UpdatedAt,
		ChannelHandle: st.ChannelHandle, ChannelDisplayName: st.ChannelDisplayName,
	}
	if st.State == live.StateLive && s.cfg.LiveHLSRoot != "" {
		v.HLSURL = "/api/v1/live/" + st.ID.String() + "/hls/master.m3u8"
	}
	return v
}

// Public "Live now" listing pagination bounds (mirrors the video-feed convention).
const (
	defaultLivePublicLimit = 30
	maxLivePublicLimit     = 100
)

// liveStreamCardView is one entry of the public "Live now" listing — the minimal,
// truthful projection of a currently-live PUBLIC stream for a discovery rail. It
// deliberately omits fields the rail cannot honestly use: no privacy/state (every
// entry is public+live), no stream key, and no viewer/concurrent count (no
// server-side counter exists yet — a W4 live-completion dependency). is_live is
// always true here (this is the card contract that can cheaply carry it; the video
// feed card intentionally does NOT, since live streams are a disjoint table from
// videos). A thumbnail/preview is omitted because live streams have no
// server-generated poster yet.
type liveStreamCardView struct {
	ID                 string     `json:"id"`
	Title              string     `json:"title"`
	Description        string     `json:"description,omitempty"`
	ChannelHandle      string     `json:"channel_handle"`
	ChannelDisplayName string     `json:"channel_display_name"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	IsLive             bool       `json:"is_live"`
	// HLSURL is the live playlist path, present only when a media server
	// (LIVE_HLS_ROOT) is configured to serve it (every listed stream is live).
	HLSURL string `json:"hls_url,omitempty"`
}

// liveStreamPublicListResponse is the public "Live now" listing envelope.
type liveStreamPublicListResponse struct {
	LiveStreams []liveStreamCardView `json:"live_streams"`
	Limit       int                  `json:"limit"`
	Offset      int                  `json:"offset"`
}

// handleListLivePublicStreams lists the currently-live PUBLIC streams across all
// channels for the home "Live now" rail, most-recently-started first. Public (no
// auth required); unlisted/private and offline/ended streams never appear. Never
// returns a stream key. This is a read surface: like the sibling public detail
// GET /live/{id}, it is NOT gated on the live feature toggle (that gate guards
// creation/ingest) — when live is disabled there are simply no live streams to
// list.
func (s *Server) handleListLivePublicStreams(c echo.Context) error {
	limit := clampInt(queryInt(c, "limit", defaultLivePublicLimit), 1, maxLivePublicLimit)
	offset := queryInt(c, "offset", 0)
	cards, err := s.livesvc.ListLivePublic(c.Request().Context(), limit, offset)
	if err != nil {
		return err
	}
	views := make([]liveStreamCardView, 0, len(cards))
	for _, cd := range cards {
		v := liveStreamCardView{
			ID: cd.ID.String(), Title: cd.Title, Description: cd.Description,
			ChannelHandle: cd.ChannelHandle, ChannelDisplayName: cd.ChannelDisplayName,
			StartedAt: cd.StartedAt, IsLive: true,
		}
		if s.cfg.LiveHLSRoot != "" {
			v.HLSURL = "/api/v1/live/" + cd.ID.String() + "/hls/master.m3u8"
		}
		views = append(views, v)
	}
	return c.JSON(http.StatusOK, liveStreamPublicListResponse{LiveStreams: views, Limit: limit, Offset: offset})
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
	if !s.liveEnabled() {
		return &FeatureDisabledError{Feature: "live"}
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
		Title:         strings.TrimSpace(in.Title),
		Description:   strings.TrimSpace(in.Description),
		Privacy:       in.Privacy,
		Permanent:     in.Permanent,
		ReplayEnabled: in.ReplayEnabled,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, createLiveStreamResponse{
		LiveStream: s.newLiveStreamView(stream),
		StreamKey:  key,
		RTMPURL:    s.cfg.LiveRTMPURL,
	})
}

// handleUpdateLiveStream edits a live stream the caller owns (title, description,
// privacy, permanent, replay_enabled). Behind requireAuth; a non-owner or unknown
// id is 404 (existence is not leaked). It never rotates the key or changes the
// live state.
func (s *Server) handleUpdateLiveStream(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	var in updateLiveStreamRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	stream, err := s.livesvc.Get(ctx, id)
	if err != nil || stream.OwnerID != userID {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	updated, err := s.livesvc.Update(ctx, id, live.UpdateInput{
		Title:         strings.TrimSpace(in.Title),
		Description:   strings.TrimSpace(in.Description),
		Privacy:       in.Privacy,
		Permanent:     in.Permanent,
		ReplayEnabled: in.ReplayEnabled,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "live stream not found")
	}
	return c.JSON(http.StatusOK, s.newLiveStreamView(updated))
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
		views = append(views, s.newLiveStreamView(st))
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
	return c.JSON(http.StatusOK, s.newLiveStreamView(stream))
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

// liveIngestRequest is the JSON body of the internal RTMP ingest hooks — the
// media server (or a harness) presents the raw stream key of the publisher.
type liveIngestRequest struct {
	StreamKey string `json:"stream_key"`
}

const ingestSecretHeader = "X-Ingest-Secret"

// liveRTMPApp is the RTMP application name the media server publishes into; it is
// the constant path segment of the on-publish redirect target used to rename a
// session to its stream ID (so the raw key never lands in HLS/recording paths).
const liveRTMPApp = "live"

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

// ingestIdentity extracts the publisher identity the media server presents. It
// accepts BOTH the nginx-rtmp callback form (application/x-www-form-urlencoded
// with a `name` field) and a JSON `{stream_key}` body (curl/harness). At publish
// time `name` is the raw stream key; after the on-publish redirect renames the
// session it is the stream ID (see handleLiveIngestStart). isForm reports whether
// the request is the nginx-rtmp form callback (which expects a redirect rename).
func ingestIdentity(c echo.Context) (ident string, isForm bool) {
	if v := strings.TrimSpace(c.FormValue("name")); v != "" {
		return v, true
	}
	var in liveIngestRequest
	_ = c.Bind(&in)
	return strings.TrimSpace(in.StreamKey), false
}

// handleLiveIngestStart is the RTMP ingest "on-publish" hook: the media server
// posts the publisher's stream key; a matching stream is flipped to live (allow
// the publish). Authenticated by the ingest shared secret, NOT a user token. An
// unknown/invalid key is 404 (deny). Not enabled (404) without LIVE_INGEST_SECRET.
//
// For the nginx-rtmp form callback the response is a 302 redirect whose last path
// segment is the stream ID: nginx-rtmp renames the RTMP session to that name, so
// every downstream path (HLS segments, the recording) is keyed by the stream ID
// and the raw stream key never touches the filesystem. A JSON caller (harness)
// gets a 200 with the resolved id instead.
func (s *Server) handleLiveIngestStart(c echo.Context) error {
	if err := s.ingestAuthorized(c); err != nil {
		return err
	}
	ident, isForm := ingestIdentity(c)
	if ident == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "stream_key is required")
	}
	id, needsRename, err := s.livesvc.StartByIdentity(c.Request().Context(), ident)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "no live stream for that key")
	}
	if isForm && needsRename {
		// Rename the nginx-rtmp session to the stream ID (only the last path
		// segment is significant to the module); keeps the key out of paths. A
		// post-rename re-invocation (needsRename=false) is allowed without
		// redirecting again, so the module does not loop.
		return c.Redirect(http.StatusFound, "rtmp://localhost/"+liveRTMPApp+"/"+id.String())
	}
	if isForm {
		return c.NoContent(http.StatusOK) // allow the (already-renamed) publish
	}
	return c.JSON(http.StatusOK, map[string]string{"id": id.String(), "state": live.StateLive})
}

// handleLiveIngestStop is the RTMP "on-publish-done" hook: the stream identified
// by the key (harness/pre-rename) or the stream ID (media server, post-rename)
// returns to offline (permanent) or ended (one-shot). Same auth as start. When
// the stream has replay enabled, the finished recording is republished as a VOD
// best-effort in the background (never blocking or failing this hook).
func (s *Server) handleLiveIngestStop(c echo.Context) error {
	if err := s.ingestAuthorized(c); err != nil {
		return err
	}
	ident, _ := ingestIdentity(c)
	if ident == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "stream_key is required")
	}
	stream, err := s.livesvc.StopByIdentity(c.Request().Context(), ident)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "no live stream for that key")
	}
	if stream.ReplayEnabled {
		// Detached: the recording ingest + transcode outlive this request, and a
		// replay must never delay or fail the stop hook.
		go s.livesvc.RunReplay(context.WithoutCancel(c.Request().Context()), stream.ID)
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
