package auth

// OAuth/OIDC login (fix_plan P4 "OAuth2 provider abstraction" + P15 "OAuth
// redirect validation").
//
// Vidra treats every provider as a generic, spec-compliant OIDC issuer: the
// authorization/token/JWKS endpoints come from the issuer's discovery document
// (<issuer>/.well-known/openid-configuration), so Google, Keycloak, Authentik,
// a GitHub OIDC shim, … all work with the same code. The heavy protocol lifting
// is delegated to two well-maintained, narrowly scoped dependencies rather than
// hand-rolled crypto:
//
//   - golang.org/x/oauth2 — the authorization-code + PKCE dance;
//   - github.com/coreos/go-oidc/v3 — discovery, JWKS fetching/caching, and
//     id_token signature/issuer/audience/expiry verification. Hand-writing JWKS
//     handling and RSA/EC verification is exactly the kind of security-critical
//     code that should not be bespoke.
//
// Security posture:
//   - The redirect URI is supplied by the HTTP layer, derived server-side from
//     PUBLIC_BASE_URL — never from request parameters (P15).
//   - Every attempt carries a fresh state (CSRF), nonce (id_token replay), and
//     PKCE verifier (code interception); Complete verifies all three.
//   - No provider tokens are persisted. Vidra issues its own session (the
//     normal access + rotating refresh pair) after verification.
//   - The id_token email claim is trusted for account linking ONLY when the
//     provider asserts email_verified — an unverified matching email is
//     refused (ErrOAuthEmailConflict), otherwise anyone could register an
//     unverified address at a lax IdP and take over the local account.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to responses.
var (
	// ErrUnknownOAuthProvider means the provider name is not configured.
	ErrUnknownOAuthProvider = errors.New("auth: unknown oauth provider")
	// ErrOAuthExchange means the code exchange or id_token verification failed
	// upstream (provider unreachable, bad code, invalid token signature…).
	ErrOAuthExchange = errors.New("auth: oauth code exchange failed")
	// ErrOAuthNonceMismatch means the id_token's nonce does not match the one
	// minted at Begin — a replayed or forged token.
	ErrOAuthNonceMismatch = errors.New("auth: oauth nonce mismatch")
	// ErrOAuthEmailConflict means the provider email matches an existing local
	// account but the provider does not assert it verified, so linking would be
	// an account-takeover vector and creating a duplicate is impossible.
	ErrOAuthEmailConflict = errors.New("auth: oauth email belongs to an existing account but is not verified by the provider")
	// ErrOAuthEmailMissing means the provider supplied no email, which is
	// required to create a local account (identities already linked still log in).
	ErrOAuthEmailMissing = errors.New("auth: oauth provider supplied no email")
	// ErrOAuthIdentityNotFound means the caller has no identity linked for the
	// provider (unlink of a non-linked provider).
	ErrOAuthIdentityNotFound = errors.New("auth: oauth identity not linked")
	// ErrOAuthLastCredential means unlinking would leave a passwordless account
	// with no way to sign in.
	ErrOAuthLastCredential = errors.New("auth: cannot remove the last sign-in method")
)

// OAuthProvider is one configured OIDC provider (mirrors
// config.OAuthProviderConfig without importing config into this package).
type OAuthProvider struct {
	Name         string
	IssuerURL    string
	ClientID     string
	ClientSecret string
	Scopes       []string
}

// OAuthRepository is the data access the OAuth service needs.
// *sqlcgen.Queries satisfies it directly.
type OAuthRepository interface {
	GetOAuthIdentity(ctx context.Context, arg sqlcgen.GetOAuthIdentityParams) (sqlcgen.OauthIdentity, error)
	CreateOAuthIdentity(ctx context.Context, arg sqlcgen.CreateOAuthIdentityParams) (sqlcgen.OauthIdentity, error)
	UpdateOAuthIdentityHandle(ctx context.Context, arg sqlcgen.UpdateOAuthIdentityHandleParams) error
	ListOAuthIdentitiesByUser(ctx context.Context, userID uuid.UUID) ([]sqlcgen.OauthIdentity, error)
	CountOAuthIdentitiesByUser(ctx context.Context, userID uuid.UUID) (int64, error)
	DeleteOAuthIdentity(ctx context.Context, arg sqlcgen.DeleteOAuthIdentityParams) (int64, error)
	UsernameExists(ctx context.Context, lower string) (bool, error)

	GetUserByEmail(ctx context.Context, lowerEmail string) (sqlcgen.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error)
	CreateUser(ctx context.Context, arg sqlcgen.CreateUserParams) (sqlcgen.User, error)
	CountUsers(ctx context.Context) (int64, error)
	SetUserEmailVerified(ctx context.Context, id uuid.UUID) error
}

// oidcClient lazily performs OIDC discovery for one provider and caches the
// result, so a temporarily unreachable IdP delays only OAuth attempts — never
// process boot.
type oidcClient struct {
	mu       sync.Mutex
	provider *oidc.Provider
}

func (c *oidcClient) get(ctx context.Context, hc *http.Client, issuer string) (*oidc.Provider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil {
		return c.provider, nil
	}
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, hc), issuer)
	if err != nil {
		return nil, fmt.Errorf("auth: oidc discovery for issuer failed: %w", err)
	}
	c.provider = p
	return p, nil
}

// OAuthService implements OIDC login/link on top of the core auth service
// (which issues the resulting Vidra session).
type OAuthService struct {
	repo      OAuthRepository
	auth      *Service
	providers []OAuthProvider
	clients   map[string]*oidcClient
	// httpClient performs discovery/JWKS/token-endpoint calls. Issuer URLs are
	// operator configuration (not user input), so no SSRF guard is needed; the
	// timeout bounds a slow IdP.
	httpClient *http.Client
}

// OAuthOption configures optional OAuthService behavior.
type OAuthOption func(*OAuthService)

// WithOAuthHTTPClient overrides the outbound HTTP client (tests point it at a
// fake provider; production keeps the timeout default). Nil is ignored.
func WithOAuthHTTPClient(c *http.Client) OAuthOption {
	return func(s *OAuthService) {
		if c != nil {
			s.httpClient = c
		}
	}
}

// NewOAuthService builds the OAuth service. svc issues the Vidra session after
// a successful external login; providers may be empty (all routes 404).
func NewOAuthService(repo OAuthRepository, svc *Service, providers []OAuthProvider, opts ...OAuthOption) *OAuthService {
	s := &OAuthService{
		repo:       repo,
		auth:       svc,
		providers:  providers,
		clients:    make(map[string]*oidcClient, len(providers)),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
	for _, p := range providers {
		s.clients[p.Name] = &oidcClient{}
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ProviderNames returns the configured provider names in configuration order.
// Never nil.
func (s *OAuthService) ProviderNames() []string {
	names := make([]string, 0, len(s.providers))
	for _, p := range s.providers {
		names = append(names, p.Name)
	}
	return names
}

// Enabled reports whether a provider name is configured.
func (s *OAuthService) Enabled(name string) bool {
	_, _, err := s.lookup(name)
	return err == nil
}

func (s *OAuthService) lookup(name string) (OAuthProvider, *oidcClient, error) {
	for _, p := range s.providers {
		if p.Name == name {
			return p, s.clients[p.Name], nil
		}
	}
	return OAuthProvider{}, nil, ErrUnknownOAuthProvider
}

// oauthConfig assembles the oauth2 config for one attempt. redirectURI is
// derived by the caller from PUBLIC_BASE_URL (P15) — never from the request.
func oauthConfig(p OAuthProvider, op *oidc.Provider, redirectURI string) *oauth2.Config {
	scopes := p.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "email", "profile"}
	} else {
		hasOpenID := false
		for _, sc := range scopes {
			if sc == oidc.ScopeOpenID {
				hasOpenID = true
			}
		}
		if !hasOpenID {
			scopes = append([]string{oidc.ScopeOpenID}, scopes...)
		}
	}
	return &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.ClientSecret,
		Endpoint:     op.Endpoint(),
		RedirectURL:  redirectURI,
		Scopes:       scopes,
	}
}

// OAuthState carries the per-attempt anti-forgery values minted by BeginAuth
// and required back at CompleteAuth. The HTTP layer transports it to the
// browser and back in a signed, short-lived, httpOnly cookie; the values are
// secrets for the duration of the attempt and must never be logged.
type OAuthState struct {
	// State is the CSRF token echoed back by the provider in the callback URL.
	State string
	// Nonce binds the id_token to this attempt (checked against its nonce claim).
	Nonce string
	// Verifier is the PKCE code verifier (its S256 challenge went out with the
	// authorization request; the verifier goes to the token endpoint only).
	Verifier string
}

// BeginAuth starts the authorization-code flow: it mints fresh state/nonce/PKCE
// values and returns the provider authorization URL to redirect the browser to.
func (s *OAuthService) BeginAuth(ctx context.Context, provider, redirectURI string) (string, OAuthState, error) {
	p, cl, err := s.lookup(provider)
	if err != nil {
		return "", OAuthState{}, err
	}
	op, err := cl.get(ctx, s.httpClient, p.IssuerURL)
	if err != nil {
		return "", OAuthState{}, err
	}
	st := OAuthState{
		State:    randomURLToken(),
		Nonce:    randomURLToken(),
		Verifier: oauth2.GenerateVerifier(),
	}
	authURL := oauthConfig(p, op, redirectURI).AuthCodeURL(st.State,
		oidc.Nonce(st.Nonce),
		oauth2.S256ChallengeOption(st.Verifier),
	)
	return authURL, st, nil
}

// OAuthOutcome classifies what CompleteAuth did with a verified identity.
type OAuthOutcome string

const (
	// OAuthLogin — the identity was already linked; the linked account logged in.
	OAuthLogin OAuthOutcome = "login"
	// OAuthLinked — the identity was new but its verified email matched an
	// existing account, which it is now linked to (and logged in).
	OAuthLinked OAuthOutcome = "linked"
	// OAuthCreated — a brand-new account was created for the identity.
	OAuthCreated OAuthOutcome = "created"
)

// oidcClaims are the id_token claims Vidra consumes. email_verified is a
// boolean per OIDC core; providers that violate the spec simply do not get
// email-based linking.
type oidcClaims struct {
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
}

// CompleteAuth finishes the flow after the provider redirected back: it
// exchanges the code (with the PKCE verifier), verifies the id_token against
// the provider JWKS (signature, issuer, audience, expiry) and the attempt
// nonce, then resolves the external identity to a Vidra session:
//
//	known identity                          → log its account in
//	new identity, verified email match      → link to that account, log in
//	new identity, unverified email match    → refuse (ErrOAuthEmailConflict)
//	new identity, no match                  → create an account (username
//	                                          derived + deduped; email_verified
//	                                          inherited from the claim)
func (s *OAuthService) CompleteAuth(ctx context.Context, provider, redirectURI, code string, st OAuthState, userAgent string) (sqlcgen.User, Tokens, OAuthOutcome, error) {
	p, cl, err := s.lookup(provider)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, "", err
	}
	op, err := cl.get(ctx, s.httpClient, p.IssuerURL)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, "", err
	}

	octx := oidc.ClientContext(ctx, s.httpClient)
	tok, err := oauthConfig(p, op, redirectURI).Exchange(octx, code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return sqlcgen.User{}, Tokens{}, "", fmt.Errorf("%w: %v", ErrOAuthExchange, err)
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return sqlcgen.User{}, Tokens{}, "", fmt.Errorf("%w: token response carried no id_token", ErrOAuthExchange)
	}
	idToken, err := op.Verifier(&oidc.Config{ClientID: p.ClientID}).Verify(octx, rawIDToken)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, "", fmt.Errorf("%w: id_token verification: %v", ErrOAuthExchange, err)
	}
	if st.Nonce == "" || idToken.Nonce != st.Nonce {
		return sqlcgen.User{}, Tokens{}, "", ErrOAuthNonceMismatch
	}
	if idToken.Subject == "" {
		return sqlcgen.User{}, Tokens{}, "", fmt.Errorf("%w: id_token carried no subject", ErrOAuthExchange)
	}
	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil {
		return sqlcgen.User{}, Tokens{}, "", fmt.Errorf("%w: id_token claims: %v", ErrOAuthExchange, err)
	}
	return s.resolveIdentity(ctx, p.Name, idToken.Subject, claims, userAgent)
}

// resolveIdentity maps a verified (provider, subject, claims) to a local
// session, following the login / link / create ladder.
func (s *OAuthService) resolveIdentity(ctx context.Context, provider, subject string, claims oidcClaims, userAgent string) (sqlcgen.User, Tokens, OAuthOutcome, error) {
	// Known identity → login.
	if ident, err := s.repo.GetOAuthIdentity(ctx, sqlcgen.GetOAuthIdentityParams{Provider: provider, Subject: subject}); err == nil {
		user, err := s.repo.GetUserByID(ctx, ident.UserID)
		if err != nil {
			return sqlcgen.User{}, Tokens{}, "", ErrAccountNotFound
		}
		if !user.IsActive {
			return sqlcgen.User{}, Tokens{}, "", ErrAccountDisabled
		}
		tokens, err := s.auth.issueTokens(ctx, user, userAgent)
		if err != nil {
			return sqlcgen.User{}, Tokens{}, "", err
		}
		return user, tokens, OAuthLogin, nil
	}

	email := strings.TrimSpace(claims.Email)

	// Unknown identity + existing account with the same email → link, but only
	// when the provider asserts the email verified (otherwise it is an account-
	// takeover vector, and a duplicate account is impossible anyway).
	if email != "" {
		if user, err := s.repo.GetUserByEmail(ctx, email); err == nil {
			if !claims.EmailVerified {
				return sqlcgen.User{}, Tokens{}, "", ErrOAuthEmailConflict
			}
			if !user.IsActive {
				return sqlcgen.User{}, Tokens{}, "", ErrAccountDisabled
			}
			if _, err := s.repo.CreateOAuthIdentity(ctx, sqlcgen.CreateOAuthIdentityParams{
				Provider: provider, Subject: subject, UserID: user.ID, Email: email,
			}); err != nil {
				if isUniqueViolation(err) {
					// The account already has a different subject linked for this
					// provider, or a concurrent attempt won. Refuse cleanly.
					return sqlcgen.User{}, Tokens{}, "", ErrConflict
				}
				return sqlcgen.User{}, Tokens{}, "", err
			}
			tokens, err := s.auth.issueTokens(ctx, user, userAgent)
			if err != nil {
				return sqlcgen.User{}, Tokens{}, "", err
			}
			return user, tokens, OAuthLinked, nil
		}
	}

	// Unknown identity, no matching account → create one. An email is required
	// (users.email is NOT NULL + unique; and account recovery needs it).
	if email == "" {
		return sqlcgen.User{}, Tokens{}, "", ErrOAuthEmailMissing
	}
	username, err := s.deriveUsername(ctx, claims.PreferredUsername, claims.Name, email)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, "", err
	}
	// Signup parity with password registration: while the instance awaits its
	// owner (ownerclaim.go), no path may create an account — least of all one
	// that used to mint the admin.
	if err := s.auth.refuseIfOwnerUnclaimed(ctx); err != nil {
		return sqlcgen.User{}, Tokens{}, "", err
	}
	user, err := s.repo.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: username,
		Email:    email,
		// No password: OAuth is the account's credential. An empty hash can
		// never verify (bcrypt rejects it), so password login stays impossible
		// until the user sets one via the password-reset flow.
		PasswordHash: "",
		Role:         "user",
		// OAuth accounts are never held pending email verification: the IdP
		// attests the identity (claims.email_verified is honored below), so the
		// W7 registration gate deliberately does not apply here.
		HistoryEnabled: s.auth.newUserHistoryEnabled(),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return sqlcgen.User{}, Tokens{}, "", ErrConflict
		}
		return sqlcgen.User{}, Tokens{}, "", err
	}
	if claims.EmailVerified {
		if err := s.repo.SetUserEmailVerified(ctx, user.ID); err != nil {
			return sqlcgen.User{}, Tokens{}, "", err
		}
		user.EmailVerified = true
	}
	if _, err := s.repo.CreateOAuthIdentity(ctx, sqlcgen.CreateOAuthIdentityParams{
		Provider: provider, Subject: subject, UserID: user.ID, Email: email,
	}); err != nil {
		return sqlcgen.User{}, Tokens{}, "", err
	}
	tokens, err := s.auth.issueTokens(ctx, user, userAgent)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, "", err
	}
	return user, tokens, OAuthCreated, nil
}

// Identities lists the caller's linked OAuth identities (oldest first).
func (s *OAuthService) Identities(ctx context.Context, userID uuid.UUID) ([]sqlcgen.OauthIdentity, error) {
	return s.repo.ListOAuthIdentitiesByUser(ctx, userID)
}

// Unlink removes the caller's identity for a provider. It refuses to remove
// the last sign-in method: a passwordless account (created via OAuth, never
// set a password) must keep at least one linked identity
// (ErrOAuthLastCredential → the user should set a password via the reset flow
// first). The check-then-delete is not transactional; the worst case of a
// pathological concurrent double-unlink is an account recoverable via password
// reset, which is acceptable.
func (s *OAuthService) Unlink(ctx context.Context, userID uuid.UUID, provider string) error {
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return ErrAccountNotFound
	}
	if user.PasswordHash == "" {
		n, err := s.repo.CountOAuthIdentitiesByUser(ctx, userID)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrOAuthLastCredential
		}
	}
	rows, err := s.repo.DeleteOAuthIdentity(ctx, sqlcgen.DeleteOAuthIdentityParams{UserID: userID, Provider: provider})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrOAuthIdentityNotFound
	}
	return nil
}

// usernameChecker is the repo capability deriveUsername needs. Both the OIDC and
// the ATProto login services share the same derivation via this seam.
type usernameChecker interface {
	UsernameExists(ctx context.Context, lower string) (bool, error)
}

// deriveUsername is the OAuthService convenience wrapper over the package-level
// deriveUsername (kept so existing callers/tests read unchanged).
func (s *OAuthService) deriveUsername(ctx context.Context, preferred, name, email string) (string, error) {
	return deriveUsername(ctx, s.repo, preferred, name, email)
}

// deriveUsername builds a unique local username from candidate labels: the first
// non-empty of preferred, name, and the email local part — sanitized to lowercase
// [a-z0-9._-], then deduped with a numeric suffix against repo.UsernameExists.
func deriveUsername(ctx context.Context, repo usernameChecker, preferred, name, email string) (string, error) {
	base := ""
	local, _, _ := strings.Cut(email, "@")
	for _, cand := range []string{preferred, name, local} {
		if b := sanitizeUsername(cand); b != "" {
			base = b
			break
		}
	}
	if len(base) < 3 {
		base = "user"
	}
	if len(base) > 24 {
		base = base[:24] // leave room for a dedupe suffix within the 30-char cap
	}
	for i := 0; i < 50; i++ {
		cand := base
		if i > 0 {
			cand = base + strconv.Itoa(i+1)
		}
		taken, err := repo.UsernameExists(ctx, cand)
		if err != nil {
			return "", err
		}
		if !taken {
			return cand, nil
		}
	}
	// Pathologically crowded namespace: fall back to a random suffix. The
	// unique index remains the backstop either way.
	return base + "-" + randomURLToken()[:6], nil
}

// sanitizeUsername lowercases and keeps [a-z0-9._-], mapping runs of anything
// else (spaces, unicode, …) to a single dash, trimmed at both ends.
func sanitizeUsername(s string) string {
	var b strings.Builder
	lastDash := true // suppress a leading dash
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
			lastDash = r == '-'
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-.")
}

// randomURLToken returns a 256-bit URL-safe random token (state/nonce values).
func randomURLToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read is documented never to fail on supported platforms;
		// never proceed with weak randomness if it somehow does.
		panic(fmt.Sprintf("auth: crypto/rand failed: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
