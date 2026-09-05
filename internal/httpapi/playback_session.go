package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/playback"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// Playback sessions (docs/productionization/interfaces.md §5, phase-4 item 1).
//
// A session is the ONE call a player makes before it plays: it decides
// authorization once, and answers with everything needed to run the playback
// without asking the core API again per segment — which manifest to fetch, what
// packaging format it is in, which rungs exist, a correlation id for quality
// telemetry, and a credential IF (and only if) this video needs one.
//
// Everything phase 4 and 5 add — CDN steering, signed URLs, DRM license
// configuration, P2P policy — lands as fields on THIS response, behind an API
// the player already calls. That is the whole reason to ship it before any of
// them exist.
//
// # Why the token is conditional
//
// The obvious design — mint a playback token for every viewer — is the one thing
// this endpoint must not do. credentialedMediaRequest treats ANY ?pt= or
// Authorization header as credentialed, and a credentialed media request is
// forced to no-store and is never presigned or redirected (delivery.go). Today
// that is harmless because tokens exist only for password videos. Handing one to
// every viewer would silently turn every media request origin-only and delete
// the entire CDN/presign benefit phase 4 exists to deliver — with no error, no
// log line, and no test failing.
//
// So the token is minted only for a video that genuinely cannot be played
// without one. That is exactly the password tier: a private or unpublished video
// is reachable by its owner through the short-lived video-read cookie, which a <video src> or
// native-HLS request carries on its own; a password video has no cookie
// equivalent, which is why ?pt= exists at all.

// playbackSessionResponse is the session object a player consumes.
type playbackSessionResponse struct {
	// SessionID correlates every event this playback produces (phase-4 item 4).
	// It is server-minted per session and is NOT the X-Vidra-Session browse id,
	// which is client-generated per tab and carries no authorization meaning.
	SessionID string `json:"session_id"`
	// VideoID / LiveStreamID echo the subject this session is for, so a client
	// holding several sessions never has to remember which request produced which
	// response. EXACTLY ONE is present, and which one is also how a client knows
	// what it is playing: a VOD session (POST /videos/{id}/playback-session)
	// carries video_id, a live session (POST /live/{id}/playback-session, phase-4
	// item 7) carries live_stream_id. They are separate fields rather than one
	// polymorphic id because videos and live streams are different tables — a
	// client that took a live stream's id for a video id would get a 404 from
	// every video endpoint it tried.
	VideoID      string `json:"video_id,omitempty"`
	LiveStreamID string `json:"live_stream_id,omitempty"`
	// PackagingFormat is "hls-ts" or "cmaf"; omitted when no tree is ready.
	PackagingFormat string `json:"packaging_format,omitempty"`
	// HLSURL/DASHURL are ORIGIN-RELATIVE. They are not presigned: a presigned URL
	// is a single-object bearer credential with an hour of life, and advertising
	// one per manifest would be a far worse credential-lifetime story than the
	// per-request redirect decision serveMediaAsset already makes at byte-serve
	// time. Delivery source selection stays where it is; the session says WHAT to
	// play, not WHERE the bytes come from.
	HLSURL  string `json:"hls_url,omitempty"`
	DASHURL string `json:"dash_url,omitempty"`
	// Renditions are the ladder rungs, tallest first — the same list the video
	// detail carries.
	Renditions []renditionView `json:"renditions,omitempty"`
	// PlaybackToken is present ONLY for a subject that requires one: a video with
	// privacy = password, or a PRIVATE live stream. Absent means "play these URLs
	// as they are" — and absent is the case that keeps CDN and presigned delivery
	// reachable. See the file comment.
	PlaybackToken string `json:"playback_token,omitempty"`
	// ExpiresIn is the token's lifetime in seconds, present only with a token.
	ExpiresIn int `json:"expires_in,omitempty"`
	// DRM is the content-protection block (interfaces.md §10, phase-5): how the
	// media is encrypted and where a license comes from. ABSENT for clear
	// media, which is every video on every install — no packaging step writes
	// content keys yet — so this field changes no shipped response. See
	// httpapi/drm.go, and TestPlaybackSessionJSONUnchangedByDefault for the
	// regression test that keeps the default session byte-identical.
	//
	// It lands HERE, on the response the player already calls, which is the
	// reason this endpoint shipped before any of phase 5 existed (see the file
	// comment). A live session never carries it: live media is not packaged and
	// has nothing to protect.
	DRM *playbackSessionDRM `json:"drm,omitempty"`
}

// handleCreatePlaybackSession mints a playback session for one video.
//
// Authorization is videoVisibleForMedia — the SAME function the media routes
// call, not a reimplementation of it. interfaces.md §5 requires mint-time auth
// to reproduce those semantics exactly, and calling the function is the only way
// to guarantee that: an invisible video is 404 (existence is not leaked), and a
// password video with no credential is the deliberate 401 password_required that
// the watch and embed pages render their unlock prompt from. Both answers are
// byte-identical to what GET /hls/master.m3u8 already returns, so the unlock
// flow needs no new client behaviour.
//
// A video with no ready HLS tree is a 200 with no manifest URLs rather than a
// 404: the caller IS authorised, there is simply nothing transcoded yet (or
// transcoding is not wired at all), and progressive /original playback still
// works. A 404 here would be indistinguishable from "no such video" and would
// break the progressive path.
func (s *Server) handleCreatePlaybackSession(c echo.Context) error {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	v, err := s.videoVisibleForMedia(c, id)
	if err != nil {
		return err
	}
	sessionID := uuid.New()
	resp := playbackSessionResponse{SessionID: sessionID.String(), VideoID: id.String()}
	if tree, ok := s.hlsDetail(c, id); ok {
		resp.PackagingFormat = tree.format
		resp.HLSURL = tree.hlsURL
		resp.DASHURL = tree.dashURL
		resp.Renditions = tree.renditions
	}
	if token, ttl, ok := s.mintSessionToken(v, id, sessionID); ok {
		resp.PlaybackToken = token
		resp.ExpiresIn = int(ttl / time.Second)
	}
	resp.DRM = s.sessionDRM(c, id, sessionID)
	return c.JSON(http.StatusOK, resp)
}

// mintSessionToken issues this session's media credential, or reports that this
// video needs none.
//
// Reaching here means the caller already cleared videoVisibleForMedia, so for a
// password video they are the owner, a moderator, or already hold a valid token.
// In all three cases a session-scoped token is the right answer:
//   - the owner and the moderator currently have NO way to watch their own
//     password video in Safari, because native HLS cannot send an Authorization
//     header and there is no cookie path for the password gate;
//   - a caller holding a token gets it renewed, which is the renewal the item
//     asks for. Renewal EXTENDS an existing grant, it never creates one — an
//     expired or absent token fails videoVisibleForMedia above and never gets
//     here.
func (s *Server) mintSessionToken(v sqlcgen.GetVideoByIDRow, videoID, sessionID uuid.UUID) (string, time.Duration, bool) {
	if s.playbackSigner == nil || !videoRequiresPlaybackToken(v) {
		return "", 0, false
	}
	return s.playbackSigner.Sign(videoID, sessionID, playback.ScopePlayback, playbackTokenTTL), playbackTokenTTL, true
}

// videoRequiresPlaybackToken reports whether this video's media is unreachable
// without a ?pt= credential. Only the password tier is: every other privacy tier
// is gated on account identity, which a media element's request carries in the
// session cookie by itself.
//
// This predicate is the load-bearing half of the conditional-token rule. Widening
// it is not a feature addition — it turns off CDN and presigned delivery for
// every video it starts matching (see the file comment).
func videoRequiresPlaybackToken(v sqlcgen.GetVideoByIDRow) bool {
	return v.Privacy == video.PrivacyPassword
}
