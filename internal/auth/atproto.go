package auth

// ATProto identity login (see internal/atproto for the protocol client).
//
// This is IDENTITY-ONLY login: a user proves control of an ATProto account
// (Bluesky or any self-hosted PDS) by its handle, and Vidra issues its own
// normal session. We act as a PUBLIC OAuth client with scope `atproto` only and
// DISCARD the PDS tokens the instant the returned DID is verified — we never
// call the PDS on the user's behalf (that remains the separate app-password
// cross-posting feature).
//
// The service mirrors OAuthService: the protocol machinery lives behind the
// ATProtoFlow seam (fully fakeable in tests), this layer runs the auth-critical
// invariants (iss / sub / scope) and the login-or-create identity ladder, and
// the HTTP layer (internal/httpapi) owns the cookie/redirect transport.
//
// Security invariants enforced here (auth-bypass-class if wrong):
//   - iss  (RFC 9207): the callback `iss` MUST equal the issuer bound at Begin.
//   - sub:  the token `sub` MUST equal the DID resolved and bound at Begin.
//   - scope: the granted scope MUST include `atproto`.
// (Bidirectional handle verification, the DPoP dance, and SSRF guarding live in
// internal/atproto; state single-use lives in the HTTP cookie layer.)

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"

	"golang.org/x/oauth2"

	"github.com/vidra/vidra-core/internal/atproto"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// atprotoProvider is the oauth_identities.provider value for ATProto logins. It
// reuses the existing table with subject=<DID>, so identity listing / unlink /
// the last-credential guard all work unchanged.
const atprotoProvider = "atproto"

// atprotoCallbackPath and atprotoClientMetadataPath are the login routes, used
// to derive the OAuth client_id and redirect_uri server-side from PUBLIC_BASE_URL
// (never from request input). They must match the routes mounted in httpapi.
const (
	atprotoCallbackPath       = "/api/v1/auth/atproto/callback"
	atprotoClientMetadataPath = "/api/v1/auth/atproto/client-metadata.json"
)

// atprotoEmailDomain is the reserved (RFC 2606) domain for the synthetic,
// never-routable email a created ATProto account is given. ATProto login carries
// no email, but users.email is NOT NULL + unique; a `.invalid` local address can
// never receive mail, so it cannot become an account-recovery or takeover vector.
const atprotoEmailDomain = "@atproto.invalid"

// Sentinel errors the HTTP layer maps to responses.
var (
	// ErrATProtoDisabled means ATPROTO_LOGIN_ENABLED is false (routes answer 503).
	ErrATProtoDisabled = errors.New("auth: atproto login is disabled")
	// ErrATProtoInvalidHandle means the submitted handle is empty/malformed (422).
	ErrATProtoInvalidHandle = errors.New("auth: atproto handle is invalid")
	// ErrATProtoResolution means identity resolution or auth-server discovery
	// failed for a non-network reason (handle unresolvable, DID mismatch, no PDS,
	// invalid auth-server metadata) — the account/instance is not usable (502).
	ErrATProtoResolution = errors.New("auth: atproto identity could not be resolved")
	// ErrATProtoUpstream means an outbound login hop failed at the network/HTTP
	// level (the PDS / auth server is unreachable or errored) (502).
	ErrATProtoUpstream = errors.New("auth: atproto upstream request failed")
	// ErrATProtoIdentityMismatch means an auth-critical invariant failed: the
	// callback iss did not match, or the token sub/scope did not verify. This is a
	// hard reject — never a session.
	ErrATProtoIdentityMismatch = errors.New("auth: atproto identity verification failed")
)

// ATProtoFlow is the protocol seam the login service drives. *atproto.OAuthClient
// satisfies it directly; unit tests fake the whole protocol.
type ATProtoFlow interface {
	ResolveIdentity(ctx context.Context, handle string) (atproto.Identity, error)
	DiscoverAuthServer(ctx context.Context, pdsURL string) (atproto.AuthServerMeta, error)
	PushAuthRequest(ctx context.Context, meta atproto.AuthServerMeta, in atproto.PARInput) (atproto.PARResult, error)
	ExchangeCode(ctx context.Context, meta atproto.AuthServerMeta, in atproto.ExchangeInput) (atproto.ExchangeResult, error)
}

// ATProtoState carries the per-attempt values minted by Begin and required back
// at Complete. The HTTP layer seals it into a signed, short-lived, httpOnly
// cookie: State, Verifier, and DPoPKeyJWK are attempt secrets and must never be
// logged. The DPoP private JWK rides along because it is flow-ephemeral and
// useless after the single-use exchange (rationale in the httpapi cookie layer).
type ATProtoState struct {
	State         string `json:"state"`
	DID           string `json:"did"`
	Handle        string `json:"handle"`
	Issuer        string `json:"iss"`
	TokenEndpoint string `json:"token_endpoint"`
	PDSURL        string `json:"pds_url"`
	Verifier      string `json:"verifier"`
	DPoPKeyJWK    string `json:"dpop_jwk"`
	DPoPNonce     string `json:"dpop_nonce"`
	ReturnTo      string `json:"return_to"`
}

// ATProtoOAuthService implements ATProto identity login on top of the core auth
// service (which issues the resulting Vidra session).
type ATProtoOAuthService struct {
	repo          OAuthRepository
	auth          *Service
	flow          ATProtoFlow
	enabled       bool
	publicBaseURL string
}

// ATProtoOAuthOption configures the service.
type ATProtoOAuthOption func(*ATProtoOAuthService)

// WithATProtoEnabled sets whether identity login is on (ATPROTO_LOGIN_ENABLED).
// When false, Begin/Complete return ErrATProtoDisabled; the routes still exist
// (stable contract) but answer 503.
func WithATProtoEnabled(enabled bool) ATProtoOAuthOption {
	return func(s *ATProtoOAuthService) { s.enabled = enabled }
}

// WithATProtoPublicBaseURL sets the public origin from which the OAuth client_id
// and redirect_uri are derived server-side (never from request parameters).
func WithATProtoPublicBaseURL(u string) ATProtoOAuthOption {
	return func(s *ATProtoOAuthService) { s.publicBaseURL = strings.TrimRight(u, "/") }
}

// NewATProtoOAuthService builds the ATProto login service. svc issues the Vidra
// session after a verified login; flow is the protocol client (real or fake).
func NewATProtoOAuthService(repo OAuthRepository, svc *Service, flow ATProtoFlow, opts ...ATProtoOAuthOption) *ATProtoOAuthService {
	s := &ATProtoOAuthService{repo: repo, auth: svc, flow: flow}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Enabled reports whether ATProto identity login is turned on.
func (s *ATProtoOAuthService) Enabled() bool { return s.enabled }

// isHTTPSBase reports whether the public base URL is an https origin (production
// mode); otherwise the dev virtual-localhost client is used.
func (s *ATProtoOAuthService) isHTTPSBase() bool {
	return strings.HasPrefix(strings.ToLower(s.publicBaseURL), "https://")
}

// RedirectURI is the OAuth redirect URI (the callback route), derived from
// PUBLIC_BASE_URL. In dev (non-https base) the host is rewritten localhost→127.0.0.1
// per the ATProto virtual-localhost client rule.
func (s *ATProtoOAuthService) RedirectURI() string {
	redirect := s.publicBaseURL + atprotoCallbackPath
	if !s.isHTTPSBase() {
		redirect = rewriteLocalhostTo127(redirect)
	}
	return redirect
}

// ClientID is the OAuth public-client identifier. In production it is the hosted
// client-metadata document URL; in dev it is the spec's virtual-localhost client
// (http://localhost?redirect_uri=…&scope=atproto).
func (s *ATProtoOAuthService) ClientID() string {
	if s.isHTTPSBase() {
		return s.publicBaseURL + atprotoClientMetadataPath
	}
	q := url.Values{}
	q.Set("redirect_uri", s.RedirectURI())
	q.Set("scope", "atproto")
	return "http://localhost?" + q.Encode()
}

// ClientMetadata is the public client-metadata document served at
// client-metadata.json (production hosted-client mode); harmless in dev.
type ClientMetadata struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	ClientURI               string   `json:"client_uri"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	Scope                   string   `json:"scope"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ApplicationType         string   `json:"application_type"`
	DPoPBoundAccessTokens   bool     `json:"dpop_bound_access_tokens"`
}

// ClientMetadata returns the hosted client-metadata document, kept in lock-step
// with the client_id/redirect_uri the flow actually sends.
func (s *ATProtoOAuthService) ClientMetadata() ClientMetadata {
	return ClientMetadata{
		ClientID:                s.ClientID(),
		ClientName:              "Vidra",
		ClientURI:               s.publicBaseURL,
		RedirectURIs:            []string{s.RedirectURI()},
		GrantTypes:              []string{"authorization_code"},
		ResponseTypes:           []string{"code"},
		Scope:                   "atproto",
		TokenEndpointAuthMethod: "none",
		ApplicationType:         "web",
		DPoPBoundAccessTokens:   true,
	}
}

// Begin starts the login flow: it resolves+verifies the handle, discovers the
// auth server, mints a fresh DPoP key + PKCE verifier + state token, performs the
// mandatory PAR, and returns the authorization URL to redirect the browser to
// plus the ATProtoState the HTTP layer seals into the attempt cookie.
func (s *ATProtoOAuthService) Begin(ctx context.Context, handle, returnTo string) (string, ATProtoState, error) {
	if !s.enabled {
		return "", ATProtoState{}, ErrATProtoDisabled
	}
	if strings.TrimSpace(handle) == "" {
		return "", ATProtoState{}, ErrATProtoInvalidHandle
	}

	identity, err := s.flow.ResolveIdentity(ctx, handle)
	if err != nil {
		return "", ATProtoState{}, mapResolveError(err)
	}
	meta, err := s.flow.DiscoverAuthServer(ctx, identity.PDSURL)
	if err != nil {
		return "", ATProtoState{}, mapResolveError(err)
	}

	jwk, err := atproto.GenerateDPoPKeyJWK()
	if err != nil {
		return "", ATProtoState{}, err
	}
	verifier := oauth2.GenerateVerifier()
	stateToken := randomURLToken()

	par, err := s.flow.PushAuthRequest(ctx, meta, atproto.PARInput{
		ClientID:    s.ClientID(),
		RedirectURI: s.RedirectURI(),
		State:       stateToken,
		LoginHint:   identity.Handle,
		Verifier:    verifier,
		DPoPKeyJWK:  jwk,
	})
	if err != nil {
		return "", ATProtoState{}, mapResolveError(err)
	}

	// The authorization redirect carries ONLY client_id + request_uri (PAR pushed
	// every other parameter server-to-server).
	q := url.Values{}
	q.Set("client_id", s.ClientID())
	q.Set("request_uri", par.RequestURI)
	authURL := meta.AuthEndpoint + "?" + q.Encode()

	st := ATProtoState{
		State:         stateToken,
		DID:           identity.DID,
		Handle:        identity.Handle,
		Issuer:        meta.Issuer,
		TokenEndpoint: meta.TokenEndpoint,
		PDSURL:        identity.PDSURL,
		Verifier:      verifier,
		DPoPKeyJWK:    jwk,
		DPoPNonce:     par.DPoPNonce,
		ReturnTo:      returnTo,
	}
	return authURL, st, nil
}

// Complete finishes the flow after the auth server redirected back: it enforces
// the iss / sub / scope invariants, then resolves the verified DID to a Vidra
// session (login an existing linked account, or create a new one). It discards
// the PDS tokens.
func (s *ATProtoOAuthService) Complete(ctx context.Context, st ATProtoState, code, iss, userAgent string) (sqlcgen.User, Tokens, OAuthOutcome, error) {
	if !s.enabled {
		return sqlcgen.User{}, Tokens{}, "", ErrATProtoDisabled
	}
	// Invariant (iss, RFC 9207): the callback issuer MUST match the one bound at
	// Begin, exactly and present.
	if iss == "" || iss != st.Issuer {
		return sqlcgen.User{}, Tokens{}, "", ErrATProtoIdentityMismatch
	}

	res, err := s.flow.ExchangeCode(ctx, atproto.AuthServerMeta{
		Issuer:        st.Issuer,
		TokenEndpoint: st.TokenEndpoint,
	}, atproto.ExchangeInput{
		ClientID:    s.ClientID(),
		RedirectURI: s.RedirectURI(),
		Code:        code,
		Verifier:    st.Verifier,
		DPoPKeyJWK:  st.DPoPKeyJWK,
		DPoPNonce:   st.DPoPNonce,
	})
	if err != nil {
		if errors.Is(err, atproto.ErrOAuthScope) {
			return sqlcgen.User{}, Tokens{}, "", ErrATProtoIdentityMismatch
		}
		return sqlcgen.User{}, Tokens{}, "", ErrATProtoUpstream
	}

	// Invariant (sub): the token subject MUST equal the DID resolved at Begin.
	if res.DID == "" || res.DID != st.DID {
		return sqlcgen.User{}, Tokens{}, "", ErrATProtoIdentityMismatch
	}
	// Invariant (scope): the grant MUST include atproto (belt-and-braces with the
	// atproto client's own check, so a faked flow cannot skip it).
	if !scopeHasAtproto(res.Scope) {
		return sqlcgen.User{}, Tokens{}, "", ErrATProtoIdentityMismatch
	}

	return s.resolveATProtoIdentity(ctx, st, res.DID, userAgent)
}

// resolveATProtoIdentity maps a verified DID to a Vidra session: an already-linked
// identity logs its account in; an unknown DID creates a fresh account. There is
// no email-linking branch (ATProto login carries no verified email — unlike OIDC).
func (s *ATProtoOAuthService) resolveATProtoIdentity(ctx context.Context, st ATProtoState, did, userAgent string) (sqlcgen.User, Tokens, OAuthOutcome, error) {
	handle := atprotoHandlePtr(st.Handle)

	// Known identity → login.
	if ident, err := s.repo.GetOAuthIdentity(ctx, sqlcgen.GetOAuthIdentityParams{Provider: atprotoProvider, Subject: did}); err == nil {
		user, err := s.repo.GetUserByID(ctx, ident.UserID)
		if err != nil {
			return sqlcgen.User{}, Tokens{}, "", ErrAccountNotFound
		}
		if !user.IsActive {
			return sqlcgen.User{}, Tokens{}, "", ErrAccountDisabled
		}
		// Refresh the stored display handle: ATProto handles are mutable, so keep
		// it current on every re-login. Display-only, so a failure here never
		// blocks the session.
		if handle != nil && (ident.Handle == nil || *ident.Handle != *handle) {
			_ = s.repo.UpdateOAuthIdentityHandle(ctx, sqlcgen.UpdateOAuthIdentityHandleParams{
				Provider: atprotoProvider, Subject: did, Handle: handle,
			})
		}
		tokens, err := s.auth.issueTokens(ctx, user, userAgent)
		if err != nil {
			return sqlcgen.User{}, Tokens{}, "", err
		}
		return user, tokens, OAuthLogin, nil
	}

	// Unknown identity → create an account. The username derives from the handle
	// (first label, then the full handle); the account is passwordless and gets a
	// synthetic non-routable email.
	firstLabel, _, _ := strings.Cut(st.Handle, ".")
	username, err := deriveUsername(ctx, s.repo, firstLabel, st.Handle, "")
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
		// Synthetic, RFC 2606-reserved, never-routable email (ATProto login has no
		// verified email, and users.email is NOT NULL + unique).
		Email: sanitizeDIDForEmail(did) + atprotoEmailDomain,
		// No password: ATProto is the account's credential. An empty hash can never
		// verify (bcrypt rejects it), so password login stays impossible until the
		// user sets one via the password-reset flow.
		PasswordHash:   "",
		Role:           "user",
		HistoryEnabled: s.auth.newUserHistoryEnabled(),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return sqlcgen.User{}, Tokens{}, "", ErrConflict
		}
		return sqlcgen.User{}, Tokens{}, "", err
	}
	// Deliberately NOT SetUserEmailVerified: the synthetic address is unverifiable
	// by design and the DID — not the email — is the account's identity.
	if _, err := s.repo.CreateOAuthIdentity(ctx, sqlcgen.CreateOAuthIdentityParams{
		Provider: atprotoProvider, Subject: did, UserID: user.ID, Email: "", Handle: handle,
	}); err != nil {
		if isUniqueViolation(err) {
			return sqlcgen.User{}, Tokens{}, "", ErrConflict
		}
		return sqlcgen.User{}, Tokens{}, "", err
	}
	tokens, err := s.auth.issueTokens(ctx, user, userAgent)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, "", err
	}
	return user, tokens, OAuthCreated, nil
}

// mapResolveError maps an atproto client error to a login-service sentinel: bad
// handle → invalid; network/HTTP failure → upstream; everything else (unresolvable
// handle, DID mismatch, no PDS, invalid auth-server metadata) → resolution.
func mapResolveError(err error) error {
	switch {
	case errors.Is(err, atproto.ErrInvalidHandle):
		return ErrATProtoInvalidHandle
	case errors.Is(err, atproto.ErrOAuthUpstream):
		return ErrATProtoUpstream
	default:
		return ErrATProtoResolution
	}
}

// atprotoHandlePtr trims a resolved ATProto handle and returns it as a pointer
// for the nullable oauth_identities.handle column, or nil when empty (so a
// missing handle stays NULL rather than an empty string).
func atprotoHandlePtr(handle string) *string {
	h := strings.TrimSpace(handle)
	if h == "" {
		return nil
	}
	return &h
}

// scopeHasAtproto reports whether a space-delimited OAuth scope grants atproto.
func scopeHasAtproto(scope string) bool {
	for _, s := range strings.Fields(scope) {
		if s == "atproto" {
			return true
		}
	}
	return false
}

// sanitizeDIDForEmail builds the local part of the synthetic account email from a
// DID: lowercased, restricted to [a-z0-9.-] with colons mapped to dashes. A local
// part that would be empty or exceed 60 chars is replaced by a stable
// "did-"+hex(sha256(did))[:32] so it stays a valid, unique, bounded local address.
func sanitizeDIDForEmail(did string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(did) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		case r == ':':
			b.WriteByte('-')
		}
	}
	local := b.String()
	if local == "" || len(local) > 60 {
		sum := sha256.Sum256([]byte(did))
		local = "did-" + hex.EncodeToString(sum[:])[:32]
	}
	return local
}

// rewriteLocalhostTo127 rewrites a URL's localhost host to 127.0.0.1 (preserving
// the port), as required by the ATProto virtual-localhost dev client.
func rewriteLocalhostTo127(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Hostname() == "localhost" {
		if p := u.Port(); p != "" {
			u.Host = "127.0.0.1:" + p
		} else {
			u.Host = "127.0.0.1"
		}
	}
	return u.String()
}
