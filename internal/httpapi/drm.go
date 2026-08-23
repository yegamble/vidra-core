package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/drm"
)

// Content protection at the API edge (docs/productionization/interfaces.md §10,
// phase-5 enterprise).
//
// Two surfaces, and they are deliberately unequal in weight. The `drm` block on
// the playback session is the one every player reads; the ClearKey license
// endpoint is a TEST provider's own license service, present so the whole path
// can be proven end to end before a commercial key system is wired in.
//
// # Nothing here changes a shipped response
//
// drmOrNone() falls back to drm.NoDRM, which reports clear media for every
// video, and no install has a content key because the packaging half of the
// seam has not landed. So the session response is byte-identical to the one
// that shipped before this file existed — asserted, on the raw body, by
// TestPlaybackSessionJSONUnchangedByDefault. That test is the point: a `drm`
// key appearing on every session would be a silent contract change for every
// existing player.
//
// # Why the license route is not a media route
//
// A license request is an XHR the player makes itself, so it can set an
// Authorization header, and it is answered from the database rather than from
// storage. It therefore has nothing to do with the credentialed-media trap that
// governs /hls/ and /original: those routes turn any ?pt= or Authorization into
// no-store-and-never-redirect, which is why the playback session mints tokens
// so sparingly. This route is an ordinary API route, and no media route is
// touched by any of it.

// playbackSessionDRM is the session's content-protection block: what the media
// is protected with, and where a license comes from.
//
// ONE field on the session rather than two (a "protection" and a "license"),
// even though the provider answers them with two methods, because a player
// consumes them together — a key id with nowhere to redeem it, or a license URL
// for unencrypted media, is not a state any client can act on. It is
// `omitempty` and a POINTER so that clear media — every video, today — produces
// no key at all rather than an empty object.
type playbackSessionDRM struct {
	// Scheme is the CENC protection scheme the segments are encrypted with
	// ("cenc").
	Scheme string `json:"scheme"`
	// KeyID is the CENC key id (KID). Public: it is in the media, in the EME
	// `encrypted` event, and in the license request the player will make.
	KeyID string `json:"key_id"`
	// KeySystems is where a license comes from, in the provider's order of
	// preference. Omitted when the provider needs no license service (a
	// protected asset whose keys are distributed some other way).
	KeySystems []playbackSessionKeySystem `json:"key_systems,omitempty"`
}

// playbackSessionKeySystem is one EME key system the player may choose.
type playbackSessionKeySystem struct {
	// KeySystem is the EME key system identifier, e.g. "org.w3.clearkey".
	KeySystem string `json:"key_system"`
	// LicenseURL is where the player POSTs its license request:
	// origin-relative when this instance issues the licenses, absolute when a
	// third-party license service does. Same convention as hls_url/dash_url.
	LicenseURL string `json:"license_url"`
}

// drmOrNone returns the configured provider, or the null provider when none is
// wired. Every call site goes through it so that "no DRM" is an object that
// answers questions rather than a nil that each caller must remember to check.
func (s *Server) drmOrNone() drm.Provider {
	if s.drmProvider == nil {
		return drm.NoDRM{}
	}
	return s.drmProvider
}

// sessionDRM builds the session's `drm` block, or nil for clear media.
//
// PROTECTION IS ASKED FIRST AND IS THE GATE. If the asset is not encrypted
// there is no license to configure, so LicenseConfiguration is not called at
// all — which is what keeps the common path (every video on every install) to
// zero extra work beyond the provider's own answer, and keeps NoDRM's session
// free of any DRM-shaped field.
//
// A provider ERROR is logged and swallowed rather than failing the session. A
// session that 500s because a DRM lookup failed would take down playback of
// CLEAR media too — the session is one call for every video, protected or not —
// and the honest degraded answer for "we could not determine protection" is the
// same as for unprotected media: no drm block, and a player that tries to play
// it in the clear and fails loudly at the CDM instead of silently at the API.
func (s *Server) sessionDRM(c echo.Context, videoID, sessionID uuid.UUID) *playbackSessionDRM {
	ctx := c.Request().Context()
	provider := s.drmOrNone()

	protection, err := provider.GetProtectionMetadata(ctx, videoID)
	if err != nil {
		s.logger.Warn("drm protection lookup failed", "video_id", videoID, "error", err)
		return nil
	}
	if protection == nil {
		return nil
	}
	out := &playbackSessionDRM{Scheme: protection.Scheme, KeyID: protection.KeyID.String()}

	cfg, err := provider.LicenseConfiguration(ctx, videoID, sessionID)
	if err != nil {
		// Protection is still reported: the player at least knows the media is
		// encrypted, which is better than being told it is clear.
		s.logger.Warn("drm license configuration failed", "video_id", videoID, "error", err)
		return out
	}
	if cfg != nil {
		for _, ks := range cfg.KeySystems {
			out.KeySystems = append(out.KeySystems, playbackSessionKeySystem{
				KeySystem:  ks.KeySystem,
				LicenseURL: ks.LicenseURL,
			})
		}
	}
	return out
}

// clearKeyLicenseRequest is the EME ClearKey license request body. The CDM
// produces it; its shape is the W3C's, not ours.
type clearKeyLicenseRequest struct {
	KIDs []string `json:"kids"`
	Type string   `json:"type,omitempty"`
}

// Validate rejects a body that names no key, or so many that answering it would
// be unbounded work. It never echoes a key id back: a validation message that
// repeated caller-supplied strings would be a reflection surface for no benefit.
func (r clearKeyLicenseRequest) Validate() []FieldError {
	switch {
	case len(r.KIDs) == 0:
		return []FieldError{{Field: "kids", Message: "must name at least one key id"}}
	case len(r.KIDs) > drm.MaxLicenseKeyIDs:
		return []FieldError{{Field: "kids", Message: "too many key ids in one request"}}
	}
	return nil
}

// handleClearKeyLicense answers an EME ClearKey license request for one video.
//
// # Authorization
//
// videoVisibleForMedia — the SAME function the media routes and the playback
// session call, not a reimplementation. A license is authority over the KEY to
// a video's bytes, so it must not be obtainable by anyone who could not obtain
// the bytes: an invisible video is 404 (existence is not leaked), and a
// password video with no credential is 401 password_required. That gate already
// accepts the playback session's token as `Authorization: Bearer <token>`,
// which is exactly how a password video's player reaches this endpoint — a
// license request is an XHR the player makes itself, so unlike native-HLS
// segment fetches it can set a header.
//
// # What "not protected" answers
//
// 404, and the same 404 for three different states: no provider is configured,
// the configured provider does not issue ClearKey licenses, and this video has
// no keys. Distinguishing them would tell an unauthorised-but-visible caller
// whether a given video is DRM-protected, which is not their business, and
// would tell an attacker which instances to bother probing.
func (s *Server) handleClearKeyLicense(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if _, err := s.videoVisibleForMedia(c, id); err != nil {
		return err
	}
	var in clearKeyLicenseRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	issuer, ok := s.drmOrNone().(drm.ClearKeyIssuer)
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, "no drm keys for this video")
	}
	license, err := issuer.IssueClearKeyLicense(c.Request().Context(), id, in.KIDs)
	switch {
	case errors.Is(err, drm.ErrNoKeys), errors.Is(err, drm.ErrUnknownKeyID):
		return echo.NewHTTPError(http.StatusNotFound, "no drm keys for this video")
	case err != nil:
		// The error is logged, never returned: it can name the video and the
		// failure class, and a license endpoint is not where an operator's KEK
		// troubles should be narrated to a viewer.
		s.logger.Error("clearkey license issuance failed", "video_id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "license unavailable")
	}
	// The response body CONTAINS THE CONTENT KEY. It is never cached anywhere:
	// a shared cache holding a license would hand the key to viewers who never
	// cleared the gate above.
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, license)
}
