package httpapi

// Step-up authentication over HTTP, driven end to end against the same fake
// PDS + authorization server the login tests use (atproto_login_test.go). The
// point of running it here rather than only at the service level is that the
// step-up REUSES the login round trip: the same start, the same signed state
// cookie, the same callback, the same iss/sub/scope invariants. A test that
// stubbed the provider would prove the handlers agree with a stub.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/atproto"
	"github.com/vidra/vidra-core/internal/auth"
)

// stepUpStart POSTs /auth/step-up/start with the caller's bearer token and
// returns the response plus the sealed attempt cookie.
func (e *atprotoLoginEnv) stepUpStart(t *testing.T, token, body string) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/step-up/start", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	e.srv.Handler().ServeHTTP(rec, req)
	var cookie *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == atprotoStateCookieName && ck.Value != "" {
			cookie = ck
		}
	}
	return rec, cookie
}

// signIn runs a full ATProto login and returns the session cookie and a bearer
// token for it — the state every step-up test starts from.
func (e *atprotoLoginEnv) signIn(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	_, cookie := e.start(t, `{"handle":"alice.example","return_to":"/"}`)
	cb := e.callback(t, e.callbackQuery(), cookie)
	session := atprotoSessionCookie(cb)
	if session == nil {
		t.Fatal("login did not set a session cookie")
	}
	// The refresh cookie ROTATES on use, so the caller gets the bearer token
	// and not the spent cookie: reusing the cookie a second time is a 401 by
	// design, and a test that did would be measuring rotation, not step-up.
	return session, e.accessToken(t, session)
}

// meWithBearer reads /auth/me with an access token, the way the SPA does after
// the callback hop.
func (e *atprotoLoginEnv) meWithBearer(t *testing.T, bearer string) userView {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+bearer)
	e.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("me = %d, body=%s", rec.Code, rec.Body.String())
	}
	var u userView
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	return u
}

// callbackQuery is the query a compliant authorization server sends back: the
// state it received during PAR, plus the RFC 9207 iss.
func (e *atprotoLoginEnv) callbackQuery() string {
	return "?code=the-code&state=" + url.QueryEscape(e.backend.parState) +
		"&iss=" + url.QueryEscape(e.backend.srv.URL)
}

// stepUpToken runs a whole step-up round trip and returns the raw token the
// callback handed back in the redirect.
func (e *atprotoLoginEnv) stepUpToken(t *testing.T, bearer string) string {
	t.Helper()
	rec, cookie := e.stepUpStart(t, bearer, `{"provider":"atproto","handle":"alice.example","return_to":"/settings/security"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("step-up start = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cookie == nil {
		t.Fatal("step-up start did not seal an attempt cookie")
	}
	cb := e.callback(t, e.callbackQuery(), cookie)
	if cb.Code != http.StatusFound {
		t.Fatalf("step-up callback = %d, want 302; body=%s", cb.Code, cb.Body.String())
	}
	loc, err := url.Parse(cb.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Path != "/settings/security" {
		t.Fatalf("callback landed on %q, want /settings/security", loc.Path)
	}
	// A step-up callback must NOT mint a session: the caller already has one,
	// and silently rotating it would sign the browser into a second identity
	// if the provider account had drifted.
	if atprotoSessionCookie(cb) != nil {
		t.Error("the step-up callback issued a session cookie; it must only mint an assertion")
	}
	tok := loc.Query().Get("step_up")
	if tok == "" {
		t.Fatalf("callback carried no step_up token: %q", cb.Header().Get("Location"))
	}
	return tok
}

func (e *atprotoLoginEnv) postJSON(t *testing.T, path, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if bearer != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+bearer)
	}
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) ErrorResponse {
	t.Helper()
	var er ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil {
		t.Fatalf("decoding error envelope from %q: %v", rec.Body.String(), err)
	}
	return er
}

// TestStepUpSetPasswordEndToEnd is A30's dead end, closed and measured: an
// ATProto-created account starts with one credential and no way to a second,
// and ends this test with a password AND the ability to unlink the provider.
func TestStepUpSetPasswordEndToEnd(t *testing.T) {
	env := newATProtoLoginEnv(t, true)
	_, bearer := env.signIn(t)

	// The account's own view now SAYS it is in the unrecoverable shape, which
	// is what lets the settings page prompt rather than leaving the user to
	// discover it when they lose the Bluesky account.
	me := env.meWithBearer(t, bearer)
	if me.HasPassword {
		t.Error("a fresh ATProto account reports has_password true")
	}
	if !me.EmailPlaceholder {
		t.Errorf("email_placeholder false for %q", me.Email)
	}

	// Without an assertion the route refuses, and NAMES the remedy.
	rec := env.postJSON(t, "/api/v1/auth/me/password/set", bearer, `{"new_password":"a-brand-new-password"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("set with no token = %d, want 422 (missing field); body=%s", rec.Code, rec.Body.String())
	}
	rec = env.postJSON(t, "/api/v1/auth/me/password/set", bearer, `{"new_password":"a-brand-new-password","step_up_token":"not-a-real-token"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("set with a bogus token = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	er := decodeError(t, rec)
	if er.Error.Code != "step_up_required" {
		t.Errorf("code = %q, want step_up_required", er.Error.Code)
	}
	if strings.Join(er.Error.StepUpProviders, ",") != "atproto" {
		t.Errorf("step_up_providers = %v, want [atproto]", er.Error.StepUpProviders)
	}

	// A provider the account never linked is refused BEFORE the round trip.
	rec, _ = env.stepUpStart(t, bearer, `{"provider":"google","return_to":"/settings/security"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("step-up with an unlinked provider = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Error.Code; got != "step_up_provider_not_linked" {
		t.Errorf("code = %q, want step_up_provider_not_linked", got)
	}

	// The real round trip.
	token := env.stepUpToken(t, bearer)
	rec = env.postJSON(t, "/api/v1/auth/me/password/set", bearer, `{"new_password":"a-brand-new-password","step_up_token":"`+token+`"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("set password = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// Read it back through the account's own view, and prove the credential is
	// real by logging in with it.
	me = env.meWithBearer(t, bearer)
	if !me.HasPassword {
		t.Error("has_password still false after the password was set")
	}
	login := env.postJSON(t, "/api/v1/auth/login", "",
		`{"identifier":"alice","password":"a-brand-new-password"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("password login after set = %d, want 200; body=%s", login.Code, login.Body.String())
	}

	// Single use: the same assertion cannot be spent again. (The account now
	// has a password, so the routing answer is what it gets — and that answer
	// is itself the SC5 negative "a user with a password → 422 on set".)
	rec = env.postJSON(t, "/api/v1/auth/me/password/set", bearer, `{"new_password":"yet-another-password","step_up_token":"`+token+`"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("re-set = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Error.Code; got != "password_already_set" {
		t.Errorf("code = %q, want password_already_set", got)
	}

	// SC3: the unlink refusal's remedy is now reachable, and taken.
	del := httptest.NewRequest(http.MethodDelete, "/api/v1/me/oauth-identities/atproto", nil)
	del.Header.Set(echo.HeaderAuthorization, "Bearer "+bearer)
	unlink := httptest.NewRecorder()
	env.srv.Handler().ServeHTTP(unlink, del)
	if unlink.Code != http.StatusNoContent {
		t.Fatalf("unlink after a password was set = %d, want 204; body=%s", unlink.Code, unlink.Body.String())
	}
}

// TestStepUpRefusalNamesTheReachableRemedy: the 422 that protects the last
// sign-in method used to point at the password-reset flow, which for this
// account shape can never complete. It has to name the route that works.
func TestStepUpRefusalNamesTheReachableRemedy(t *testing.T) {
	env := newATProtoLoginEnv(t, true)
	_, bearer := env.signIn(t)

	del := httptest.NewRequest(http.MethodDelete, "/api/v1/me/oauth-identities/atproto", nil)
	del.Header.Set(echo.HeaderAuthorization, "Bearer "+bearer)
	rec := httptest.NewRecorder()
	env.srv.Handler().ServeHTTP(rec, del)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unlink last method = %d, want 422", rec.Code)
	}
	msg := decodeError(t, rec).Error.Message
	if !strings.Contains(msg, "/api/v1/auth/me/password/set") {
		t.Errorf("the refusal does not name a reachable remedy: %q", msg)
	}
}

// TestStepUpTokenIsBoundToItsSession: the assertion authorises the browser that
// earned it and nothing else. Two live sessions for the SAME account is the
// sharpest form of the test — the user id matches, so only the session binding
// can refuse it.
func TestStepUpTokenIsBoundToItsSession(t *testing.T) {
	env := newATProtoLoginEnv(t, true)
	_, first := env.signIn(t)
	_, second := env.signIn(t)
	if first == second {
		t.Fatal("the two sign-ins produced the same access token")
	}

	token := env.stepUpToken(t, first)
	rec := env.postJSON(t, "/api/v1/auth/me/password/set", second, `{"new_password":"a-brand-new-password","step_up_token":"`+token+`"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("another session spending the assertion = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Error.Code; got != "step_up_required" {
		t.Errorf("code = %q, want step_up_required", got)
	}
	// The legitimate holder's proof survived the theft attempt.
	ok := env.postJSON(t, "/api/v1/auth/me/password/set", first, `{"new_password":"a-brand-new-password","step_up_token":"`+token+`"}`)
	if ok.Code != http.StatusNoContent {
		t.Fatalf("the owning session = %d, want 204; body=%s", ok.Code, ok.Body.String())
	}
}

// TestStepUpRequiresAuthentication: every route in the family refuses an
// anonymous caller, because there is no session to bind an assertion to.
func TestStepUpRequiresAuthentication(t *testing.T) {
	env := newATProtoLoginEnv(t, true)
	for _, tc := range []struct{ path, body string }{
		{"/api/v1/auth/step-up/start", `{"provider":"atproto","handle":"alice.example"}`},
		{"/api/v1/auth/me/password/set", `{"new_password":"a-brand-new-password","step_up_token":"x"}`},
	} {
		rec := env.postJSON(t, tc.path, "", tc.body)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s = %d, want 401; body=%s", tc.path, rec.Code, rec.Body.String())
		}
	}
}

// TestStepUpStartRefusedWhenProviderDisabled: an instance that turns ATProto
// login off must say so with the typed 503, not fail as if the handle were
// wrong — otherwise the settings page tells the user to fix their handle for a
// challenge that can never complete.
//
// The second server shares the first's accounts, sessions and repository, which
// is the only honest way to model "the flag changed after I signed in".
func TestStepUpStartRefusedWhenProviderDisabled(t *testing.T) {
	env := newATProtoLoginEnv(t, true)
	_, bearer := env.signIn(t)

	disabled := env.twinWithATProtoEnabled(t, false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/step-up/start",
		strings.NewReader(`{"provider":"atproto","handle":"alice.example","return_to":"/settings/security"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+bearer)
	disabled.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("step-up start with ATProto login off = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Error.Code; got != "atproto_disabled" {
		t.Errorf("code = %q, want atproto_disabled", got)
	}
}

// twinWithATProtoEnabled stands a second Server over the SAME auth service and
// repository with a different ATPROTO_LOGIN_ENABLED.
func (e *atprotoLoginEnv) twinWithATProtoEnabled(t *testing.T, enabled bool) *Server {
	t.Helper()
	client := atproto.NewOAuthClient(
		atproto.WithOAuthClientAllowPrivate(true),
		atproto.WithTXTResolver(func(_ context.Context, name string) ([]string, error) {
			if name == "_atproto."+e.backend.handle {
				return []string{"did=" + e.backend.did}, nil
			}
			return nil, nil
		}),
		atproto.WithPLCURL(e.backend.srv.URL),
	)
	loginsvc := auth.NewATProtoOAuthService(e.repo, e.authsvc, client,
		auth.WithATProtoEnabled(enabled),
		auth.WithATProtoPublicBaseURL(e.cfg.PublicBaseURL),
	)
	return New(e.cfg, nil, nil,
		WithAuthService(e.authsvc, 15*time.Minute),
		WithATProtoLoginService(loginsvc),
		WithOAuthService(auth.NewOAuthService(e.repo, e.authsvc, nil)),
		WithContactMailer(e.mailer),
		WithLogger(slog.New(slog.NewTextHandler(discardWriter{}, nil))),
	)
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestStepUpEmailChangeFromPlaceholder is SC2 over HTTP: the account moves off
// its unroutable synthetic address without ever supplying a password, and the
// confirmation goes to the NEW mailbox — the one proof a step-up does not
// replace.
func TestStepUpEmailChangeFromPlaceholder(t *testing.T) {
	env := newATProtoLoginEnv(t, true)
	_, bearer := env.signIn(t)

	// The shipped password path refuses this account, as it always did.
	rec := env.postJSON(t, "/api/v1/auth/me/email-change", bearer,
		`{"current_password":"anything","new_email":"alice@example.test"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("password path on a passwordless account = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	// Supplying BOTH proofs is refused: it must never be ambiguous which one
	// authorised the change, or which one got spent.
	rec = env.postJSON(t, "/api/v1/auth/me/email-change", bearer,
		`{"current_password":"anything","step_up_token":"x","new_email":"alice@example.test"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("both proofs = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}

	token := env.stepUpToken(t, bearer)
	rec = env.postJSON(t, "/api/v1/auth/me/email-change", bearer,
		`{"step_up_token":"`+token+`","new_email":"alice@example.test"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("step-up email change = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if env.mailer.changeTo != "alice@example.test" {
		t.Fatalf("confirmation went to %q, want the NEW address", env.mailer.changeTo)
	}
	// Unchanged until confirmed.
	if me := env.meWithBearer(t, bearer); me.Email != "did-plc-alice@atproto.invalid" {
		t.Errorf("the live address moved before confirmation: %q", me.Email)
	}

	rec = env.postJSON(t, "/api/v1/auth/me/email-change/confirm", bearer,
		`{"token":"`+env.mailer.changeToken+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	me := env.meWithBearer(t, bearer)
	if me.Email != "alice@example.test" || me.EmailPlaceholder {
		t.Errorf("after confirmation: email=%q placeholder=%v", me.Email, me.EmailPlaceholder)
	}
	// The account is verified by the same statement that moved it, so the
	// address is a real recovery path now, not merely a nicer string.
	if !me.EmailVerified {
		t.Error("the confirmed address is not marked verified")
	}
	// No notice was aimed at the unroutable old address.
	if env.mailer.changeNotices != 0 {
		t.Errorf("%d change notices were sent; the old address was unroutable", env.mailer.changeNotices)
	}
}
