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
//   - A PROVIDER-ASSERTED EMAIL NEVER LINKS AN ACCOUNT. An id_token whose email
//     matches an existing local account is refused (ErrOAuthEmailConflict)
//     whether or not the provider asserts email_verified. The A05 lab measured
//     what the old "verified email links" rule cost: a SECOND configured
//     provider asserted the owner's address verified and signed in as the
//     owner — role admin, is_owner true — having proved nothing about the local
//     credential. An operator picks the IdP, but picking two must not make
//     either one a master key for the other's accounts, and no IdP's word about
//     an address is evidence that its holder consented to hand over a Vidra
//     account. An account is matched by SUBJECT, which is issuer-scoped and
//     unforgeable across providers; linking is an act the account holder
//     performs from settings, in a session (see LinkIdentity).
//   - The second factor applies here. A provider sign-in resolving to an
//     MFA-enabled account withholds the session exactly as the password path
//     does and returns the same single-purpose mfa_token (OAuthSession).

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

	"github.com/vidra/vidra-core/internal/pgconv"
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
	// ErrOAuthEmailConflict means the provider email belongs to an existing
	// local account that this provider subject is not linked to. Verified or
	// not, the claim is not evidence the account holder consented, so the sign-in
	// is refused rather than auto-linked; the remedy is to sign in with the
	// account's own credential and connect the provider from settings.
	ErrOAuthEmailConflict = errors.New("auth: oauth email belongs to an existing account this identity is not linked to")
	// ErrOAuthEmailMissing means the provider supplied no email, which is
	// required to create a local account (identities already linked still log in).
	ErrOAuthEmailMissing = errors.New("auth: oauth provider supplied no email")
	// ErrOAuthIdentityNotFound means the caller has no identity linked for the
	// provider (unlink of a non-linked provider).
	ErrOAuthIdentityNotFound = errors.New("auth: oauth identity not linked")
	// ErrOAuthLastCredential means unlinking would leave a passwordless account
	// with no way to sign in.
	ErrOAuthLastCredential = errors.New("auth: cannot remove the last sign-in method")
	// ErrOAuthIdentityClaimed means the verified provider subject is already
	// linked to a DIFFERENT local account. Moving it silently would be an
	// account switch the caller did not ask for, so both the link flow and a
	// login-purpose callback arriving inside a session refuse.
	ErrOAuthIdentityClaimed = errors.New("auth: this provider identity belongs to another account")
	// ErrOAuthProviderAlreadyLinked means the account already has an identity
	// for this provider (the (user_id, provider) unique index). One provider
	// links one identity per account; unlink the current one first.
	ErrOAuthProviderAlreadyLinked = errors.New("auth: this provider is already linked to your account")
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
	// OAuthLinked — the identity was attached to an account the CALLER was
	// already signed in to (the settings link flow). It is never reached from a
	// plain login any more: an email match is a refusal, not a link.
	OAuthLinked OAuthOutcome = "linked"
	// OAuthCreated — a brand-new account was created for the identity.
	OAuthCreated OAuthOutcome = "created"
)

// OAuthSession is what a verified provider identity resolves to. It is a struct
// rather than a longer return list because the interesting case is the one that
// carries NO session: an MFA-enabled account gets the same withheld-session
// treatment the password path gives it, and a caller that destructured
// (user, tokens, outcome) would have read the zero Tokens as a session.
type OAuthSession struct {
	// User is the account the identity resolved to.
	User sqlcgen.User
	// Outcome classifies what happened (login / linked / created).
	Outcome OAuthOutcome
	// Tokens is the issued session — zero when MFARequired.
	Tokens Tokens
	// MFARequired reports that the account has a second factor, so no session
	// was issued: the caller must complete POST /auth/mfa/challenge.
	MFARequired bool
	// MFAToken is the single-purpose, five-minute challenge token — the SAME
	// one Login mints, so the challenge endpoint needs no second code path.
	MFAToken string
}

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
//	known identity (provider, subject)  → log its account in
//	unknown identity, email matches an
//	  existing account                  → refuse (ErrOAuthEmailConflict),
//	                                      verified or not — see the file header
//	unknown identity, no email match    → create an account (username derived +
//	                                      deduped; email_verified inherited from
//	                                      the claim)
//
// …and on an MFA-enabled account the session is withheld for the challenge.
func (s *OAuthService) CompleteAuth(ctx context.Context, provider, redirectURI, code string, st OAuthState, userAgent string) (OAuthSession, error) {
	as, err := s.VerifyIdentity(ctx, provider, redirectURI, code, st)
	if err != nil {
		return OAuthSession{}, err
	}
	return s.ResolveAssertion(ctx, as, userAgent)
}

// VerifyAssertion runs everything CompleteAuth does EXCEPT turning the verified
// identity into a session, returning the provider subject the id_token attested.
// It is the OIDC half of the step-up flow, and it shares CompleteAuth's body for
// the same reason the ATProto one does: a step-up must be exactly as strong as
// the login it stands in for, and the only way to keep it so is for both to run
// the same exchange, the same JWKS verification and the same nonce check.
func (s *OAuthService) VerifyAssertion(ctx context.Context, provider, redirectURI, code string, st OAuthState) (string, error) {
	_, subject, _, err := s.verifyAssertion(ctx, provider, redirectURI, code, st)
	return subject, err
}

// verifyAssertion exchanges the code and verifies the id_token (signature,
// issuer, audience, expiry) and the attempt nonce, returning the canonical
// provider name, the subject and the consumed claims.
func (s *OAuthService) verifyAssertion(ctx context.Context, provider, redirectURI, code string, st OAuthState) (string, string, oidcClaims, error) {
	var claims oidcClaims
	p, cl, err := s.lookup(provider)
	if err != nil {
		return "", "", claims, err
	}
	op, err := cl.get(ctx, s.httpClient, p.IssuerURL)
	if err != nil {
		return "", "", claims, err
	}

	octx := oidc.ClientContext(ctx, s.httpClient)
	tok, err := oauthConfig(p, op, redirectURI).Exchange(octx, code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return "", "", claims, fmt.Errorf("%w: %v", ErrOAuthExchange, err)
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return "", "", claims, fmt.Errorf("%w: token response carried no id_token", ErrOAuthExchange)
	}
	idToken, err := op.Verifier(&oidc.Config{ClientID: p.ClientID}).Verify(octx, rawIDToken)
	if err != nil {
		return "", "", claims, fmt.Errorf("%w: id_token verification: %v", ErrOAuthExchange, err)
	}
	if st.Nonce == "" || idToken.Nonce != st.Nonce {
		return "", "", claims, ErrOAuthNonceMismatch
	}
	if idToken.Subject == "" {
		return "", "", claims, fmt.Errorf("%w: id_token carried no subject", ErrOAuthExchange)
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", "", claims, fmt.Errorf("%w: id_token claims: %v", ErrOAuthExchange, err)
	}
	return p.Name, idToken.Subject, claims, nil
}

// SubjectLinkedTo reports whether a verified provider subject belongs to the
// account holding a session. See the ATProto twin: without it, a completed
// round trip with ANY account at the provider would satisfy the step-up.
func (s *OAuthService) SubjectLinkedTo(ctx context.Context, provider, subject string, userID uuid.UUID) bool {
	ident, err := s.repo.GetOAuthIdentity(ctx, sqlcgen.GetOAuthIdentityParams{Provider: provider, Subject: subject})
	return err == nil && ident.UserID == userID
}

// OAuthAssertion is one verified provider identity: the subject it attested and
// the claims Vidra consumes, kept apart from what is DONE with them. Nothing
// here is an auth decision on its own — the subject is the identity, and the
// email is a snapshot the create path uses and the login path refuses on.
type OAuthAssertion struct {
	Provider          string
	Subject           string
	Email             string
	EmailVerified     bool
	PreferredUsername string
	Name              string
}

// VerifyIdentity runs the full protocol verification (exchange, JWKS, nonce)
// and returns the attested identity WITHOUT resolving it to an account. The
// authorization code is single-use, so the HTTP layer has exactly one chance to
// verify and must then decide — login, link, or refuse — from the result; this
// is the seam that makes that possible. Login, link and step-up all reach the
// same verifyAssertion for the reason stated on VerifyAssertion: three flows
// verifying a provider differently are three chances to verify one wrongly.
func (s *OAuthService) VerifyIdentity(ctx context.Context, provider, redirectURI, code string, st OAuthState) (OAuthAssertion, error) {
	name, subject, claims, err := s.verifyAssertion(ctx, provider, redirectURI, code, st)
	if err != nil {
		return OAuthAssertion{}, err
	}
	return OAuthAssertion{
		Provider:          name,
		Subject:           subject,
		Email:             strings.TrimSpace(claims.Email),
		EmailVerified:     claims.EmailVerified,
		PreferredUsername: claims.PreferredUsername,
		Name:              claims.Name,
	}, nil
}

// ResolveAssertion turns an already-verified identity into a session, following
// the login / refuse / create ladder.
func (s *OAuthService) ResolveAssertion(ctx context.Context, as OAuthAssertion, userAgent string) (OAuthSession, error) {
	return s.resolveIdentity(ctx, as.Provider, as.Subject, oidcClaims{
		Email:             as.Email,
		EmailVerified:     as.EmailVerified,
		PreferredUsername: as.PreferredUsername,
		Name:              as.Name,
	}, userAgent)
}

// LinkIdentity attaches a verified provider identity to an EXISTING account —
// the account the caller is signed in to. This is the only linking path there
// is: a login can no longer link on an email claim, so linking is always
// something the account holder does deliberately, from a session.
//
// It refuses two ways, and the distinction matters to the person reading it:
// ErrOAuthIdentityClaimed means the subject is somebody ELSE's (moving it would
// be an account switch nobody asked for), while ErrOAuthProviderAlreadyLinked
// means this account already uses this provider with a different subject
// (the (user_id, provider) unique index — unlink first). Re-linking the SAME
// subject to the SAME account is idempotent, because a person who clicks
// "Connect" twice has not made a mistake worth an error.
//
// verifiedEmail is set only when the provider asserted a verified address that
// is the account's OWN address: that is the one case where the round trip
// proves the local mailbox claim, and A05 recorded it as a fact this service
// learned and then threw away.
func (s *OAuthService) LinkIdentity(ctx context.Context, userID uuid.UUID, as OAuthAssertion, handle *string) (OAuthOutcome, error) {
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return "", ErrAccountNotFound
	}
	if !user.IsActive {
		return "", ErrAccountDisabled
	}
	if ident, err := s.repo.GetOAuthIdentity(ctx, sqlcgen.GetOAuthIdentityParams{
		Provider: as.Provider, Subject: as.Subject,
	}); err == nil {
		if ident.UserID != userID {
			return "", ErrOAuthIdentityClaimed
		}
		if handle != nil && (ident.Handle == nil || *ident.Handle != *handle) {
			_ = s.repo.UpdateOAuthIdentityHandle(ctx, sqlcgen.UpdateOAuthIdentityHandleParams{
				Provider: as.Provider, Subject: as.Subject, Handle: handle,
			})
		}
		s.markVerifiedIfOwnAddress(ctx, user, as)
		return OAuthLogin, nil
	}
	if _, err := s.repo.CreateOAuthIdentity(ctx, sqlcgen.CreateOAuthIdentityParams{
		Provider: as.Provider, Subject: as.Subject, UserID: userID, Email: as.Email, Handle: handle,
	}); err != nil {
		if pgconv.IsUniqueViolation(err) {
			// Either (provider, subject) or (user_id, provider). The subject was
			// unclaimed a statement ago, so a race on it is possible; the common
			// case by far is this account already holding another identity for
			// the same provider. Both are refusals, and the second is the one an
			// operator can act on, so re-read to tell them apart.
			if ident, gerr := s.repo.GetOAuthIdentity(ctx, sqlcgen.GetOAuthIdentityParams{
				Provider: as.Provider, Subject: as.Subject,
			}); gerr == nil && ident.UserID != userID {
				return "", ErrOAuthIdentityClaimed
			}
			return "", ErrOAuthProviderAlreadyLinked
		}
		return "", err
	}
	s.markVerifiedIfOwnAddress(ctx, user, as)
	return OAuthLinked, nil
}

// markVerifiedIfOwnAddress records the one email fact a link legitimately
// proves: the provider verified an address, and it is the address this account
// already holds. Best-effort — a failure here must not undo a completed link.
func (s *OAuthService) markVerifiedIfOwnAddress(ctx context.Context, user sqlcgen.User, as OAuthAssertion) {
	if !as.EmailVerified || as.Email == "" || user.EmailVerified {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(user.Email), as.Email) {
		return
	}
	_ = s.repo.SetUserEmailVerified(ctx, user.ID)
}

// SubjectClaimedByOther reports whether a provider subject is already linked to
// an account OTHER than userID — the check that lets a link START refuse before
// walking somebody through a consent screen that could only end in a refusal.
func (s *OAuthService) SubjectClaimedByOther(ctx context.Context, provider, subject string, userID uuid.UUID) bool {
	ident, err := s.repo.GetOAuthIdentity(ctx, sqlcgen.GetOAuthIdentityParams{Provider: provider, Subject: subject})
	return err == nil && ident.UserID != userID
}

// HasProviderLinked reports whether an account already holds an identity for a
// provider (the (user_id, provider) unique index), so the link start can answer
// 422 rather than sending the browser to the IdP for nothing.
func (s *OAuthService) HasProviderLinked(ctx context.Context, userID uuid.UUID, provider string) bool {
	idents, err := s.repo.ListOAuthIdentitiesByUser(ctx, userID)
	if err != nil {
		return false
	}
	for _, id := range idents {
		if id.Provider == provider {
			return true
		}
	}
	return false
}

// resolveIdentity maps a verified (provider, subject, claims) to a local
// session, following the login / refuse / create ladder. There is deliberately
// no link rung: see the file header.
func (s *OAuthService) resolveIdentity(ctx context.Context, provider, subject string, claims oidcClaims, userAgent string) (OAuthSession, error) {
	// Known identity → login. This is the ONLY match rule, and it is the reason
	// a provider-created account is safe: its account is found by the subject
	// that created it, never by the address that subject happens to assert.
	if ident, err := s.repo.GetOAuthIdentity(ctx, sqlcgen.GetOAuthIdentityParams{Provider: provider, Subject: subject}); err == nil {
		user, err := s.repo.GetUserByID(ctx, ident.UserID)
		if err != nil {
			return OAuthSession{}, ErrAccountNotFound
		}
		if !user.IsActive {
			return OAuthSession{}, ErrAccountDisabled
		}
		return s.auth.providerSession(ctx, user, OAuthLogin, userAgent)
	}

	email := strings.TrimSpace(claims.Email)

	// Unknown identity + an existing account with the same email → REFUSE.
	// email_verified is not consulted: a verified claim from a provider this
	// account never linked is still only that provider's word, and honouring it
	// is precisely the owner-takeover A05 measured. The account holder's remedy
	// is to sign in with their own credential and connect the provider from
	// settings, which is what the typed code tells the landing page to say.
	if email != "" {
		if _, err := s.repo.GetUserByEmail(ctx, email); err == nil {
			return OAuthSession{}, ErrOAuthEmailConflict
		}
	}

	// Unknown identity, no matching account → create one. An email is required
	// (users.email is NOT NULL + unique; and account recovery needs it).
	if email == "" {
		return OAuthSession{}, ErrOAuthEmailMissing
	}
	username, err := s.deriveUsername(ctx, claims.PreferredUsername, claims.Name, email)
	if err != nil {
		return OAuthSession{}, err
	}
	// Signup parity with password registration: while the instance awaits its
	// owner (ownerclaim.go), no path may create an account — least of all one
	// that used to mint the admin.
	if err := s.auth.refuseIfOwnerUnclaimed(ctx); err != nil {
		return OAuthSession{}, err
	}
	// Signup parity continued: the instance's registration policy applies to a
	// provider signup exactly as it does to a password one. This is reached only
	// on the CREATE branch — an already-linked identity logged in above — so
	// closing registration never locks out the accounts that already exist.
	requireApproval, err := s.auth.refuseSignupByPolicy()
	if err != nil {
		return OAuthSession{}, err
	}
	if requireApproval {
		return OAuthSession{}, s.auth.RequestProviderRegistration(ctx, ProviderRegistrationInput{
			Username: username, Email: email,
			Provider: provider, Subject: subject,
			IdentityEmail: email, EmailVerified: claims.EmailVerified,
		})
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
		if pgconv.IsUniqueViolation(err) {
			return OAuthSession{}, nameConflict(err)
		}
		return OAuthSession{}, err
	}
	if claims.EmailVerified {
		if err := s.repo.SetUserEmailVerified(ctx, user.ID); err != nil {
			return OAuthSession{}, err
		}
		user.EmailVerified = true
	}
	if _, err := s.repo.CreateOAuthIdentity(ctx, sqlcgen.CreateOAuthIdentityParams{
		Provider: provider, Subject: subject, UserID: user.ID, Email: email,
	}); err != nil {
		return OAuthSession{}, err
	}
	return s.auth.providerSession(ctx, user, OAuthCreated, userAgent)
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
