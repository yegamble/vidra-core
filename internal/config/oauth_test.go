package config

import (
	"strings"
	"testing"
)

// clearOAuthEnv blanks every env var these tests set so table entries do not
// leak into each other (t.Setenv restores after the test).
func clearOAuthEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"OAUTH_PROVIDERS", "PUBLIC_BASE_URL", "VIDRA_ENV",
		"OAUTH_GOOGLE_ISSUER", "OAUTH_GOOGLE_CLIENT_ID", "OAUTH_GOOGLE_CLIENT_SECRET", "OAUTH_GOOGLE_SCOPES",
		"OAUTH_GITHUB_OIDC_ISSUER", "OAUTH_GITHUB_OIDC_CLIENT_ID", "OAUTH_GITHUB_OIDC_CLIENT_SECRET",
	} {
		t.Setenv(k, "")
	}
}

func TestOAuthDisabledByDefault(t *testing.T) {
	clearOAuthEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.OAuthProviders) != 0 {
		t.Errorf("OAuthProviders = %v, want empty", cfg.OAuthProviders)
	}
	if names := cfg.OAuthProviderNames(); names == nil || len(names) != 0 {
		t.Errorf("OAuthProviderNames() = %#v, want empty non-nil slice", names)
	}
}

func TestOAuthProvidersParsed(t *testing.T) {
	clearOAuthEnv(t)
	t.Setenv("OAUTH_PROVIDERS", "google, github-oidc")
	t.Setenv("PUBLIC_BASE_URL", "http://localhost:8080")
	t.Setenv("OAUTH_GOOGLE_ISSUER", "https://accounts.google.com/")
	t.Setenv("OAUTH_GOOGLE_CLIENT_ID", "gid")
	t.Setenv("OAUTH_GOOGLE_CLIENT_SECRET", "gsecret")
	t.Setenv("OAUTH_GOOGLE_SCOPES", "openid,email")
	// Dashes in the provider name map to underscores in the env prefix.
	t.Setenv("OAUTH_GITHUB_OIDC_ISSUER", "https://idp.example/gh")
	t.Setenv("OAUTH_GITHUB_OIDC_CLIENT_ID", "ghid")
	t.Setenv("OAUTH_GITHUB_OIDC_CLIENT_SECRET", "ghsecret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.OAuthProviders) != 2 {
		t.Fatalf("len(OAuthProviders) = %d, want 2", len(cfg.OAuthProviders))
	}
	g := cfg.OAuthProviders[0]
	if g.Name != "google" || g.IssuerURL != "https://accounts.google.com" || g.ClientID != "gid" || g.ClientSecret != "gsecret" {
		t.Errorf("google provider = %+v (issuer trailing slash should be trimmed)", g)
	}
	if len(g.Scopes) != 2 || g.Scopes[0] != "openid" || g.Scopes[1] != "email" {
		t.Errorf("google scopes = %v, want [openid email]", g.Scopes)
	}
	gh := cfg.OAuthProviders[1]
	if gh.Name != "github-oidc" || gh.IssuerURL != "https://idp.example/gh" || gh.ClientID != "ghid" {
		t.Errorf("github-oidc provider = %+v", gh)
	}
	if names := cfg.OAuthProviderNames(); len(names) != 2 || names[0] != "google" || names[1] != "github-oidc" {
		t.Errorf("OAuthProviderNames() = %v", names)
	}
}

func TestOAuthValidationFailures(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name: "missing PUBLIC_BASE_URL",
			env: map[string]string{
				"OAUTH_PROVIDERS":            "google",
				"OAUTH_GOOGLE_ISSUER":        "https://accounts.google.com",
				"OAUTH_GOOGLE_CLIENT_ID":     "id",
				"OAUTH_GOOGLE_CLIENT_SECRET": "secret",
			},
			wantErr: "PUBLIC_BASE_URL",
		},
		{
			name: "missing issuer",
			env: map[string]string{
				"OAUTH_PROVIDERS":            "google",
				"PUBLIC_BASE_URL":            "http://localhost:8080",
				"OAUTH_GOOGLE_CLIENT_ID":     "id",
				"OAUTH_GOOGLE_CLIENT_SECRET": "secret",
			},
			wantErr: "OAUTH_GOOGLE_ISSUER",
		},
		{
			name: "missing client id",
			env: map[string]string{
				"OAUTH_PROVIDERS":            "google",
				"PUBLIC_BASE_URL":            "http://localhost:8080",
				"OAUTH_GOOGLE_ISSUER":        "https://accounts.google.com",
				"OAUTH_GOOGLE_CLIENT_SECRET": "secret",
			},
			wantErr: "OAUTH_GOOGLE_CLIENT_ID",
		},
		{
			name: "missing client secret",
			env: map[string]string{
				"OAUTH_PROVIDERS":        "google",
				"PUBLIC_BASE_URL":        "http://localhost:8080",
				"OAUTH_GOOGLE_ISSUER":    "https://accounts.google.com",
				"OAUTH_GOOGLE_CLIENT_ID": "id",
			},
			wantErr: "OAUTH_GOOGLE_CLIENT_SECRET",
		},
		{
			name: "invalid provider name",
			env: map[string]string{
				"OAUTH_PROVIDERS": "Goo gle",
				"PUBLIC_BASE_URL": "http://localhost:8080",
			},
			wantErr: "provider name",
		},
		{
			name: "duplicate provider",
			env: map[string]string{
				"OAUTH_PROVIDERS":            "google,google",
				"PUBLIC_BASE_URL":            "http://localhost:8080",
				"OAUTH_GOOGLE_ISSUER":        "https://accounts.google.com",
				"OAUTH_GOOGLE_CLIENT_ID":     "id",
				"OAUTH_GOOGLE_CLIENT_SECRET": "secret",
			},
			wantErr: "duplicate OAuth provider",
		},
		{
			name: "production requires https issuer",
			env: map[string]string{
				"VIDRA_ENV":                  "production",
				"JWT_SECRET":                 "a-production-grade-secret-0123456789abcdef",
				"OAUTH_PROVIDERS":            "google",
				"PUBLIC_BASE_URL":            "https://videos.example",
				"OAUTH_GOOGLE_ISSUER":        "http://accounts.google.com",
				"OAUTH_GOOGLE_CLIENT_ID":     "id",
				"OAUTH_GOOGLE_CLIENT_SECRET": "secret",
			},
			wantErr: "must be https in production",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearOAuthEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load() error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}
