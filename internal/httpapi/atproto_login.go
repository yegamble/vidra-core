package httpapi

// ATProto identity-login endpoints (see internal/auth ATProtoOAuthService and
// internal/atproto for the protocol client).
//
// Browser flow:
//
//	POST /api/v1/auth/atproto/start     → resolves+verifies the handle, runs
//	    PAR, seals the per-attempt state (incl. the ephemeral DPoP key) into a
//	    short-lived, HMAC-signed, httpOnly vidra_atproto_state cookie, and
//	    returns {authorization_url}. The SPA fetches this with credentials, then
//	    top-level-navigates to the URL.
//	GET  /api/v1/auth/atproto/callback  → verifies the signed attempt cookie
//	    against the returned state, enforces the iss/sub/scope invariants, mints
//	    the normal Vidra session in COOKIE MODE, and 302-redirects to the
//	    validated return_to with ?oauth=1 (the SPA then calls POST /auth/refresh
//	    with credentials to obtain its access token).
//	GET  /api/v1/auth/atproto/client-metadata.json → the public OAuth client
//	    metadata document (production hosted-client mode).
//
// Redirect validation: the OAuth redirect_uri and client_id are always derived
// server-side from PUBLIC_BASE_URL (never from request parameters); the
// post-login return_to is accepted only as a same-origin relative path and
// travels inside the signed state cookie.

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// atprotoStateCookieName carries the signed per-attempt login state between the
// start request and the auth-server callback.
const atprotoStateCookieName = "vidra_atproto_state"

// atprotoStateCookiePath scopes the state cookie to the ATProto login routes.
const atprotoStateCookiePath = "/api/v1/auth/atproto"

// atprotoStateTTL bounds one login attempt (client-side Max-Age + server-side
// age check).
const atprotoStateTTL = 10 * time.Minute

// atprotoStatePayload is the signed content of the state cookie: the whole
// ATProtoState plus an issued-at.
//
// Note the DPoP PRIVATE key JWK rides inside this signed, httpOnly cookie. That
// is safe: it is flow-ephemeral (a fresh key per attempt), useless after the
// single-use 10-minute exchange, and its only holder is the resource owner's own
// browser. Signing (HMAC over the JWT secret) prevents tampering; HttpOnly keeps
// it out of scripts.
type atprotoStatePayload struct {
	auth.ATProtoState
	IssuedAt int64 `json:"iat"`
}

// sealATProtoState encodes and HMAC-signs the payload (key: the JWT secret — the
// same trust domain as the sessions the flow mints).
func (s *Server) sealATProtoState(p atprotoStatePayload) (string, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(s.cfg.JWTSecret))
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// openATProtoState verifies and decodes a sealed state cookie value, rejecting
// bad signatures, malformed payloads, and expired attempts.
func (s *Server) openATProtoState(sealed string) (atprotoStatePayload, bool) {
	bodyB64, sigB64, ok := strings.Cut(sealed, ".")
	if !ok {
		return atprotoStatePayload{}, false
	}
	body, err := base64.RawURLEncoding.DecodeString(bodyB64)
	if err != nil {
		return atprotoStatePayload{}, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return atprotoStatePayload{}, false
	}
	mac := hmac.New(sha256.New, []byte(s.cfg.JWTSecret))
	mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return atprotoStatePayload{}, false
	}
	var p atprotoStatePayload
	if err := json.Unmarshal(body, &p); err != nil {
		return atprotoStatePayload{}, false
	}
	if p.State == "" || time.Since(time.Unix(p.IssuedAt, 0)) > atprotoStateTTL {
		return atprotoStatePayload{}, false
	}
	return p, true
}

func (s *Server) setATProtoStateCookie(c echo.Context, sealed string) {
	c.SetCookie(&http.Cookie{
		Name:     atprotoStateCookieName,
		Value:    sealed,
		Path:     atprotoStateCookiePath,
		MaxAge:   int(atprotoStateTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode, // Lax: sent on the top-level GET back from the auth server
		Secure:   s.cfg.CookieSecure(),
	})
}

func (s *Server) clearATProtoStateCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     atprotoStateCookieName,
		Value:    "",
		Path:     atprotoStateCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure(),
	})
}

// atprotoLoginStartRequest is the POST /auth/atproto/start body.
type atprotoLoginStartRequest struct {
	// Handle is the ATProto handle to sign in with (e.g. alice.bsky.social).
	Handle string `json:"handle"`
	// ReturnTo is where to send the browser after the flow (same-origin path).
	ReturnTo string `json:"return_to"`
}

func (r atprotoLoginStartRequest) Validate() []FieldError {
	var fes []FieldError
	h := strings.TrimSpace(r.Handle)
	if h == "" {
		fes = append(fes, FieldError{Field: "handle", Message: "is required"})
	} else if !strings.Contains(h, ".") || strings.ContainsAny(h, " \t") {
		fes = append(fes, FieldError{Field: "handle", Message: "must be a domain-style handle (e.g. alice.bsky.social)"})
	}
	return fes
}

// atprotoLoginStartResponse carries the auth-server authorization URL.
type atprotoLoginStartResponse struct {
	AuthorizationURL string `json:"authorization_url"`
}

// handleATProtoLoginStart begins the identity-login flow: it seals the attempt
// state into the signed cookie and returns the authorization URL for a top-level
// navigation. 503 when disabled, 422 for a bad handle/return_to, 502 for
// resolution/upstream failures.
func (s *Server) handleATProtoLoginStart(c echo.Context) error {
	var in atprotoLoginStartRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	returnTo, ok := safeReturnPath(in.ReturnTo)
	if !ok {
		return &ValidationError{Fields: []FieldError{
			{Field: "return_to", Message: "must be a same-origin relative path"},
		}}
	}
	authURL, st, err := s.atprotologinsvc.Begin(c.Request().Context(), in.Handle, returnTo)
	if err != nil {
		return atprotoLoginError(err)
	}
	sealed, err := s.sealATProtoState(atprotoStatePayload{ATProtoState: st, IssuedAt: time.Now().Unix()})
	if err != nil {
		return err
	}
	s.setATProtoStateCookie(c, sealed)
	return c.JSON(http.StatusOK, atprotoLoginStartResponse{AuthorizationURL: authURL})
}

// handleATProtoLoginCallback finishes the flow: it verifies the signed attempt
// cookie against the returned state, then turns the verified identity into a
// cookie-mode Vidra session before redirecting to return_to?oauth=1. Protocol
// violations (missing/forged/expired state, missing code) are 400; user-actionable
// outcomes (denied, identity mismatch, upstream, conflict, disabled account)
// redirect with ?oauth_error=<code>.
func (s *Server) handleATProtoLoginCallback(c echo.Context) error {
	ck, err := c.Cookie(atprotoStateCookieName)
	if err != nil || ck == nil || ck.Value == "" {
		s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "atproto_state_missing")
		return echo.NewHTTPError(http.StatusBadRequest, "missing or expired atproto login state")
	}
	// Single-use: the attempt cookie never survives its callback.
	s.clearATProtoStateCookie(c)
	p, ok := s.openATProtoState(ck.Value)
	if !ok {
		s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "atproto_state_invalid")
		return echo.NewHTTPError(http.StatusBadRequest, "missing or expired atproto login state")
	}
	returnTo, ok := safeReturnPath(p.ReturnTo)
	if !ok {
		returnTo = "/"
	}
	if errCode := c.QueryParam("error"); errCode != "" {
		// The user denied consent (or the auth server refused). Not a protocol
		// violation — hand the browser back with a stable code.
		s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "atproto_provider_denied")
		return oauthErrorRedirect(c, returnTo, "access_denied")
	}
	if subtle.ConstantTimeCompare([]byte(c.QueryParam("state")), []byte(p.State)) != 1 {
		s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "atproto_state_mismatch")
		return echo.NewHTTPError(http.StatusBadRequest, "atproto state mismatch")
	}
	code := c.QueryParam("code")
	if code == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing authorization code")
	}

	user, tokens, outcome, err := s.atprotologinsvc.Complete(
		c.Request().Context(), p.ATProtoState, code, c.QueryParam("iss"), c.Request().UserAgent(),
	)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrATProtoDisabled):
			return atprotoLoginError(err)
		case errors.Is(err, auth.ErrATProtoIdentityMismatch):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "atproto_identity_mismatch")
			return oauthErrorRedirect(c, returnTo, "atproto_identity_mismatch")
		case errors.Is(err, auth.ErrATProtoUpstream):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "atproto_upstream")
			return oauthErrorRedirect(c, returnTo, "atproto_upstream")
		case errors.Is(err, auth.ErrAccountDisabled):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "account_disabled")
			return oauthErrorRedirect(c, returnTo, "account_disabled")
		case errors.Is(err, auth.ErrOwnerClaimRequired):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "owner_claim_required")
			return oauthErrorRedirect(c, returnTo, "owner_claim_required")
		case errors.Is(err, auth.ErrConflict):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "atproto_conflict")
			return oauthErrorRedirect(c, returnTo, "conflict")
		}
		return err
	}

	// Audit the concrete outcome; every path is also a login. The reason names the
	// provider, never a DID, token, or handle.
	if outcome == auth.OAuthCreated {
		s.audit(c, observability.ActionRegister, observability.ResultSuccess, user.ID.String(), "atproto")
	}
	s.audit(c, observability.ActionLogin, observability.ResultSuccess, user.ID.String(), "atproto")

	// A top-level navigation cannot safely receive a bearer token, so the session
	// is cookie-mode: the rotating refresh token travels in the httpOnly
	// vidra_refresh cookie and the SPA obtains its access token via
	// POST /auth/refresh (credentials included).
	s.setRefreshCookie(c, tokens.RefreshToken)
	return atprotoLoginSuccessRedirect(c, returnTo)
}

// handleATProtoClientMetadata serves the public OAuth client-metadata document
// (production hosted-client mode). No auth; always mounted.
func (s *Server) handleATProtoClientMetadata(c echo.Context) error {
	return c.JSON(http.StatusOK, s.atprotologinsvc.ClientMetadata())
}

// atprotoLoginSuccessRedirect sends the browser back to the (already validated)
// return_to path with ?oauth=1 so the SPA knows to fetch its access token,
// preserving any query the path already carried.
func atprotoLoginSuccessRedirect(c echo.Context, returnTo string) error {
	u, err := url.Parse(returnTo)
	if err != nil {
		u = &url.URL{Path: "/"}
	}
	q := u.Query()
	q.Set("oauth", "1")
	u.RawQuery = q.Encode()
	return c.Redirect(http.StatusFound, u.String())
}

// atprotoLoginError maps ATProto login-service sentinels to HTTP error envelopes
// with stable machine codes. It never surfaces a PDS/auth-server URL, token, or
// upstream body.
func atprotoLoginError(err error) error {
	switch {
	case errors.Is(err, auth.ErrATProtoDisabled):
		return &ATProtoLoginError{Status: http.StatusServiceUnavailable, Code: "atproto_disabled", Message: "ATProto login is not enabled on this instance"}
	case errors.Is(err, auth.ErrATProtoInvalidHandle):
		return &ATProtoLoginError{Status: http.StatusUnprocessableEntity, Code: "invalid_handle", Message: "that is not a valid ATProto handle"}
	case errors.Is(err, auth.ErrATProtoResolution):
		return &ATProtoLoginError{Status: http.StatusBadGateway, Code: "atproto_resolution_failed", Message: "could not resolve that ATProto identity"}
	case errors.Is(err, auth.ErrATProtoUpstream):
		return &ATProtoLoginError{Status: http.StatusBadGateway, Code: "atproto_upstream", Message: "could not reach the ATProto server; please try again"}
	default:
		return err
	}
}
