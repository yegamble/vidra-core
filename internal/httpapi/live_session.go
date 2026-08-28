package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/live"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/playback"
)

// Live playback sessions (phase-4 item 7) — the live half of the session model
// playback_session.go introduced for VOD, answering with the SAME session object.
//
// # What this is for
//
// Live had no revocable credential of any kind. Its privacy tiers are
// public/unlisted/private (validateLiveFields allows nothing else — there is no
// `password` tier as there is for videos), so a private broadcast was watchable
// by exactly one account and by nobody else, ever, while an unlisted one was
// watchable by anyone who learned the stream id — which the channel's public live
// detail hands out. There was no middle: no way to let one guest into a private
// broadcast, and no access that expires.
//
// A live session closes that. It hands the caller a live-scoped, stream-scoped,
// expiring token, and liveStreamForHLS accepts it in place of ownership. The
// credential is bounded twice over: by its own TTL, and by the live-state check
// that every live media request still passes through — so ENDING THE BROADCAST
// REVOKES EVERY TOKEN outstanding against it. That is the revocation live never
// had.
//
// # What it deliberately is not
//
// No delivery-source selection. Live segments never enter storage.Backend during
// a broadcast — see serveLiveHLSFile and docs/operations.md, "The live plane is
// single-host" — so there is no presign, no mirror CID and no CDN path to
// advertise, and hls_url is the plain origin path the live detail already
// carries.

// handleCreateLivePlaybackSession mints a playback session for one live stream.
//
// Authorization is liveStreamForHLS — the SAME function the live media routes
// call, not a reimplementation. Every one of its answers is inherited: 404 when
// LIVE_HLS_ROOT is unconfigured (nothing could be played anyway), 404 for an
// unknown stream, 404 for a private stream the caller has no claim on (existence
// is not leaked), and 404 for a stream that is not currently live.
//
// That last one is the difference from the VOD session, and it is right rather
// than an oversight. A VOD session with no ready tree is a 200 with no manifest
// because progressive playback still works; a live stream that is not live has
// nothing to play by any route, and a 200 would advertise an hls_url that 404s.
func (s *Server) handleCreateLivePlaybackSession(c echo.Context) error {
	id, err := pathUUID(c, "id", "live stream not found")
	if err != nil {
		return err
	}
	stream, err := s.liveStreamForHLS(c, id)
	if err != nil {
		return err
	}
	sessionID := uuid.New()
	resp := playbackSessionResponse{
		SessionID:    sessionID.String(),
		LiveStreamID: id.String(),
		// The media server muxes MPEG-TS segments into a single-bitrate playlist,
		// so the format is hls-ts and there is no MPD and no rendition ladder to
		// advertise. Claiming rungs a live stream does not have would give an
		// engine adapter (item 3) a quality menu with nothing behind it.
		PackagingFormat: media.HLSFormatTS,
		HLSURL:          liveHLSMasterURL(id),
	}
	if token, ttl, ok := s.mintLiveSessionToken(stream, sessionID); ok {
		resp.PlaybackToken = token
		resp.ExpiresIn = int(ttl / time.Second)
	}
	return c.JSON(http.StatusOK, resp)
}

// mintLiveSessionToken issues this session's media credential, or reports that
// this stream needs none.
//
// Reaching here means the caller already cleared liveStreamForHLS, so for a
// private stream they are the owner or they already hold a valid token. Both
// deserve one: the owner because native HLS in Safari cannot send an
// Authorization header and there is no cookie path for a shared link, and an
// existing holder because that is renewal — which EXTENDS a grant and never
// creates one, since an expired or absent token fails the gate above and never
// reaches this line.
//
// Public and unlisted streams get nothing, on purpose. Handing every live viewer
// a token would mark every playlist and segment request credentialed, forcing
// no-store and blocking every redirect — the same silent delivery regression the
// VOD session was built to avoid.
func (s *Server) mintLiveSessionToken(stream live.Stream, sessionID uuid.UUID) (string, time.Duration, bool) {
	if s.playbackSigner == nil || !liveStreamRequiresPlaybackToken(stream) {
		return "", 0, false
	}
	return s.playbackSigner.Sign(stream.ID, sessionID, playback.ScopeLive, playbackTokenTTL), playbackTokenTTL, true
}
