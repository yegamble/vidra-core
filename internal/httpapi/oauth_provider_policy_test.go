package httpapi

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/vidra/vidra-core/internal/auth"
)

// TestOAuthCallbackProviderSignupPolicy pins the typed redirect codes for the
// two per-provider account-creation controls of an admin-managed provider, and
// that neither refusal creates an account or mints a session.
func TestOAuthCallbackProviderSignupPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy auth.OAuthProvider
		claims map[string]any
		code   string
	}{
		{"signup disabled", auth.OAuthProvider{DisableSignup: true},
			map[string]any{"sub": "s1", "email": "a@corp.test", "email_verified": true}, "signup_disabled"},
		{"domain not allowed", auth.OAuthProvider{AllowedEmailDomains: []string{"corp.test"}},
			map[string]any{"sub": "s2", "email": "a@other.test", "email_verified": true}, "email_domain_not_allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newOAuthEnv(t)
			p := tc.policy
			p.Name, p.IssuerURL, p.ClientID, p.ClientSecret = "fake", env.fake.srv.URL, "test-client", "test-client-secret"
			env.oauthsvc.SetDatabaseProviders([]auth.OAuthProvider{p})

			cb := env.completeFlow(t, tc.claims, "/login")
			if cb.Code != http.StatusFound {
				t.Fatalf("callback = %d, want 302 (body=%s)", cb.Code, cb.Body.String())
			}
			loc, err := url.Parse(cb.Header().Get("Location"))
			if err != nil || loc.Query().Get("oauth_error") != tc.code {
				t.Errorf("Location = %q, want ?oauth_error=%s", cb.Header().Get("Location"), tc.code)
			}
			if sessionCookieFrom(cb) != nil || len(env.repo.users) != 0 {
				t.Error("a refused provider signup must not create an account or a session")
			}
		})
	}
}
