package httpapi

// Step-up authentication: proving you are the account holder with the provider
// that IS your credential, for accounts that have no other proof.
//
// A30 measured the dead end this closes. An account created by ATProto (or
// OIDC) login is passwordless and holds a synthetic …@atproto.invalid address,
// so the two proofs every sensitive self-service action rests on — the current
// password, and a link mailed to the account's address — are BOTH unavailable
// to it. The unlink refusal that protects its only sign-in method says "set a
// password first"; nothing could. Lose the provider account and the Vidra
// account went with it.
//
// The flow, deliberately built on the login round trip rather than beside it:
//
//	POST /api/v1/auth/step-up/start        (authenticated) resolves the provider,
//	    runs the SAME Begin the login does, and seals the attempt into the SAME
//	    signed httpOnly cookie — with a step_up purpose plus the caller's user
//	    and session ids bound into the sealed payload. Returns
//	    {authorization_url} for a top-level navigation.
//	GET  …/auth/atproto/callback | …/auth/oauth/:provider/callback
//	    on a step_up payload, verify the assertion exactly as a login does, then
//	    — instead of minting a session — check the attested subject belongs to
//	    the bound account and issue a single-use step-up token, redirecting to
//	    return_to with ?step_up=<token> (or ?step_up_error=<code>).
//	POST /api/v1/auth/me/password/set      spends that token to set a first
//	    password; POST …/me/email-change accepts it in place of a password.
//
// Why the raw token rides in the redirect query. It is bound to the SESSION
// that started the challenge, so it authorises nothing in any other browser:
// whoever can read this browser's history already holds the session the token
// is useless without. It is also single-use and dead in ten minutes, and the
// settings page strips it from the URL on arrival. The alternative — a second
// httpOnly cookie plus an exchange endpoint — buys nothing against an attacker
// who has the browser, and costs a route.

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

// stepUpPurpose marks a sealed login attempt as a step-up rather than a login.
// The empty purpose is a login, so every cookie sealed before this existed —
// and every login sealed after it — keeps its meaning unchanged.
const stepUpPurpose = "step_up"

// stepUpStartRequest is the POST /api/v1/auth/step-up/start body.
type stepUpStartRequest struct {
	// Provider is "atproto" or a configured OIDC provider name. It must be one
	// the CALLER has linked: a round trip through a provider this account never
	// linked proves nothing about this account.
	Provider string `json:"provider"`
	// Handle is the ATProto handle to re-authenticate with (ATProto only).
	Handle string `json:"handle"`
	// ReturnTo is where to send the browser afterwards (same-origin path).
	ReturnTo string `json:"return_to"`
}

func (r stepUpStartRequest) Validate() []FieldError {
	var fes []FieldError
	if strings.TrimSpace(r.Provider) == "" {
		fes = append(fes, FieldError{Field: "provider", Message: "is required"})
	}
	return fes
}

// stepUpStartResponse carries the authorization URL for the top-level handoff,
// exactly like the login start does.
type stepUpStartResponse struct {
	AuthorizationURL string `json:"authorization_url"`
}

// handleStepUpStart begins a step-up challenge for the authenticated caller.
//
// The provider must already be linked to the account. That check is here, at
// the START, and not only at the callback: beginning a challenge that could
// never authorise anything would send the user through a whole consent screen
// to be refused at the end of it.
func (s *Server) handleStepUpStart(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	sessionID := sessionIDFromContext(c)
	if _, perr := uuid.Parse(sessionID); perr != nil {
		// Every authenticated request carries a session id; a caller that does
		// not cannot be bound to one, and an unbound assertion is not a
		// step-up. Refused rather than issued unbound.
		return echo.NewHTTPError(http.StatusUnauthorized, "this request is not bound to a session")
	}
	var in stepUpStartRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	returnTo, ok := safeReturnPath(in.ReturnTo)
	if !ok {
		return &ValidationError{Fields: []FieldError{
			{Field: "return_to", Message: "must be a same-origin relative path"},
		}}
	}
	provider := strings.TrimSpace(in.Provider)
	if !s.authsvc.HasLinkedProvider(c.Request().Context(), userID, provider) {
		return &StepUpNotLinkedError{Provider: provider}
	}

	if provider == auth.ATProtoProviderName {
		return s.startATProtoStepUp(c, userID, sessionID, in.Handle, returnTo)
	}
	return s.startOAuthStepUp(c, userID, sessionID, provider, returnTo)
}

// startATProtoStepUp seals a step-up ATProto attempt into the login flow's own
// state cookie and hands back the authorization URL.
func (s *Server) startATProtoStepUp(c echo.Context, userID uuid.UUID, sessionID, handle, returnTo string) error {
	if s.atprotologinsvc == nil {
		return &ATProtoLoginError{Status: http.StatusServiceUnavailable, Code: "atproto_disabled", Message: "ATProto login is not enabled on this instance"}
	}
	h := strings.TrimSpace(handle)
	if h == "" || !strings.Contains(h, ".") || strings.ContainsAny(h, " \t") {
		return &ValidationError{Fields: []FieldError{
			{Field: "handle", Message: "must be a domain-style handle (e.g. alice.bsky.social)"},
		}}
	}
	authURL, st, err := s.atprotologinsvc.Begin(c.Request().Context(), h, returnTo)
	if err != nil {
		// Includes the typed 503 when ATPROTO_LOGIN_ENABLED is off: a disabled
		// provider must say so, not fail as if the handle were wrong.
		return atprotoLoginError(err)
	}
	sealed, err := s.sealATProtoState(atprotoStatePayload{
		ATProtoState: st,
		IssuedAt:     time.Now().Unix(),
		Purpose:      stepUpPurpose,
		UserID:       userID.String(),
		SessionID:    sessionID,
	})
	if err != nil {
		return err
	}
	s.setATProtoStateCookie(c, sealed)
	return c.JSON(http.StatusOK, stepUpStartResponse{AuthorizationURL: authURL})
}

// startOAuthStepUp is the OIDC twin. It answers with the authorization URL as
// JSON rather than the login's 302, because the request that starts it must
// carry the caller's bearer token — which a top-level navigation cannot send.
func (s *Server) startOAuthStepUp(c echo.Context, userID uuid.UUID, sessionID, provider, returnTo string) error {
	if s.oauthsvc == nil || !s.oauthsvc.Enabled(provider) {
		return echo.NewHTTPError(http.StatusNotFound, "unknown oauth provider")
	}
	redirectURI := s.oauthRedirectURI(provider)
	if redirectURI == "" {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "oauth is not configured on this instance")
	}
	authURL, st, err := s.oauthsvc.BeginAuth(c.Request().Context(), provider, redirectURI)
	if err != nil {
		if errors.Is(err, auth.ErrUnknownOAuthProvider) {
			return echo.NewHTTPError(http.StatusNotFound, "unknown oauth provider")
		}
		return echo.NewHTTPError(http.StatusBadGateway, "oauth provider is unavailable")
	}
	sealed, err := s.sealOAuthState(oauthStatePayload{
		Provider:  provider,
		State:     st.State,
		Nonce:     st.Nonce,
		Verifier:  st.Verifier,
		ReturnTo:  returnTo,
		IssuedAt:  time.Now().Unix(),
		Purpose:   stepUpPurpose,
		UserID:    userID.String(),
		SessionID: sessionID,
	})
	if err != nil {
		return err
	}
	s.setOAuthStateCookie(c, sealed)
	return c.JSON(http.StatusOK, stepUpStartResponse{AuthorizationURL: authURL})
}

// completeStepUp is the shared tail of both callbacks once the provider
// assertion has been VERIFIED: it confirms the attested subject belongs to the
// bound account, mints the single-use token, and redirects.
//
// The subject check is what makes this a step-up and not merely a sign-in: a
// completed round trip with somebody else's Bluesky account proves that person
// controls that account, and nothing whatsoever about this one.
func (s *Server) completeStepUp(c echo.Context, returnTo, provider, boundUserID, boundSessionID string, linked bool) error {
	userID, uerr := uuid.Parse(boundUserID)
	sessionID, serr := uuid.Parse(boundSessionID)
	if uerr != nil || serr != nil {
		s.audit(c, observability.ActionStepUpGrant, observability.ResultFailure, "", "unbound_state")
		return stepUpErrorRedirect(c, returnTo, "step_up_failed")
	}
	if !linked {
		s.audit(c, observability.ActionStepUpGrant, observability.ResultFailure, userID.String(), "identity_mismatch")
		return stepUpErrorRedirect(c, returnTo, "step_up_identity_mismatch")
	}
	grant, err := s.authsvc.IssueStepUp(c.Request().Context(), userID, sessionID, provider)
	if err != nil {
		s.audit(c, observability.ActionStepUpGrant, observability.ResultFailure, userID.String(), "issue_failed")
		return stepUpErrorRedirect(c, returnTo, "step_up_failed")
	}
	s.audit(c, observability.ActionStepUpGrant, observability.ResultSuccess, userID.String(), provider)
	return s.stepUpSuccessRedirect(c, returnTo, grant.Token)
}

// THE STEP-UP TRANSPORT, AND WHY IT IS A COOKIE
//
// The assertion used to ride back as `?step_up=<token>` on the landing URL, and
// the argument for that was that a step-up token is worthless without the
// session it is bound to — whoever can read the URL already holds the session.
// The auth rehearsal put a real logging proxy in front of the flow and measured
// what the argument costs: `step_up=<32 bytes>` appears in the proxy's access
// log and in the `Referer` header of every same-origin subresource the landing
// page then fetches, while `vidra_mfa_pending` — the same shape of secret, one
// route away — appears in none of them, because the proxy logs
// `"Cookie":["REDACTED"]`.
//
// "Bound to a session" is a bound on the blast radius, not a reason to write a
// credential into three log files. The two transports were visible side by side
// in one access log, and only one of them wrote anything down. So the assertion
// travels the way the mfa_token already does: `vidra_step_up`, httpOnly (no
// script reads it, and the frontend now reads nothing from the URL), SameSite
// Lax (it must survive the top-level GET back from the provider), scoped to the
// two routes that can spend it, and dead in the same ten minutes the row is.
//
// The landing URL carries only the `?secure=password` the caller put in its own
// return_to. There is no flag beside it: the page shows the form it was asked
// for, and an absent or spent assertion is answered by the route, as 403
// `step_up_required` — which the page already renders.
const stepUpCookieName = "vidra_step_up"

// stepUpCookiePath is the narrowest prefix BOTH consumers share:
// POST /api/v1/auth/me/password/set and POST /api/v1/auth/me/email-change. It
// is deliberately not "/" — a cookie that rides requests it cannot authorise is
// a cookie in more logs than necessary, which is the whole finding.
const stepUpCookiePath = "/api/v1/auth/me"

// setStepUpCookie parks the granted assertion for the redirect back.
func (s *Server) setStepUpCookie(c echo.Context, token string) {
	s.writeStateCookie(c, stepUpCookieName, stepUpCookiePath, token, auth.StepUpTTL)
}

func (s *Server) clearStepUpCookie(c echo.Context) {
	s.clearStateCookie(c, stepUpCookieName, stepUpCookiePath)
}

// stepUpCookieToken returns the assertion the request's cookie carries, or ""
// when there is none.
func stepUpCookieToken(c echo.Context) string {
	ck, err := c.Cookie(stepUpCookieName)
	if err != nil || ck == nil {
		return ""
	}
	return ck.Value
}

// stepUpTokenFor resolves the assertion a credential-writing request presents:
// the cookie first, then the body field.
//
// The body field survives for the API client that has no browser — it starts
// the step-up itself and reads the token off the Set-Cookie header — and
// because a client that already sends it must not break on the day the cookie
// arrives. The COOKIE wins when both are present: it is the one this instance
// just issued, and preferring the caller's copy would let a stale token in a
// body silently outrank a fresh grant.
func stepUpTokenFor(c echo.Context, fromBody string) string {
	if ck := stepUpCookieToken(c); ck != "" {
		return ck
	}
	return strings.TrimSpace(fromBody)
}

// stepUpSuccessRedirect parks the assertion in the cookie and hands the browser
// back to the validated return_to UNCHANGED — no token, and no flag either.
func (s *Server) stepUpSuccessRedirect(c echo.Context, returnTo, token string) error {
	s.setStepUpCookie(c, token)
	u, err := url.Parse(returnTo)
	if err != nil {
		u = &url.URL{Path: "/"}
	}
	// A failed attempt earlier in this browser may have left step_up_error on
	// the URL the caller round-tripped; a success must not land carrying it.
	q := u.Query()
	q.Del("step_up")
	q.Del("step_up_error")
	u.RawQuery = q.Encode()
	return c.Redirect(http.StatusFound, u.String())
}

// stepUpErrorRedirect hands the browser back with a stable machine code. It
// never carries a provider URL, subject, or token.
func stepUpErrorRedirect(c echo.Context, returnTo, code string) error {
	u, err := url.Parse(returnTo)
	if err != nil {
		u = &url.URL{Path: "/"}
	}
	q := u.Query()
	q.Set("step_up_error", code)
	u.RawQuery = q.Encode()
	return c.Redirect(http.StatusFound, u.String())
}

// setPasswordRequest is the POST /api/v1/auth/me/password/set body. There is no
// current_password field BY DESIGN: this route exists only for accounts that
// have none, and it refuses (422) any account that does.
type setPasswordRequest struct {
	NewPassword string `json:"new_password"`
	// StepUpToken is the single-use assertion from a completed provider round
	// trip — the proof that stands in for the password this account cannot
	// supply.
	StepUpToken string `json:"step_up_token"`
}

// Validate does NOT require step_up_token: since the assertion moved onto the
// httpOnly `vidra_step_up` cookie the browser's request carries no such field,
// and a 422 naming one would be an instruction the page cannot follow. A
// request with no assertion at all is answered where the assertion is checked,
// as 403 `step_up_required` — the same answer a spent or expired one gets.
func (r setPasswordRequest) Validate() []FieldError {
	var fes []FieldError
	// The SAME policy registration, the reset and the password change enforce —
	// one rule for what a vidra password may be, wherever it is set.
	switch {
	case len(r.NewPassword) < 8:
		fes = append(fes, FieldError{Field: "new_password", Message: "must be at least 8 characters"})
	case len(r.NewPassword) > maxPasswordLen:
		fes = append(fes, FieldError{Field: "new_password", Message: "must be at most 72 characters"})
	}
	return fes
}

// handleSetPassword gives a password-less account its first password. Behind
// requireAuth AND the strict auth limiter, like every other credential-writing
// route: the step-up token is a guessing surface, thin as it is (256 bits,
// single-use, ten minutes, session-bound).
//
// 204 on success, and every OTHER session is revoked — the password change's
// semantics exactly, because the consequence is the same. 422
// `password_already_set` when the account has one (the change route is the
// door). 403 `step_up_required`, naming the providers that can satisfy it,
// when the assertion is missing, spent, expired, or belongs to another session
// or account.
func (s *Server) handleSetPassword(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in setPasswordRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	token := stepUpTokenFor(c, in.StepUpToken)
	if err := s.authsvc.SetPassword(c.Request().Context(), userID,
		in.NewPassword, token, sessionIDFromContext(c)); err != nil {
		return s.setPasswordError(c, userID, err)
	}
	// Single use, enforced where it can be seen. The ROW is spent by the
	// statement that read it; clearing the cookie is what stops the browser
	// sending a token this instance would now refuse.
	s.clearStepUpCookie(c)
	s.audit(c, observability.ActionPasswordSet, observability.ResultSuccess, userID.String(), "")
	return c.NoContent(http.StatusNoContent)
}

// setPasswordError maps the service sentinels, auditing the refusal by RULE —
// never the password, never the token.
func (s *Server) setPasswordError(c echo.Context, userID uuid.UUID, err error) error {
	switch {
	case errors.Is(err, auth.ErrPasswordAlreadySet):
		s.audit(c, observability.ActionPasswordSet, observability.ResultFailure, userID.String(), "password_already_set")
		return &PasswordAlreadySetError{}
	case errors.Is(err, auth.ErrStepUpRequired):
		// Missing, spent, expired, or another session's. In every one of those
		// the cookie holds nothing that will ever work again, so it goes — the
		// alternative is a browser that re-presents a dead token on every
		// attempt for the next ten minutes.
		s.clearStepUpCookie(c)
		s.audit(c, observability.ActionPasswordSet, observability.ResultFailure, userID.String(), "step_up_required")
		return &StepUpRequiredError{Providers: s.authsvc.StepUpProvidersFor(c.Request().Context(), userID)}
	case errors.Is(err, auth.ErrAccountNotFound):
		return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
	}
	return err
}
