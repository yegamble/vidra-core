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
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
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
	// Purpose is "" for a login and "step_up" for a re-authentication started
	// from an existing session (auth_step_up.go); UserID and SessionID bind
	// that attempt to the browser that started it. All three are inside the
	// SIGNED payload.
	Purpose   string `json:"purpose,omitempty"`
	UserID    string `json:"uid,omitempty"`
	SessionID string `json:"sid,omitempty"`
}

func (p oauthStatePayload) stateToken() string { return p.State }
func (p oauthStatePayload) issuedAt() int64    { return p.IssuedAt }

// sealOAuthState encodes and HMAC-signs the payload (key: the JWT secret —
// same trust domain as the sessions the flow mints). See statecookie.go.
func (s *Server) sealOAuthState(p oauthStatePayload) (string, error) {
	return sealState(s.cfg.JWTSecret, p)
}

// openOAuthState verifies and decodes a sealed state cookie value. It rejects
// bad signatures, malformed payloads, and expired attempts.
func (s *Server) openOAuthState(sealed string) (oauthStatePayload, bool) {
	return openState[oauthStatePayload](s.cfg.JWTSecret, sealed, oauthStateTTL)
}

// setOAuthStateCookie parks the sealed attempt for the round trip to the
// provider. Lax: it must still ride the top-level GET back from the provider.
func (s *Server) setOAuthStateCookie(c echo.Context, sealed string) {
	s.writeStateCookie(c, oauthStateCookieName, oauthStateCookiePath, sealed, oauthStateTTL)
}

func (s *Server) clearOAuthStateCookie(c echo.Context) {
	s.clearStateCookie(c, oauthStateCookieName, oauthStateCookiePath)
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
	// An instance with NO providers answers a typed 503, like ATProto's
	// disabled path: "this instance does not do single sign-on" and "you named
	// a provider that does not exist here" are different facts, and A05
	// recorded them collapsed into one 404. An unknown name on an instance that
	// HAS providers is still a 404.
	if s.oauthsvc == nil || len(s.oauthsvc.ProviderNames()) == 0 {
		return &OAuthNotConfiguredError{}
	}
	if !s.oauthsvc.Enabled(name) {
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
		// Typed so the answer survives the 5xx message scrub (see
		// OAuthUpstreamError): "an unexpected error occurred" is indistinguishable
		// from a crash to the operator whose IdP is simply unreachable.
		return &OAuthUpstreamError{
			Status:  http.StatusBadGateway,
			Code:    "oauth_provider_unavailable",
			Message: "this instance could not reach its sign-in provider, so the sign-in could not start — try again shortly, or ask the administrator to check the provider configuration",
		}
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
	if s.oauthsvc == nil || len(s.oauthsvc.ProviderNames()) == 0 {
		return &OAuthNotConfiguredError{}
	}
	if !s.oauthsvc.Enabled(name) {
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

	// The step-up branch: same verification, no session. See the ATProto twin.
	if st.Purpose == stepUpPurpose {
		subject, err := s.oauthsvc.VerifyAssertion(c.Request().Context(), name, s.oauthRedirectURI(name), code,
			auth.OAuthState{State: st.State, Nonce: st.Nonce, Verifier: st.Verifier})
		if err != nil {
			s.audit(c, observability.ActionStepUpGrant, observability.ResultFailure, st.UserID, "oauth_verify_failed")
			return stepUpErrorRedirect(c, returnTo, "step_up_failed")
		}
		uid, uerr := uuid.Parse(st.UserID)
		linked := uerr == nil && s.oauthsvc.SubjectLinkedTo(c.Request().Context(), name, subject, uid)
		return s.completeStepUp(c, returnTo, name, st.UserID, st.SessionID, linked)
	}

	// ONE verification, then the decision. The authorization code is single-use,
	// so the callback gets exactly one exchange and must dispatch on what it
	// finds: what the attempt was FOR (the signed purpose), and whether a
	// session is already signed in in this browser.
	as, err := s.oauthsvc.VerifyIdentity(
		c.Request().Context(), name, s.oauthRedirectURI(name), code,
		auth.OAuthState{State: st.State, Nonce: st.Nonce, Verifier: st.Verifier},
	)
	if err != nil {
		return s.oauthVerifyFailure(c, returnTo, st.Purpose, err)
	}

	// The link branch: connect this identity to the account that STARTED the
	// attempt (sealed uid), never to whichever account the provider names.
	if st.Purpose == oauthLinkPurpose {
		uid, uerr := uuid.Parse(st.UserID)
		if uerr != nil {
			s.audit(c, observability.ActionOAuthLink, observability.ResultFailure, "", "unbound_state")
			return oauthLinkRedirect(c, returnTo, "link_error", "link_failed")
		}
		return s.completeOAuthLink(c, returnTo, uid, as, nil, "link_error")
	}

	// A LOGIN-purpose callback arriving inside a live session is not a login
	// (A05 ruling 3). Before this, it silently switched the browser to whichever
	// account the provider authenticated — measured, from `newcomer` to a
	// freshly created `mfauser`. Treat it as a link for the session that is
	// here: attach an unlinked subject, refuse one that belongs elsewhere, and
	// in neither case mint a session or change which account is signed in.
	if uid, ok := s.linkedAccountForCallback(c); ok {
		return s.completeOAuthLink(c, returnTo, uid, as, nil, "oauth_error")
	}

	sess, err := s.oauthsvc.ResolveAssertion(c.Request().Context(), as, c.Request().UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrOAuthEmailConflict):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_email_conflict")
			return oauthErrorRedirect(c, returnTo, "email_conflict")
		case errors.Is(err, auth.ErrOAuthEmailMissing):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_email_missing")
			return oauthErrorRedirect(c, returnTo, "email_required")
		case errors.Is(err, auth.ErrAccountDisabled):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "account_disabled")
			return oauthErrorRedirect(c, returnTo, "account_disabled")
		case errors.Is(err, auth.ErrOwnerClaimRequired):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "owner_claim_required")
			return oauthErrorRedirect(c, returnTo, "owner_claim_required")
		case errors.Is(err, auth.ErrConflict), errors.Is(err, auth.ErrHandleReserved):
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_conflict")
			return oauthErrorRedirect(c, returnTo, "conflict")
		}
		return err
	}

	// The second factor applies to a provider sign-in exactly as it does to a
	// password one: no session, the same mfa_token, the same challenge endpoint.
	// The token goes into the httpOnly cookie and the URL carries only the flag
	// (see oauth_link.go for why the token is not in the query).
	if sess.MFARequired {
		s.audit(c, observability.ActionLogin, observability.ResultFailure, sess.User.ID.String(), "mfa_required")
		s.setMFAPendingCookie(c, sess.MFAToken)
		return mfaChallengeRedirect(c, returnTo)
	}

	// Audit the concrete outcome; every path is also a login. The reason names
	// the provider, never a token or email.
	if sess.Outcome == auth.OAuthCreated {
		s.audit(c, observability.ActionRegister, observability.ResultSuccess, sess.User.ID.String(), "oauth:"+name)
	}
	s.audit(c, observability.ActionLogin, observability.ResultSuccess, sess.User.ID.String(), "oauth:"+name)

	// A top-level navigation cannot safely receive a bearer token, so OAuth
	// sessions are always cookie-mode: the rotating refresh token travels in
	// the httpOnly vidra_refresh cookie and the SPA obtains its access token
	// via POST /auth/refresh (credentials included).
	s.setRefreshCookie(c, sess.Tokens.RefreshToken)
	return c.Redirect(http.StatusFound, returnTo)
}

// oauthVerifyFailure maps a failed id_token verification. A nonce mismatch is a
// protocol violation and stays a 400 for every purpose; an upstream exchange
// failure is a typed 502 for a login and a redirect code for a link, because
// the browser mid-link is on a settings page that can render an answer, while a
// login lands on a page whose whole job is to show one.
func (s *Server) oauthVerifyFailure(c echo.Context, returnTo, purpose string, err error) error {
	if errors.Is(err, auth.ErrOAuthNonceMismatch) {
		s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_nonce_mismatch")
		return echo.NewHTTPError(http.StatusBadRequest, "oauth nonce mismatch")
	}
	if !errors.Is(err, auth.ErrOAuthExchange) {
		return err
	}
	if purpose == oauthLinkPurpose {
		s.audit(c, observability.ActionOAuthLink, observability.ResultFailure, "", "oauth_exchange_failed")
		return oauthLinkRedirect(c, returnTo, "link_error", "oauth_exchange_failed")
	}
	s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "oauth_exchange_failed")
	return &OAuthUpstreamError{
		Status:  http.StatusBadGateway,
		Code:    "oauth_exchange_failed",
		Message: "the sign-in provider did not complete the exchange, so no session was issued — try signing in again",
	}
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	provider := c.Param("provider")
	if err := s.oauthsvc.Unlink(c.Request().Context(), userID, provider); err != nil {
		switch {
		case errors.Is(err, auth.ErrOAuthIdentityNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "identity not linked")
		case errors.Is(err, auth.ErrOAuthLastCredential):
			s.audit(c, observability.ActionOAuthUnlink, observability.ResultFailure, userID.String(), "last_credential")
			// The remedy has to be one the caller can actually reach. Until
			// A30's step-up shipped it was not: this account is passwordless
			// AND holds an unroutable placeholder address, so "use the password
			// reset" pointed at a mailbox that cannot receive. Name the route
			// that works instead.
			return echo.NewHTTPError(http.StatusUnprocessableEntity,
				"cannot remove the last sign-in method — set a password first with POST /api/v1/auth/me/password/set, which authorises you by re-signing in with this same provider")
		case errors.Is(err, auth.ErrAccountNotFound):
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	s.audit(c, observability.ActionOAuthUnlink, observability.ResultSuccess, userID.String(), "oauth:"+provider)
	return c.NoContent(http.StatusNoContent)
}
