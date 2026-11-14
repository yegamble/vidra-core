package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/instancesettings"
)

// TestSignupMustAcceptTheConfiguredDocuments: while the instance has terms or a
// privacy policy configured, POST /auth/register without terms_accepted is a
// 422 on that field and creates nothing; with it, the account is created and
// what was accepted — each URL and the operator's version label — is stored
// with it. With nothing configured the field is ignored.
func TestSignupMustAcceptTheConfiguredDocuments(t *testing.T) {
	srv := videoServer(t)
	repo := authRepoBySrv[srv]
	adminTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if got := repo.created[len(repo.created)-1]; len(got.ConsentDocuments) != 0 {
		t.Fatalf("with no documents configured the signup recorded %v", got.ConsentDocuments)
	}
	patchSettings(t, srv, adminTok, `{"terms_url":"https://example.test/terms","terms_version":"2026-11-12","privacy_url":"https://example.test/privacy","registration_minimum_age":16}`)

	inst := publicInstance(t, srv)
	if inst.TermsURL == "" || inst.TermsVersion != "2026-11-12" || inst.PrivacyURL == "" || inst.PrivacyVersion != "" {
		t.Fatalf("instance documents = %q %q / %q %q", inst.TermsURL, inst.TermsVersion, inst.PrivacyURL, inst.PrivacyVersion)
	}

	accounts := len(repo.created)
	for name, body := range map[string]string{
		"absent": `{"username":"bob","email":"bob@example.test","password":"supersecret","age_attestation":true}`,
		"false":  `{"username":"bob","email":"bob@example.test","password":"supersecret","age_attestation":true,"terms_accepted":false}`,
	} {
		rec := postTo(srv, "/api/v1/auth/register", body)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("terms_accepted %s: register = %d, want 422; body=%s", name, rec.Code, rec.Body.String())
		}
		var env ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if len(env.Error.Fields) != 1 || env.Error.Fields[0].Field != "terms_accepted" {
			t.Fatalf("terms_accepted %s: fields = %+v, want one terms_accepted error", name, env.Error.Fields)
		}
	}
	if len(repo.created) != accounts {
		t.Fatalf("a refused signup created an account")
	}

	rec := postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret","age_attestation":true,"terms_accepted":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register with the acceptance = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	got := repo.created[len(repo.created)-1]
	if got.ConsentSource != "signup" ||
		!slices.Equal(got.ConsentDocuments, []string{"terms", "privacy", "age"}) ||
		!slices.Equal(got.ConsentVersions, []string{"2026-11-12", "", ""}) ||
		!slices.Equal(got.ConsentUrls, []string{"https://example.test/terms", "https://example.test/privacy", ""}) ||
		!slices.Equal(got.ConsentMinimumAges, []int32{0, 0, 16}) {
		t.Fatalf("recorded consent = %q %v %v %v %v", got.ConsentSource, got.ConsentDocuments, got.ConsentVersions, got.ConsentUrls, got.ConsentMinimumAges)
	}
}

// With approval required, the applicant accepts when they apply: the request is
// refused without terms_accepted and otherwise keeps what was accepted.
func TestQueuedSignupMustAcceptTheConfiguredDocuments(t *testing.T) {
	srv := videoServer(t)
	repo := authRepoBySrv[srv]
	adminTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	patchSettings(t, srv, adminTok, `{"terms_url":"https://example.test/terms","terms_version":"v1","registration_require_approval":true}`)

	rec := postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("queued signup without the acceptance = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.requested) != 0 {
		t.Fatalf("a request was filed without the acceptance")
	}
	// The minimum age is asked of a queued signup too.
	patchSettings(t, srv, adminTok, `{"registration_minimum_age":18}`)
	rec = postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret","terms_accepted":true}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"age_attestation"`) {
		t.Fatalf("queued signup without the attestation = %d %s, want a 422 on age_attestation", rec.Code, rec.Body.String())
	}
	if len(repo.requested) != 0 {
		t.Fatalf("a request was filed without the attestation")
	}
	patchSettings(t, srv, adminTok, `{"registration_minimum_age":null}`)
	rec = postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret","terms_accepted":true}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("queued signup with the acceptance = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	got := repo.requested[0]
	if !got.ConsentRecorded || got.ConsentTermsUrl == nil || *got.ConsentTermsUrl != "https://example.test/terms" ||
		got.ConsentTermsVersion == nil || *got.ConsentTermsVersion != "v1" || got.ConsentPrivacyUrl != nil || got.ConsentMinimumAge != nil {
		t.Fatalf("the request kept %+v", got)
	}
}

// TestOAuthFirstSignInCarriesTheAcceptance drives the provider flow end to end
// on an instance with terms configured: a first sign-in begun without
// ?terms_accepted=1 creates nothing and lands back with
// oauth_error=terms_required; begun with it, the account is created and the
// acceptance recorded; and the same person signing in again is never asked.
func TestOAuthFirstSignInCarriesTheAcceptance(t *testing.T) {
	terms := auth.SignupTerms{TermsURL: "https://example.test/terms", TermsVersion: "2026-11-12"}
	env := newOAuthEnv(t, auth.WithSignupTermsFunc(func() auth.SignupTerms { return terms }))
	claims := map[string]any{"sub": "sub-200", "email": "bob@example.com", "email_verified": true, "name": "Bob"}
	flow := func(query string) *httptest.ResponseRecorder {
		t.Helper()
		loc, stateCookie := env.begin(t, query)
		env.fake.setNonce(loc.Query().Get("nonce"))
		env.fake.setClaims(claims)
		return env.callback(t, "?code=fake-code&state="+url.QueryEscape(loc.Query().Get("state")), stateCookie)
	}

	rec := flow("?return_to=" + url.QueryEscape("/signup"))
	if got := rec.Header().Get("Location"); rec.Code != http.StatusFound || got != "/signup?oauth_error=terms_required" {
		t.Fatalf("first sign-in without the tick = %d → %q, want 302 → /signup?oauth_error=terms_required", rec.Code, got)
	}
	if sessionCookieFrom(rec) != nil {
		t.Fatal("a refused first sign-in must not set a session")
	}
	if len(env.repo.created) != 0 {
		t.Fatalf("a refused first sign-in created an account")
	}

	rec = flow("?terms_accepted=1&return_to=" + url.QueryEscape("/welcome"))
	if got := rec.Header().Get("Location"); rec.Code != http.StatusFound || got != "/welcome" {
		t.Fatalf("first sign-in with the tick = %d → %q, want 302 → /welcome (body=%s)", rec.Code, got, rec.Body.String())
	}
	if len(env.repo.created) != 1 {
		t.Fatalf("accounts created = %d, want 1", len(env.repo.created))
	}
	if got := env.repo.created[0]; got.ConsentSource != "oidc" || !slices.Equal(got.ConsentDocuments, []string{"terms"}) ||
		!slices.Equal(got.ConsentVersions, []string{"2026-11-12"}) || !slices.Equal(got.ConsentUrls, []string{"https://example.test/terms"}) {
		t.Fatalf("recorded %q %v %v %v", got.ConsentSource, got.ConsentDocuments, got.ConsentVersions, got.ConsentUrls)
	}

	// An existing account signs in with no tick at all.
	rec = flow("")
	if got := rec.Header().Get("Location"); rec.Code != http.StatusFound || got != "/" || sessionCookieFrom(rec) == nil {
		t.Fatalf("returning sign-in without the tick = %d → %q, want a session and 302 → /", rec.Code, got)
	}
	if len(env.repo.created) != 1 {
		t.Fatalf("a returning sign-in created an account")
	}
}

// TestOAuthFirstSignInMustAttestTheAge drives the OIDC flow on an instance with
// a minimum age: a first sign-in begun without ?age_attestation=1 creates
// nothing and lands back with oauth_error=age_required; begun with it, the
// account is created with its age row; a returning sign-in is never asked.
func TestOAuthFirstSignInMustAttestTheAge(t *testing.T) {
	env := newOAuthEnv(t, auth.WithSignupTermsFunc(func() auth.SignupTerms { return auth.SignupTerms{MinimumAge: 18} }))
	claims := map[string]any{"sub": "sub-300", "email": "cal@example.com", "email_verified": true, "name": "Cal"}
	flow := func(query string) *httptest.ResponseRecorder {
		t.Helper()
		loc, stateCookie := env.begin(t, query)
		env.fake.setNonce(loc.Query().Get("nonce"))
		env.fake.setClaims(claims)
		return env.callback(t, "?code=fake-code&state="+url.QueryEscape(loc.Query().Get("state")), stateCookie)
	}

	rec := flow("?terms_accepted=1&return_to=" + url.QueryEscape("/signup"))
	if got := rec.Header().Get("Location"); rec.Code != http.StatusFound || got != "/signup?oauth_error=age_required" {
		t.Fatalf("first sign-in without the attestation = %d → %q, want 302 → /signup?oauth_error=age_required", rec.Code, got)
	}
	if sessionCookieFrom(rec) != nil || len(env.repo.created) != 0 {
		t.Fatal("a refused first sign-in must set no session and create no account")
	}

	rec = flow("?age_attestation=1&return_to=" + url.QueryEscape("/welcome"))
	if got := rec.Header().Get("Location"); rec.Code != http.StatusFound || got != "/welcome" {
		t.Fatalf("first sign-in with the attestation = %d → %q, want 302 → /welcome (body=%s)", rec.Code, got, rec.Body.String())
	}
	if len(env.repo.created) != 1 {
		t.Fatalf("accounts created = %d, want 1", len(env.repo.created))
	}
	if got := env.repo.created[0]; got.ConsentSource != "oidc" || !slices.Equal(got.ConsentDocuments, []string{"age"}) ||
		!slices.Equal(got.ConsentMinimumAges, []int32{18}) {
		t.Fatalf("recorded %q %v %v", got.ConsentSource, got.ConsentDocuments, got.ConsentMinimumAges)
	}

	rec = flow("")
	if got := rec.Header().Get("Location"); rec.Code != http.StatusFound || got != "/" || sessionCookieFrom(rec) == nil {
		t.Fatalf("returning sign-in without the attestation = %d → %q, want a session and 302 → /", rec.Code, got)
	}
	if len(env.repo.created) != 1 {
		t.Fatalf("a returning sign-in created an account")
	}
}

// TestATProtoFirstSignInMustAttestTheAge is the same rule through the ATProto
// start body, with the minimum age read from the settings overlay.
func TestATProtoFirstSignInMustAttestTheAge(t *testing.T) {
	env := newATProtoLoginEnv(t, true)
	env.setSetting(t, instancesettings.KeyRegistrationMinimumAge, "18")
	callback := func(cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		return env.callback(t, "?code=the-code&state="+url.QueryEscape(env.backend.parState)+"&iss="+url.QueryEscape(env.backend.srv.URL), cookie)
	}

	_, cookie := env.start(t, `{"handle":"alice.example","return_to":"/signup"}`)
	cb := callback(cookie)
	if got := cb.Header().Get("Location"); cb.Code != http.StatusFound || got != "/signup?oauth_error=age_required" {
		t.Fatalf("first sign-in without the attestation = %d → %q, want 302 → /signup?oauth_error=age_required", cb.Code, got)
	}
	if atprotoSessionCookie(cb) != nil || len(env.repo.created) != 0 {
		t.Fatal("a refused first sign-in must set no session and create no account")
	}

	_, cookie = env.start(t, `{"handle":"alice.example","return_to":"/welcome","age_attestation":true}`)
	cb = callback(cookie)
	if cb.Code != http.StatusFound || atprotoSessionCookie(cb) == nil {
		t.Fatalf("first sign-in with the attestation = %d → %q, want a session", cb.Code, cb.Header().Get("Location"))
	}
	if len(env.repo.created) != 1 {
		t.Fatalf("accounts created = %d, want 1", len(env.repo.created))
	}
	if got := env.repo.created[0]; got.ConsentSource != "atproto" || !slices.Equal(got.ConsentDocuments, []string{"age"}) ||
		!slices.Equal(got.ConsentMinimumAges, []int32{18}) {
		t.Fatalf("recorded %q %v %v", got.ConsentSource, got.ConsentDocuments, got.ConsentMinimumAges)
	}
}
