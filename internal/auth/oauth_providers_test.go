package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestOAuthDatabaseProvidersOverrideEnvironment pins the precedence rule for
// admin-managed providers: a database row replaces the environment provider of
// the same name, a DISABLED row switches that name off entirely, and database-only
// providers follow the environment list.
func TestOAuthDatabaseProvidersOverrideEnvironment(t *testing.T) {
	s := NewOAuthService(nil, nil, []OAuthProvider{
		{Name: "corp", IssuerURL: "https://env.example", ClientID: "env"},
		{Name: "google", IssuerURL: "https://accounts.google.com", ClientID: "g"},
	})
	s.SetDatabaseProviders([]OAuthProvider{
		{Name: "corp", IssuerURL: "https://env.example", ClientID: "db", DisplayName: "Corp SSO"},
		{Name: "google", Disabled: true},
		{Name: "keycloak", IssuerURL: "https://kc.example", ClientID: "kc"},
		{Name: "off", IssuerURL: "https://off.example", ClientID: "x", Disabled: true},
	})
	if got := strings.Join(s.ProviderNames(), ","); got != "corp,keycloak" {
		t.Fatalf("ProviderNames() = %q, want corp,keycloak", got)
	}
	p, _, err := s.lookup("corp")
	if err != nil || p.ClientID != "db" || p.DisplayName != "Corp SSO" {
		t.Errorf("corp = %+v, %v; want the database row", p, err)
	}
	if s.Enabled("google") || s.Enabled("off") {
		t.Errorf("a disabled database row must switch its name off")
	}

	// Clearing the database set restores the environment list exactly.
	s.SetDatabaseProviders(nil)
	if got := strings.Join(s.ProviderNames(), ","); got != "corp,google" {
		t.Errorf("after clear ProviderNames() = %q, want corp,google", got)
	}
	if p, _, _ := s.lookup("corp"); p.ClientID != "env" {
		t.Errorf("after clear corp client = %q, want env", p.ClientID)
	}
}

// TestOAuthGuardedProviderDiscoveryUsesSSRFGuard proves an admin-managed
// provider's discovery goes through the SSRF guard: an issuer on a loopback
// address is refused at dial time unless private addresses were allowed.
func TestOAuthGuardedProviderDiscoveryUsesSSRFGuard(t *testing.T) {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 srvURL,
			"authorization_endpoint": srvURL + "/authorize",
			"token_endpoint":         srvURL + "/token",
			"jwks_uri":               srvURL + "/jwks",
		})
	}))
	t.Cleanup(srv.Close)
	srvURL = srv.URL
	ctx := context.Background()
	db := []OAuthProvider{{Name: "lab", IssuerURL: srv.URL, ClientID: "c", Guarded: true}}

	strict := NewOAuthService(nil, nil, nil)
	strict.SetDatabaseProviders(db)
	if _, _, err := strict.BeginAuth(ctx, "lab", "https://vidra.example/cb"); err == nil {
		t.Fatal("guarded discovery reached a loopback issuer; want an SSRF refusal")
	}

	lab := NewOAuthService(nil, nil, nil, WithOAuthAllowPrivateIssuers(true))
	lab.SetDatabaseProviders(db)
	authURL, _, err := lab.BeginAuth(ctx, "lab", "https://vidra.example/cb")
	if err != nil || !strings.HasPrefix(authURL, srv.URL+"/authorize") {
		t.Fatalf("allow-private discovery: url=%q err=%v", authURL, err)
	}

	// The environment provider keeps the operator's client (no guard): an IdP
	// on the private network is a normal operator setup.
	env := NewOAuthService(nil, nil, []OAuthProvider{{Name: "env", IssuerURL: srv.URL, ClientID: "c"}},
		WithOAuthHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	if _, _, err := env.BeginAuth(ctx, "env", "https://vidra.example/cb"); err != nil {
		t.Fatalf("environment provider discovery: %v", err)
	}
}

// TestOAuthProviderSignupPolicy covers the two per-provider account-creation
// controls. Both apply only to an UNKNOWN subject: a linked identity keeps
// signing in whatever the policy says.
func TestOAuthProviderSignupPolicy(t *testing.T) {
	repo := newOAuthFakeRepo()
	s := newOAuthTestService(repo)
	s.SetDatabaseProviders([]OAuthProvider{
		{Name: "closed", IssuerURL: "https://closed.example", ClientID: "c", DisableSignup: true},
		{Name: "corp", IssuerURL: "https://corp.example", ClientID: "c", AllowedEmailDomains: []string{"corp.example"}},
	})
	ctx := context.Background()

	if _, err := s.resolveIdentity(ctx, "closed", "s1", oidcClaims{Email: "a@x.example", EmailVerified: true}, "ua"); !errors.Is(err, ErrOAuthSignupDisabled) {
		t.Errorf("closed provider, unknown subject: err = %v, want ErrOAuthSignupDisabled", err)
	}
	u, err := repo.CreateUser(ctx, sqlcgen.CreateUserParams{Username: "linked", Email: "l@x.example", PasswordHash: "x", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	repo.identities[identKey("closed", "s-linked")] = sqlcgen.OauthIdentity{Provider: "closed", Subject: "s-linked", UserID: u.ID}
	if sess, err := s.resolveIdentity(ctx, "closed", "s-linked", oidcClaims{}, "ua"); err != nil || sess.Outcome != OAuthLogin {
		t.Errorf("closed provider, linked subject: outcome=%v err=%v; want login", sess.Outcome, err)
	}

	for _, tc := range []struct {
		name     string
		email    string
		verified bool
	}{
		{"unverified allowed domain", "bob@corp.example", false},
		{"verified other domain", "bob@evil.example", true},
		{"subdomain is not the domain", "bob@x.corp.example", true},
	} {
		if _, err := s.resolveIdentity(ctx, "corp", "sub-"+tc.name, oidcClaims{Email: tc.email, EmailVerified: tc.verified}, "ua"); !errors.Is(err, ErrOAuthEmailDomainNotAllowed) {
			t.Errorf("%s: err = %v, want ErrOAuthEmailDomainNotAllowed", tc.name, err)
		}
	}
	if sess, err := s.resolveIdentity(ctx, "corp", "sub-ok", oidcClaims{Email: "Bob@CORP.example", EmailVerified: true}, "ua"); err != nil || sess.Outcome != OAuthCreated {
		t.Errorf("verified allowed domain: outcome=%v err=%v; want created", sess.Outcome, err)
	}
}

// discoveryServer serves a discovery document whose endpoints share base.
func discoveryServer(t *testing.T, tls bool, pad int) *httptest.Server {
	t.Helper()
	var base string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": base, "authorization_endpoint": base + "/authorize",
			"token_endpoint": base + "/token", "jwks_uri": base + "/jwks",
			"userinfo_endpoint": base + "/userinfo", "padding": strings.Repeat("x", pad),
		})
	})
	var srv *httptest.Server
	if tls {
		srv = httptest.NewTLSServer(h)
	} else {
		srv = httptest.NewServer(h)
	}
	base = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

// TestGuardedDiscoveryEndpointsMustBeHTTPS: an admin-managed provider's
// discovered endpoints must be https unless private issuers are allowed.
func TestGuardedDiscoveryEndpointsMustBeHTTPS(t *testing.T) {
	ctx := context.Background()
	plain := discoveryServer(t, false, 0)
	op, err := (&oidcClient{}).get(ctx, plain.Client(), plain.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkDiscoveredEndpoints(op, false); err == nil {
		t.Error("http endpoints accepted without the private-issuer switch")
	}
	if err := checkDiscoveredEndpoints(op, true); err != nil {
		t.Errorf("http endpoints refused with the private-issuer switch: %v", err)
	}
	secure := discoveryServer(t, true, 0)
	op, err = (&oidcClient{}).get(ctx, secure.Client(), secure.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkDiscoveredEndpoints(op, false); err != nil {
		t.Errorf("https endpoints refused: %v", err)
	}
}

// TestGuardedDiscoveryBodyIsBounded: an oversized discovery document fails
// instead of being read whole.
func TestGuardedDiscoveryBodyIsBounded(t *testing.T) {
	big := discoveryServer(t, false, maxOIDCResponseBytes+1)
	s := NewOAuthService(nil, nil, nil, WithOAuthAllowPrivateIssuers(true))
	s.SetDatabaseProviders([]OAuthProvider{{Name: "big", IssuerURL: big.URL, ClientID: "c", Guarded: true}})
	if _, _, err := s.BeginAuth(context.Background(), "big", "https://vidra.example/cb"); err == nil {
		t.Fatal("oversized discovery document accepted")
	}
	small := discoveryServer(t, false, 1000)
	s.SetDatabaseProviders([]OAuthProvider{{Name: "small", IssuerURL: small.URL, ClientID: "c", Guarded: true}})
	if _, _, err := s.BeginAuth(context.Background(), "small", "https://vidra.example/cb"); err != nil {
		t.Fatalf("normal discovery document refused: %v", err)
	}
}

// TestGuardedRedirectsMustStayHTTPS: a redirect from an admin-managed
// provider's endpoint to http is refused unless private issuers are allowed.
func TestGuardedRedirectsMustStayHTTPS(t *testing.T) {
	via := []*http.Request{httptest.NewRequest(http.MethodGet, "https://idp.example/token", nil)}
	for _, tc := range []struct {
		allow  bool
		target string
		ok     bool
	}{
		{false, "http://idp.example/token", false},
		{false, "https://idp.example/token2", true},
		{true, "http://idp.example/token", true},
	} {
		s := NewOAuthService(nil, nil, nil, WithOAuthAllowPrivateIssuers(tc.allow))
		err := s.guardedClient.CheckRedirect(httptest.NewRequest(http.MethodGet, tc.target, nil), via)
		if (err == nil) != tc.ok {
			t.Errorf("allowPrivate=%v redirect to %s: err = %v, want ok=%v", tc.allow, tc.target, err, tc.ok)
		}
	}
}
