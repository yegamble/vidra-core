package httpapi

// OAuth/OIDC login endpoints (fix_plan P4 + P15).
//
// Browser flow:
//
//	GET /api/v1/auth/oauth/:provider            → 302 to the provider, with a
//	    fresh state/nonce/PKCE attempt sealed into a short-lived, HMAC-signed,
//	    httpOnly cookie (vidra_oauth_state);
//	GET /api/v1/auth/oauth/:provider/callback   → verifies state + nonce +
//	    id_token, resolves the identity (login / link / create), issues the
//	    normal Vidra session in COOKIE MODE (a top-level navigation cannot
//	    receive a JSON token safely — the SPA then calls POST /auth/refresh
//	    with credentials to obtain its access token), and 302-redirects to the
//	    validated return_to path.
//
// Redirect validation (P15): the OAuth redirect URI sent to the provider is
// ALWAYS derived server-side from PUBLIC_BASE_URL — never read from request
// parameters. The post-login return_to is accepted only as a same-origin
// relative path ("/…", never "//…" or an absolute URL) and travels inside the
// signed state cookie, so the callback redirects only within the instance.

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

// oauthStateCookieName carries the signed per-attempt OAuth state between the
// begin redirect and the provider callback.
const oauthStateCookieName = "vidra_oauth_state"

// oauthStateCookiePath scopes the state cookie to the OAuth routes only.
const oauthStateCookiePath = "/api/v1/auth/oauth"

// oauthStateTTL bounds one login attempt: the cookie expires client-side via
// Max-Age and the payload is additionally rejected server-side past this age.
const oauthStateTTL = 10 * time.Minute

// oauthStatePayload is the signed content of the state cookie. State, nonce,
// and the PKCE verifier are attempt secrets — httpOnly + signed, never logged.
type oauthStatePayload struct {
	Provider string `json:"provider"`
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	ReturnTo string `json:"return_to"`
	IssuedAt int64  `json:"iat"`
}

// sealOAuthState encodes and HMAC-signs the payload (key: the JWT secret —
// same trust domain as the sessions the flow mints).
func (s *Server) sealOAuthState(p oauthStatePayload) (string, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(s.cfg.JWTSecret))
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// openOAuthState verifies and decodes a sealed state cookie value. It rejects
// bad signatures, malformed payloads, and expired attempts.
func (s *Server) openOAuthState(sealed string) (oauthStatePayload, bool) {
	bodyB64, sigB64, ok := strings.Cut(sealed, ".")
	if !ok {
		return oauthStatePayload{}, false
	}
	body, err := base64.RawURLEncoding.DecodeString(bodyB64)
	if err != nil {
		return oauthStatePayload{}, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return oauthStatePayload{}, false
	}
	mac := hmac.New(sha256.New, []byte(s.cfg.JWTSecret))
	mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return oauthStatePayload{}, false
	}
	var p oauthStatePayload
	if err := json.Unmarshal(body, &p); err != nil {
		return oauthStatePayload{}, false
	}
	if p.State == "" || time.Since(time.Unix(p.IssuedAt, 0)) > oauthStateTTL {
		return oauthStatePayload{}, false
	}
	return p, true
}

func (s *Server) setOAuthStateCookie(c echo.Context, sealed string) {
	c.SetCookie(&http.Cookie{
		Name:     oauthStateCookieName,
		Value:    sealed,
		Path:     oauthStateCookiePath,
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode, // Lax: sent on the top-level GET back from the provider
		Secure:   s.cfg.CookieSecure(),
	})
}

func (s *Server) clearOAuthStateCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     oauthStateCookieName,
		Value:    "",
		Path:     oauthStateCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure(),
	})
}

// oauthRedirectURI derives the callback redirect URI from PUBLIC_BASE_URL —
// the P15 rule: it is never taken from request parameters, so a crafted begin
// request cannot point the provider's code anywhere else.
func (s *Server) oauthRedirectURI(provider string) string {
	if s.cfg.PublicBaseURL == "" {
		return ""
	}
	return s.cfg.PublicBaseURL + "/api/v1/auth/oauth/" + provider + "/callback"
}

// safeReturnPath validates a post-login return_to as a same-origin relative
// path: it must start with a single "/" and carry no scheme/host/backslash.
// Empty input defaults to "/".
func safeReturnPath(p string) (string, bool) {
	if p == "" {
		return "/", true
	}
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.Contains(p, "\\") {
		return "", false
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "", false
	}
	return p, true
}

// oauthErrorRedirect sends the browser back to the (already validated)
// return_to path with a machine-readable oauth_error code the frontend can
// render, preserving any query the path already carried.
func oauthErrorRedirect(c echo.Context, returnTo, code string) error {
	u, err := url.Parse(returnTo)
	if err != nil {
		u = &url.URL{Path: "/"}
	}
	q := u.Query()
	q.Set("oauth_error", code)
	u.RawQuery = q.Encode()
	return c.Redirect(http.StatusFound, u.String())
}

// handleOAuthBegin starts the OIDC login flow for one provider: it seals the
// attempt state into the signed cookie and redirects to the provider's
// authorization endpoint. 404 for a provider that is not configured.
func (s *Server) handleOAuthBegin(c echo.Context) error {
	name := c.Param("provider")
	if s.oauthsvc == nil || !s.oauthsvc.Enabled(name) {
		return echo.NewHTTPError(http.StatusNotFound, "unknown oauth provider")
	}
	returnTo, ok := safeReturnPath(c.QueryParam("return_to"))
	if !ok {
		return &ValidationError{Fields: []FieldError{
			{Field: "return_to", Message: "must be a same-origin relative path"},
		}}
	}
	redirectURI := s.oauthRedirectURI(name)
	if redirectURI == "" {
		// Config validation requires PUBLIC_BASE_URL whenever providers are
		// configured, so this only guards mis-wired tests/embeddings.
		return echo.NewHTTPError(http.StatusServiceUnavailable, "oauth is not configured on this instance")
	}
	authURL, st, err := s.oauthsvc.BeginAuth(c.Request().Context(), name, redirectURI)
	if err != nil {
		if errors.Is(err, auth.ErrUnknownOAuthProvider) {
			return echo.NewHTTPError(http.StatusNotFound, "unknown oauth provider")
		}
		// Discovery failed — the provider is down/misconfigured, not the client.
		return echo.NewHTTPError(http.StatusBadGateway, "oauth provider is unavailable")
	}
	sealed, err := s.sealOAuthState(oauthStatePayload{
		Provider: name,
		State:    st.State,
		Nonce:    st.Nonce,
		Verifier: st.Verifier,
		ReturnTo: returnTo,
		IssuedAt: time.Now().Unix(),
	})
	if err != nil {
		return err
	}
	s.setOAuthStateCookie(c, sealed)
	return c.Redirect(http.StatusFound, authURL)
}

// handleOAuthCallback finishes the flow: verifies the signed attempt cookie
// against the returned state, exchanges the code, and turns the verified
// identity into a cookie-mode Vidra session before redirecting to return_to.
// Protocol violations (missing/forged/expired state, nonce mismatch) are 400;
// provider-side failures are 502; user-actionable outcomes (denied consent,
// email conflict, disabled account) redirect with ?oauth_error=<code>.
func (s *Server) handleOAuthCallback(c echo.Context) error {
	name := c.Param("provider")
	if s.oauthsvc == nil || !s.oauthsvc.Enabled(name) {
		return echo.NewHTTPError(http.StatusNotFound, "unknown oauth provider")
	}
	ck, err := c.Cookie(oauthStateCookieName)
	if err != nil || ck == nil || ck.Value == "" {
		s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_state_missing")
		return echo.NewHTTPError(http.StatusBadRequest, "missing or expired oauth state")
	}
	// Single-use: the attempt cookie never survives its callback.
	s.clearOAuthStateCookie(c)
	st, ok := s.openOAuthState(ck.Value)
	if !ok || st.Provider != name {
		s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_state_invalid")
		return echo.NewHTTPError(http.StatusBadRequest, "missing or expired oauth state")
	}
	returnTo, ok := safeReturnPath(st.ReturnTo)
	if !ok {
		returnTo = "/"
	}
	if errCode := c.QueryParam("error"); errCode != "" {
		// The user denied consent (or the provider refused). Not a protocol
		// violation — hand the browser back to the app with a stable code.
		s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_provider_denied")
		return oauthErrorRedirect(c, returnTo, "access_denied")
	}
	if subtle.ConstantTimeCompare([]byte(c.QueryParam("state")), []byte(st.State)) != 1 {
		s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_state_mismatch")
		return echo.NewHTTPError(http.StatusBadRequest, "oauth state mismatch")
	}
	code := c.QueryParam("code")
	if code == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing authorization code")
	}

	user, tokens, outcome, err := s.oauthsvc.CompleteAuth(
		c.Request().Context(), name, s.oauthRedirectURI(name), code,
		auth.OAuthState{State: st.State, Nonce: st.Nonce, Verifier: st.Verifier},
		c.Request().UserAgent(),
	)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrOAuthNonceMismatch):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_nonce_mismatch")
			return echo.NewHTTPError(http.StatusBadRequest, "oauth nonce mismatch")
		case errors.Is(err, auth.ErrOAuthEmailConflict):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_email_conflict")
			return oauthErrorRedirect(c, returnTo, "email_conflict")
		case errors.Is(err, auth.ErrOAuthEmailMissing):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_email_missing")
			return oauthErrorRedirect(c, returnTo, "email_required")
		case errors.Is(err, auth.ErrAccountDisabled):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "account_disabled")
			return oauthErrorRedirect(c, returnTo, "account_disabled")
		case errors.Is(err, auth.ErrConflict):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_conflict")
			return oauthErrorRedirect(c, returnTo, "conflict")
		case errors.Is(err, auth.ErrOAuthExchange):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_exchange_failed")
			return echo.NewHTTPError(http.StatusBadGateway, "oauth code exchange failed")
		}
		return err
	}

	// Audit the concrete outcome; every path is also a login. The reason names
	// the provider, never a token or email.
	switch outcome {
	case auth.OAuthCreated:
		s.audit(c, observability.ActionRegister, observability.ResultSuccess, user.ID.String(), "oauth:"+name)
	case auth.OAuthLinked:
		s.audit(c, observability.ActionOAuthLink, observability.ResultSuccess, user.ID.String(), "oauth:"+name)
	}
	s.audit(c, observability.ActionLogin, observability.ResultSuccess, user.ID.String(), "oauth:"+name)

	// A top-level navigation cannot safely receive a bearer token, so OAuth
	// sessions are always cookie-mode: the rotating refresh token travels in
	// the httpOnly vidra_refresh cookie and the SPA obtains its access token
	// via POST /auth/refresh (credentials included).
	s.setRefreshCookie(c, tokens.RefreshToken)
	return c.Redirect(http.StatusFound, returnTo)
}

// oauthIdentityView is the public projection of a linked identity. The
// provider subject (the DID for ATProto) is internal plumbing and is not
// exposed; the human-readable handle is, so the owner's settings UI can show
// which Bluesky account is linked. Omitted for providers without a handle.
type oauthIdentityView struct {
	Provider  string    `json:"provider"`
	Email     string    `json:"email"`
	Handle    *string   `json:"handle,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// oauthIdentitiesResponse wraps the caller's linked identities.
type oauthIdentitiesResponse struct {
	Identities []oauthIdentityView `json:"identities"`
}

// handleListOAuthIdentities returns the caller's linked OAuth identities.
func (s *Server) handleListOAuthIdentities(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	idents, err := s.oauthsvc.Identities(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	out := make([]oauthIdentityView, 0, len(idents))
	for _, id := range idents {
		out = append(out, oauthIdentityView{Provider: id.Provider, Email: id.Email, Handle: id.Handle, CreatedAt: id.CreatedAt})
	}
	return c.JSON(http.StatusOK, oauthIdentitiesResponse{Identities: out})
}

// handleUnlinkOAuthIdentity removes the caller's identity for one provider.
// 404 when that provider is not linked; 422 when it is the account's last
// sign-in method (no password set) — set a password first.
func (s *Server) handleUnlinkOAuthIdentity(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	provider := c.Param("provider")
	if err := s.oauthsvc.Unlink(c.Request().Context(), userID, provider); err != nil {
		switch {
		case errors.Is(err, auth.ErrOAuthIdentityNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "identity not linked")
		case errors.Is(err, auth.ErrOAuthLastCredential):
			s.audit(c, observability.ActionOAuthUnlink, observability.ResultFailure, userID.String(), "last_credential")
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "cannot remove the last sign-in method — set a password first")
		case errors.Is(err, auth.ErrAccountNotFound):
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	s.audit(c, observability.ActionOAuthUnlink, observability.ResultSuccess, userID.String(), "oauth:"+provider)
	return c.NoContent(http.StatusNoContent)
}
