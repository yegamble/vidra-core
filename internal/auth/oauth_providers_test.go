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
