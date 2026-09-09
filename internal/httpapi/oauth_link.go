package httpapi

// Connecting a provider to an account you are ALREADY signed in to, and the
// second factor on a provider sign-in. Both are A05 rulings, and they are one
// file because they are two halves of the same correction: a provider round
// trip stops being a way to acquire somebody else's account, and starts being a
// way to prove something about your own.
//
// WHAT CHANGED, AND WHY IT HAD TO
//
//	1. A provider-asserted email no longer links anything. A05 signed in as the
//	   instance OWNER through a second configured provider that merely asserted
//	   the owner's address `email_verified: true` — no local credential, no
//	   consent, role admin. The rule is now: an account is matched by SUBJECT;
//	   an email that matches an existing account is `email_conflict`.
//	2. …which would have left a real user stranded, because the only linking
//	   mechanism in the product WAS that email match. So linking moves to where
//	   it always belonged: a control in settings, inside a session, using the
//	   step-up slice's own cookie pattern (purpose + uid + sid sealed in the
//	   signed state cookie, core #217).
//	3. And a callback is no longer session-blind. A05 measured a signed-in user
//	   being silently switched to a different account by starting a login flow
//	   and authenticating as somebody else. A login-purpose callback that
//	   arrives inside a live session is now a LINK for that session, or a
//	   refusal — never a switch.
//
// THE MFA TRANSPORT, AND WHY IT IS A COOKIE
//
// The password path can withhold a session because it answers JSON: it returns
// {mfa_required, mfa_token} and the client posts the token back with a code. A
// provider sign-in ends in a top-level browser navigation, which can carry no
// JSON, so the token needs a transport across the redirect. The two candidates
// were a one-time `?mfa=<token>` on the landing URL and a short-lived httpOnly
// cookie; this is the cookie, and the URL carries only the flag `?mfa=required`.
//
// The reason is that the two tokens are not comparable. The step-up assertion
// rides in a query string (`?step_up=<token>`) and that is sound, because it is
// bound to the session it was minted for and authorises nothing without it —
// whoever can read that URL already holds the session. An mfa_token has no such
// backstop: it IS the first factor, standing in for a password already proven,
// and it is bound to no session. A query parameter would put it in the browser
// history, in the `Referer` of every same-origin subresource the landing page
// then fetches, and in the access log of the frontend server and of any reverse
// proxy in front of it — three places a password would never be written. So it
// travels in `vidra_mfa_pending`: httpOnly (no script reads it), SameSite=Lax
// (it must survive the top-level GET back from the provider), scoped to the
// challenge route, and dead in the same five minutes the token itself is.
//
// Single use is enforced where it means something: the cookie is cleared the
// moment it authorises a session, and cleared when the token turns out to be
// invalid or expired (there is nothing left to keep). It is kept ONLY across a
// wrong code, because a typo is not a use and the password path lets you type
// the code again too.

import (
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

// oauthLinkPurpose marks a sealed attempt as "connect this provider to the
// account that started it". The empty purpose is still a login and "step_up"
// is still a step-up, so no cookie sealed before this existed changes meaning.
const oauthLinkPurpose = "link"

// mfaPendingCookieName carries the withheld mfa_token from a provider callback
// to the challenge the login page then completes.
const mfaPendingCookieName = "vidra_mfa_pending"

// mfaPendingCookiePath scopes the cookie to the challenge route and nothing
// else: it is not needed on /auth/refresh, /auth/login or any other request,
// and a cookie that rides requests it cannot authorise is a cookie in more
// logs than necessary.
const mfaPendingCookiePath = "/api/v1/auth/mfa/challenge"

// mfaPendingTTL matches the mfa_token's own five-minute lifetime. Keeping the
// two equal means the browser stops sending a token the server would refuse,
// rather than the two disagreeing about when the challenge died.
const mfaPendingTTL = 5 * time.Minute

// setMFAPendingCookie parks the withheld challenge token for the redirect.
func (s *Server) setMFAPendingCookie(c echo.Context, token string) {
	s.writeStateCookie(c, mfaPendingCookieName, mfaPendingCookiePath, token, mfaPendingTTL)
}

func (s *Server) clearMFAPendingCookie(c echo.Context) {
	s.clearStateCookie(c, mfaPendingCookieName, mfaPendingCookiePath)
}

// mfaPendingToken returns the challenge token the request's cookie carries, or
// "" when there is none.
func mfaPendingToken(c echo.Context) string {
	ck, err := c.Cookie(mfaPendingCookieName)
	if err != nil || ck == nil {
		return ""
	}
	return ck.Value
}

// mfaChallengeRedirect hands the browser back to the validated return_to with
// the `mfa=required` FLAG — never the token, which went into the cookie above.
// The flag is not a secret: it says a challenge is pending in this browser, and
// the login page swaps to the code entry it already has for the password path.
func mfaChallengeRedirect(c echo.Context, returnTo string) error {
	u, err := url.Parse(returnTo)
	if err != nil {
		u = &url.URL{Path: "/"}
	}
	q := u.Query()
	// The success marker would tell the page a session had been issued, and none
	// has. Drop it so a landing cannot claim both.
	q.Del("oauth")
	q.Set("mfa", "required")
	u.RawQuery = q.Encode()
	return c.Redirect(http.StatusFound, u.String())
}

// oauthLinkRedirect hands the browser back with the outcome of a link attempt:
// `?link=<provider>` on success, `?link_error=<code>` otherwise. A link is not a
// sign-in, so it never borrows the login's `oauth_error` codes — the page that
// receives this is the settings page, not the login page (the same reasoning
// step-up applies to its own `step_up_error`).
func oauthLinkRedirect(c echo.Context, returnTo, key, value string) error {
	u, err := url.Parse(returnTo)
	if err != nil {
		u = &url.URL{Path: "/"}
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return c.Redirect(http.StatusFound, u.String())
}

// linkStartRequest is the POST …/link/start body. `handle` is ATProto-only.
type linkStartRequest struct {
	Handle   string `json:"handle"`
	ReturnTo string `json:"return_to"`
}

// linkStartResponse carries the authorization URL for the top-level handoff.
// It is JSON rather than a 302 for the step-up's reason: the request has to
// carry the caller's bearer token, and a top-level navigation cannot send one.
type linkStartResponse struct {
	AuthorizationURL string `json:"authorization_url"`
}

// handleOAuthLinkStart begins "connect this OIDC provider to my account".
//
// The refusals it can make BEFORE the round trip, it makes: an account that
// already has an identity for this provider is 422 here rather than after a
// consent screen. The one it cannot is the subject collision — an OIDC subject
// is not knowable until the id_token comes back — so that one is a redirect
// code at the callback. The ATProto twin below CAN check it early, because
// ATProto resolves its subject (the DID) at start.
func (s *Server) handleOAuthLinkStart(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	sessionID := sessionIDFromContext(c)
	if _, perr := uuid.Parse(sessionID); perr != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "this request is not bound to a session")
	}
	name := c.Param("provider")
	if s.oauthsvc == nil || len(s.oauthsvc.ProviderNames()) == 0 {
		return &OAuthNotConfiguredError{}
	}
	if !s.oauthsvc.Enabled(name) {
		return echo.NewHTTPError(http.StatusNotFound, "unknown oauth provider")
	}
	var in linkStartRequest
	if err := c.Bind(&in); err != nil {
		return &ValidationError{Fields: []FieldError{{Field: "body", Message: "must be a JSON object"}}}
	}
	returnTo, ok := safeReturnPath(in.ReturnTo)
	if !ok {
		return &ValidationError{Fields: []FieldError{
			{Field: "return_to", Message: "must be a same-origin relative path"},
		}}
	}
	if s.oauthsvc.HasProviderLinked(c.Request().Context(), userID, name) {
		return &OAuthProviderLinkedError{}
	}
	redirectURI := s.oauthRedirectURI(name)
	if redirectURI == "" {
		return &OAuthNotConfiguredError{}
	}
	authURL, st, err := s.oauthsvc.BeginAuth(c.Request().Context(), name, redirectURI)
	if err != nil {
		if errors.Is(err, auth.ErrUnknownOAuthProvider) {
			return echo.NewHTTPError(http.StatusNotFound, "unknown oauth provider")
		}
		return &OAuthUpstreamError{
			Status:  http.StatusBadGateway,
			Code:    "oauth_provider_unavailable",
			Message: "this instance could not reach its sign-in provider, so the connection could not start — try again shortly, or ask the administrator to check the provider configuration",
		}
	}
	sealed, err := s.sealOAuthState(oauthStatePayload{
		Provider:  name,
		State:     st.State,
		Nonce:     st.Nonce,
		Verifier:  st.Verifier,
		ReturnTo:  returnTo,
		IssuedAt:  time.Now().Unix(),
		Purpose:   oauthLinkPurpose,
		UserID:    userID.String(),
		SessionID: sessionID,
	})
	if err != nil {
		return err
	}
	s.setOAuthStateCookie(c, sealed)
	return c.JSON(http.StatusOK, linkStartResponse{AuthorizationURL: authURL})
}

// handleATProtoLinkStart is the ATProto twin. It refuses BOTH pre-round-trip
// cases: the account already has a Bluesky identity (422), and the handle
// resolves to a DID somebody else already linked (409). The second is possible
// here and not for OIDC only because ATProto resolves its subject before the
// browser ever leaves.
func (s *Server) handleATProtoLinkStart(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	sessionID := sessionIDFromContext(c)
	if _, perr := uuid.Parse(sessionID); perr != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "this request is not bound to a session")
	}
	if s.atprotologinsvc == nil || !s.atprotologinsvc.Enabled() {
		return &ATProtoLoginError{Status: http.StatusServiceUnavailable, Code: "atproto_disabled", Message: "ATProto login is not enabled on this instance"}
	}
	var in linkStartRequest
	if err := c.Bind(&in); err != nil {
		return &ValidationError{Fields: []FieldError{{Field: "body", Message: "must be a JSON object"}}}
	}
	h := strings.TrimSpace(in.Handle)
	if h == "" || !strings.Contains(h, ".") || strings.ContainsAny(h, " \t") {
		return &ValidationError{Fields: []FieldError{
			{Field: "handle", Message: "must be a domain-style handle (e.g. alice.bsky.social)"},
		}}
	}
	returnTo, ok := safeReturnPath(in.ReturnTo)
	if !ok {
		return &ValidationError{Fields: []FieldError{
			{Field: "return_to", Message: "must be a same-origin relative path"},
		}}
	}
	if s.oauthsvc != nil && s.oauthsvc.HasProviderLinked(c.Request().Context(), userID, auth.ATProtoProviderName) {
		return &OAuthProviderLinkedError{}
	}
	authURL, st, err := s.atprotologinsvc.Begin(c.Request().Context(), h, returnTo)
	if err != nil {
		return atprotoLoginError(err)
	}
	if s.atprotologinsvc.SubjectClaimedByOther(c.Request().Context(), st.DID, userID) {
		s.audit(c, observability.ActionOAuthLink, observability.ResultFailure, userID.String(), "identity_belongs_to_another_account")
		return &OAuthIdentityClaimedError{}
	}
	sealed, err := s.sealATProtoState(atprotoStatePayload{
		ATProtoState: st,
		IssuedAt:     time.Now().Unix(),
		Purpose:      oauthLinkPurpose,
		UserID:       userID.String(),
		SessionID:    sessionID,
	})
	if err != nil {
		return err
	}
	s.setATProtoStateCookie(c, sealed)
	return c.JSON(http.StatusOK, linkStartResponse{AuthorizationURL: authURL})
}

// completeOAuthLink is the shared tail of both callbacks on a link: the
// assertion is already VERIFIED, and what remains is whether it may be attached
// to the bound account.
//
// boundUserID comes from the SIGNED state cookie on a link-purpose attempt, and
// from the live refresh session on a login-purpose attempt that arrived inside
// one. Both are the browser's own account; neither is a request parameter.
func (s *Server) completeOAuthLink(c echo.Context, returnTo string, boundUserID uuid.UUID, as auth.OAuthAssertion, handle *string) error {
	outcome, err := s.oauthsvc.LinkIdentity(c.Request().Context(), boundUserID, as, handle)
	if err != nil {
		code := "link_failed"
		switch {
		case errors.Is(err, auth.ErrOAuthIdentityClaimed):
			code = "identity_belongs_to_another_account"
		case errors.Is(err, auth.ErrOAuthProviderAlreadyLinked):
			code = "provider_already_linked"
		case errors.Is(err, auth.ErrAccountDisabled):
			code = "account_disabled"
		case errors.Is(err, auth.ErrAccountNotFound):
			code = "account_not_found"
		}
		s.audit(c, observability.ActionOAuthLink, observability.ResultFailure, boundUserID.String(), code)
		return oauthLinkRedirect(c, returnTo, "link_error", code)
	}
	if outcome == auth.OAuthLinked {
		s.audit(c, observability.ActionOAuthLink, observability.ResultSuccess, boundUserID.String(), "oauth:"+as.Provider)
	}
	// No session is minted and no refresh cookie is set: the caller already has
	// the session this link belongs to, and issuing a second one on a link would
	// be the account-switch this whole file exists to prevent.
	return oauthLinkRedirect(c, returnTo, "link", as.Provider)
}

// linkedAccountForCallback answers the one question a session-aware callback
// asks: is a session already signed in in THIS browser? The refresh cookie is
// the only credential a top-level navigation from a provider can carry, and it
// is read without rotating it (auth.Service.SessionAccount).
func (s *Server) linkedAccountForCallback(c echo.Context) (uuid.UUID, bool) {
	user, _, ok := s.authsvc.SessionAccount(c.Request().Context(), refreshCookieToken(c))
	if !ok {
		return uuid.Nil, false
	}
	return user.ID, true
}
