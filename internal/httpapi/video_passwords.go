package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/playback"
	"github.com/vidra/vidra-core/internal/video"
)

// playbackTokenTTL is the lifetime of a video unlock token (CORE-17 / W1.C2): 6h.
const playbackTokenTTL = 6 * time.Hour

// defaultUnlockRateLimit / defaultUnlockRateWindow are the built-in, always-on
// budget for POST /videos/{id}/unlock when no auth limiter is configured (e.g. a
// single-node deployment running without Redis). unlock is a password-guessing
// surface, so it must never be left unthrottled; these mirror the login default
// (10 requests / minute per client IP). A configured auth limiter
// (WithAuthRateLimiter) overrides this.
const (
	defaultUnlockRateLimit  = 10
	defaultUnlockRateWindow = time.Minute
)

// playbackTokenParam is the query-string name that carries a playback token for
// header-less players: Safari native-HLS, progressive <video src>, and <img>
// posters cannot set an Authorization header, so they append ?pt=<token>. This is
// the ONLY token type ever honoured in a query string (account tokens never are).
const playbackTokenParam = "pt"

// playbackTokenFromRequest extracts a candidate playback token: the ?pt= query
// param takes precedence (the header-less path), else the Bearer token. A Bearer
// here may actually be an ACCOUNT token — that is harmless, it simply fails
// playback-token verification (the two token types are cryptographically distinct).
func playbackTokenFromRequest(c echo.Context) string {
	if pt := c.QueryParam(playbackTokenParam); pt != "" {
		return pt
	}
	if tok, ok := bearerToken(c.Request().Header.Get(echo.HeaderAuthorization)); ok {
		return tok
	}
	return ""
}

// hasPlaybackToken reports whether the request carries a valid, unexpired
// playback token granting scope on subjectID — a video id for
// playback.ScopePlayback, a live stream id for playback.ScopeLive. The scope is
// named by the caller so one gate's credential can never open another's.
func (s *Server) hasPlaybackToken(c echo.Context, subjectID uuid.UUID, scope playback.Scope) bool {
	if s.playbackSigner == nil {
		return false
	}
	tok := playbackTokenFromRequest(c)
	if tok == "" {
		return false
	}
	return s.playbackSigner.Verify(tok, subjectID, scope)
}

// passwordGate enforces the CORE-17 access rule for a password-protected video on
// a read path. It is a no-op (returns nil) for any non-password video. For a
// privacy=password video it returns nil when the caller is the owner, a
// moderator/admin, or presents a valid playback token for this video; otherwise
// it returns a 401 password_required. The token/hash/password are never logged or
// echoed.
func (s *Server) passwordGate(c echo.Context, videoID uuid.UUID, privacy, shortCode string, ownerID uuid.UUID) error {
	if privacy != video.PrivacyPassword {
		return nil
	}
	if userID, role, ok := principalFromContext(c); ok && (userID == ownerID || isStaff(role)) {
		return nil
	}
	if s.hasPlaybackToken(c, videoID, playback.ScopePlayback) {
		return nil
	}
	// The 401 names the video. A caller who reached it by short code has no
	// other way to find the uuid that POST /videos/{id}/unlock is keyed on.
	return &PasswordRequiredError{VideoID: videoID.String(), ShortCode: shortCode}
}

// --- unlock ---

// unlockRequest is the POST /videos/{id}/unlock body.
type unlockRequest struct {
	Password string `json:"password"`
}

// unlockResponse returns the minted playback token and its lifetime in seconds.
// The token is a SECRET (video-scoped, 6h, no account identity); it is returned
// once to the caller and never logged.
type unlockResponse struct {
	PlaybackToken string `json:"playback_token"`
	ExpiresIn     int    `json:"expires_in"`
}

// handleUnlockVideo verifies a submitted password against a password-protected
// video's stored hashes and, on success, mints a video-scoped playback token.
// Optional auth; rate-limited under the same budget family as login (this is a
// password-guessing surface). 200 with the token on success; 401 on a wrong
// password; 404 for an unknown video or one that is not privacy=password (so the
// endpoint reveals nothing about non-password videos). Never logs the password or
// the token.
func (s *Server) handleUnlockVideo(c echo.Context) error {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	var in unlockRequest
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	v, err := s.videosvc.GetByID(ctx, id)
	if err != nil || v.Privacy != video.PrivacyPassword {
		// Unknown video OR not a password video: 404, so the endpoint leaks nothing.
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	ok, err := s.videosvc.VerifyPassword(ctx, id, in.Password)
	if err != nil {
		return err
	}
	if !ok {
		// Wrong password → 401. The password is never logged; abusive guessing is
		// throttled by the auth-budget rate limiter (which audits its denials).
		return echo.NewHTTPError(http.StatusUnauthorized, "incorrect password")
	}
	// A correct password opens a playback SESSION, same as POST
	// /videos/{id}/playback-session does for a caller who already had one: the
	// token carries this session's id and scope, so an unlock and a session mint
	// produce the same kind of credential and the same telemetry key. Only the
	// response shape differs — unlock predates the session object and keeps its
	// contract so the existing unlock prompt is untouched.
	token := s.playbackSigner.Sign(id, uuid.New(), playback.ScopePlayback, playbackTokenTTL)
	return c.JSON(http.StatusOK, unlockResponse{
		PlaybackToken: token,
		ExpiresIn:     int(playbackTokenTTL / time.Second),
	})
}

// --- owner password management ---

// videoPasswordView is one password's public projection (id + created_at); the
// plaintext and hash are write-only and never appear in any response.
type videoPasswordView struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// videoPasswordsResponse is the GET/PUT listing shape.
type videoPasswordsResponse struct {
	Passwords []videoPasswordView `json:"passwords"`
}

func videoPasswordViews(ps []video.VideoPassword) []videoPasswordView {
	out := make([]videoPasswordView, 0, len(ps))
	for _, p := range ps {
		out = append(out, videoPasswordView{ID: p.ID.String(), CreatedAt: p.CreatedAt})
	}
	return out
}

// setVideoPasswordRequest is the POST body (add one password).
type setVideoPasswordRequest struct {
	Password string `json:"password"`
}

// replaceVideoPasswordsRequest is the PUT body (replace the whole set).
type replaceVideoPasswordsRequest struct {
	Passwords []string `json:"passwords"`
}

// handleListVideoPasswords lists a video's passwords (owner only; id + created_at
// only). A non-owner or unknown id is 404.
func (s *Server) handleListVideoPasswords(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	ps, err := s.videosvc.ListPasswords(c.Request().Context(), userID, id)
	if err != nil {
		return videoError(err)
	}
	return c.JSON(http.StatusOK, videoPasswordsResponse{Passwords: videoPasswordViews(ps)})
}

// handleAddVideoPassword stores one new password for a video (owner only; 6–100
// chars → else 400). Returns the new row (id + created_at) with 201. Never logs
// or echoes the plaintext.
func (s *Server) handleAddVideoPassword(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	var in setVideoPasswordRequest
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	p, err := s.videosvc.AddPassword(c.Request().Context(), userID, id, in.Password)
	if err != nil {
		return videoPasswordError(err)
	}
	return c.JSON(http.StatusCreated, videoPasswordView{ID: p.ID.String(), CreatedAt: p.CreatedAt})
}

// handleReplaceVideoPasswords replaces a video's whole password set (owner only;
// 1–20 entries, each 6–100 chars → else 400). Returns the stored set (GET shape).
func (s *Server) handleReplaceVideoPasswords(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	var in replaceVideoPasswordsRequest
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ps, err := s.videosvc.ReplacePasswords(c.Request().Context(), userID, id, in.Passwords)
	if err != nil {
		return videoPasswordError(err)
	}
	return c.JSON(http.StatusOK, videoPasswordsResponse{Passwords: videoPasswordViews(ps)})
}

// handleDeleteVideoPassword removes one password (owner only). 204 on success;
// 404 for an unknown passwordId; 409 when it is the last password of a
// privacy=password video.
func (s *Server) handleDeleteVideoPassword(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	pwID, err := pathUUID(c, "passwordId", "password not found")
	if err != nil {
		return err
	}
	if err := s.videosvc.DeletePassword(c.Request().Context(), userID, id, pwID); err != nil {
		return videoPasswordError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// videoPasswordError maps the password service errors to HTTP responses: a
// *PasswordValidationError → 400, and the ownership/last-password/not-found
// sentinels via videoError (404 / 409 / 404).
func videoPasswordError(err error) error {
	var ve *video.PasswordValidationError
	if errors.As(err, &ve) {
		return echo.NewHTTPError(http.StatusBadRequest, ve.Message)
	}
	return videoError(err)
}
