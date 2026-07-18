package atproto

// ATProto OAuth client for IDENTITY LOGIN (distinct from the app-password
// cross-posting client in xrpc.go). It implements the public-client half of the
// ATProto OAuth profile: auth-server discovery, Pushed Authorization Requests
// (PAR, RFC 9126), and the DPoP-bound authorization-code token exchange. We act
// as a public client with scope `atproto` only and DISCARD the returned PDS
// tokens the instant identity is verified — we never call the PDS on the user's
// behalf.
//
// Every outbound hop is SSRF-guarded exactly like xrpc.go: https-only + a
// dial-time public-IP guard (urlsafety.Guard), with an allowPrivate escape hatch
// for dev/tests only. Response bodies are size-capped and per-hop timeouts apply.
// Errors carry only safe, structured, machine-readable info (never upstream
// bodies, tokens, PKCE verifiers, or DPoP key material).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

const (
	// loginHTTPTimeout bounds a single outbound hop in the login flow.
	loginHTTPTimeout = 10 * time.Second

	// maxMetadataBody caps DID-document, discovery-metadata, and OAuth JSON
	// response bodies (defence in depth; these are small JSON documents).
	maxMetadataBody = 512 << 10 // 512 KiB
	// maxWellKnownDID caps the /.well-known/atproto-did body (a bare DID string).
	maxWellKnownDID = 8 << 10 // 8 KiB

	// defaultPLCURL is the fixed, trusted did:plc directory host.
	defaultPLCURL = "https://plc.directory"

	// loginScope is the only OAuth scope we ever request: identity, nothing else.
	loginScope = "atproto"
)

// Sentinel errors the login service maps to responses. They carry no upstream
// body, credential, or key material.
var (
	// ErrHandleNotFound means the handle could not be resolved to a DID by either
	// DNS TXT or the HTTPS well-known method.
	ErrHandleNotFound = errors.New("atproto: handle could not be resolved")
	// ErrUnsupportedDID means the resolved DID uses a method we do not support
	// (only did:plc and did:web are handled).
	ErrUnsupportedDID = errors.New("atproto: unsupported DID method")
	// ErrDIDMismatch means the bidirectional identity binding failed: the DID
	// document id disagreed with the DID, or its alsoKnownAs did not authoritatively
	// claim the handle. Treating this as fatal is what makes handle→DID trustworthy.
	ErrDIDMismatch = errors.New("atproto: DID document does not match the handle")
	// ErrPDSNotFound means the DID document carried no usable atproto PDS service.
	ErrPDSNotFound = errors.New("atproto: no atproto PDS in the DID document")
	// ErrBadAuthServerMeta means the auth-server discovery documents failed one of
	// the mandatory OAuth-profile validations (issuer, endpoints, scopes, DPoP alg,
	// PAR requirement, …).
	ErrBadAuthServerMeta = errors.New("atproto: invalid authorization server metadata")
	// ErrOAuthUpstream means an outbound login hop failed at the network/HTTP level
	// (unreachable, non-2xx, unparseable). A *loginHTTPError satisfies errors.Is on
	// this while carrying a safe status for diagnostics.
	ErrOAuthUpstream = errors.New("atproto: login upstream request failed")
	// ErrOAuthScope means the token response did not grant the `atproto` scope.
	ErrOAuthScope = errors.New("atproto: token response is missing the atproto scope")
)

// loginHTTPError is a non-2xx login response. Like XRPCError its message is SAFE
// to log/return: only the operation and HTTP status, never the URL, body, or any
// credential. It reports as ErrOAuthUpstream so callers can errors.Is uniformly.
type loginHTTPError struct {
	Op     string
	Status int
}

func (e *loginHTTPError) Error() string {
	return fmt.Sprintf("atproto: login %s failed: status %d", e.Op, e.Status)
}

func (e *loginHTTPError) Is(target error) bool { return target == ErrOAuthUpstream }

// OAuthClient performs the SSRF-guarded ATProto login protocol. Build it with
// NewOAuthClient; it is safe for concurrent use.
type OAuthClient struct {
	allowPrivate bool
	// plcURL is the did:plc directory base (defaultPLCURL in production; tests
	// point it at an httptest server via WithPLCURL).
	plcURL string
	// lookupTXT resolves DNS TXT records for handle verification. Defaults to
	// net.DefaultResolver.LookupTXT; tests inject a fake via WithTXTResolver.
	lookupTXT func(ctx context.Context, name string) ([]string, error)
	// wellKnownBase, when set, overrides the https://<handle> origin used for the
	// /.well-known/atproto-did fetch. DEV/TEST-ONLY (WithHandleWellKnownBase).
	wellKnownBase string
}

// OAuthClientOption customises the login client.
type OAuthClientOption func(*OAuthClient)

// WithOAuthClientAllowPrivate relaxes the SSRF guard (loopback/private + http) on
// every login hop. DEVELOPMENT/TEST-ONLY; never enable it in production.
func WithOAuthClientAllowPrivate(allow bool) OAuthClientOption {
	return func(c *OAuthClient) { c.allowPrivate = allow }
}

// NewOAuthClient builds the login client used in production wiring.
func NewOAuthClient(opts ...OAuthClientOption) *OAuthClient {
	c := &OAuthClient{
		plcURL:    defaultPLCURL,
		lookupTXT: net.DefaultResolver.LookupTXT,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// guard returns the SSRF policy for this client.
func (c *OAuthClient) guard() urlsafety.Guard {
	return urlsafety.Guard{AllowPrivate: c.allowPrivate}
}

// validateFetchURL enforces the outbound-URL policy for a login hop: an
// http/https, public, fetchable origin — https specifically unless the guard is
// relaxed (dev/test). Returns the normalised URL string.
func (c *OAuthClient) validateFetchURL(raw string) (string, error) {
	u, err := c.guard().ValidateURL(raw)
	if err != nil {
		return "", ErrOAuthUpstream
	}
	if !c.allowPrivate && u.Scheme != "https" {
		return "", ErrOAuthUpstream
	}
	return u.String(), nil
}

// getGuarded performs a size-capped, SSRF-guarded GET and returns the (bounded)
// body. op names the hop for a safe error only.
func (c *OAuthClient) getGuarded(ctx context.Context, op, rawURL string, cap int64) ([]byte, error) {
	safeURL, err := c.validateFetchURL(rawURL)
	if err != nil {
		return nil, err
	}
	client := c.guard().NewClient(loginHTTPTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, safeURL, nil)
	if err != nil {
		return nil, &loginHTTPError{Op: op}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, ErrOAuthUpstream
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, cap))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &loginHTTPError{Op: op, Status: resp.StatusCode}
	}
	return body, nil
}

// AuthServerMeta is the validated subset of the auth server's discovery document
// the login flow needs.
type AuthServerMeta struct {
	// Issuer is the exact issuer string (also the RFC 9207 `iss` we bind and later
	// require back in the callback).
	Issuer string
	// PAREndpoint is the pushed_authorization_request_endpoint (mandatory here).
	PAREndpoint string
	// AuthEndpoint is the authorization_endpoint the browser is redirected to.
	AuthEndpoint string
	// TokenEndpoint is the token_endpoint for the code exchange.
	TokenEndpoint string
}

// protectedResource is the /.well-known/oauth-protected-resource document.
type protectedResource struct {
	AuthorizationServers []string `json:"authorization_servers"`
}

// authServerMetadata is the /.well-known/oauth-authorization-server document.
type authServerMetadata struct {
	Issuer                                     string   `json:"issuer"`
	PAREndpoint                                string   `json:"pushed_authorization_request_endpoint"`
	AuthorizationEndpoint                      string   `json:"authorization_endpoint"`
	TokenEndpoint                              string   `json:"token_endpoint"`
	ScopesSupported                            []string `json:"scopes_supported"`
	ResponseTypesSupported                     []string `json:"response_types_supported"`
	GrantTypesSupported                        []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported              []string `json:"code_challenge_methods_supported"`
	DPoPSigningAlgValuesSupported              []string `json:"dpop_signing_alg_values_supported"`
	AuthorizationResponseISSParameterSupported bool     `json:"authorization_response_iss_parameter_supported"`
	RequirePushedAuthorizationRequests         bool     `json:"require_pushed_authorization_requests"`
	ClientIDMetadataDocumentSupported          bool     `json:"client_id_metadata_document_supported"`
}

// DiscoverAuthServer resolves and validates the auth server for a PDS: it reads
// the PDS protected-resource document (exactly one https authorization server),
// then that server's authorization-server metadata, enforcing every mandatory
// element of the ATProto OAuth profile before returning. Any violation is fatal
// (ErrBadAuthServerMeta) — the discovery documents are attacker-influenced.
func (c *OAuthClient) DiscoverAuthServer(ctx context.Context, pdsURL string) (AuthServerMeta, error) {
	prBody, err := c.getGuarded(ctx, "protected-resource", strings.TrimRight(pdsURL, "/")+"/.well-known/oauth-protected-resource", maxMetadataBody)
	if err != nil {
		return AuthServerMeta{}, err
	}
	var pr protectedResource
	if err := json.Unmarshal(prBody, &pr); err != nil {
		return AuthServerMeta{}, ErrBadAuthServerMeta
	}
	if len(pr.AuthorizationServers) != 1 {
		return AuthServerMeta{}, ErrBadAuthServerMeta
	}
	authOrigin := strings.TrimSpace(pr.AuthorizationServers[0])
	if !c.validOrigin(authOrigin) {
		return AuthServerMeta{}, ErrBadAuthServerMeta
	}

	metaBody, err := c.getGuarded(ctx, "authserver-metadata", strings.TrimRight(authOrigin, "/")+"/.well-known/oauth-authorization-server", maxMetadataBody)
	if err != nil {
		return AuthServerMeta{}, err
	}
	var m authServerMetadata
	if err := json.Unmarshal(metaBody, &m); err != nil {
		return AuthServerMeta{}, ErrBadAuthServerMeta
	}

	// issuer must be the exact origin we fetched from.
	if m.Issuer != authOrigin || !c.validOrigin(m.Issuer) {
		return AuthServerMeta{}, ErrBadAuthServerMeta
	}
	if m.PAREndpoint == "" || m.AuthorizationEndpoint == "" || m.TokenEndpoint == "" {
		return AuthServerMeta{}, ErrBadAuthServerMeta
	}
	if !contains(m.ScopesSupported, loginScope) ||
		!contains(m.DPoPSigningAlgValuesSupported, "ES256") ||
		!contains(m.ResponseTypesSupported, "code") ||
		!contains(m.GrantTypesSupported, "authorization_code") ||
		!contains(m.CodeChallengeMethodsSupported, "S256") {
		return AuthServerMeta{}, ErrBadAuthServerMeta
	}
	if !m.AuthorizationResponseISSParameterSupported ||
		!m.RequirePushedAuthorizationRequests ||
		!m.ClientIDMetadataDocumentSupported {
		return AuthServerMeta{}, ErrBadAuthServerMeta
	}
	return AuthServerMeta{
		Issuer:        m.Issuer,
		PAREndpoint:   m.PAREndpoint,
		AuthEndpoint:  m.AuthorizationEndpoint,
		TokenEndpoint: m.TokenEndpoint,
	}, nil
}

// PARInput carries the parameters for a Pushed Authorization Request. The DPoP
// private key travels as a JWK string so the caller never handles the concrete
// key type; the PKCE challenge is derived from Verifier here.
type PARInput struct {
	ClientID    string
	RedirectURI string
	State       string
	LoginHint   string
	// Verifier is the PKCE code verifier; its S256 challenge is sent, the verifier
	// is held back for the token exchange.
	Verifier string
	// DPoPKeyJWK is the marshalled per-flow DPoP private key.
	DPoPKeyJWK string
}

// PARResult is the outcome of a successful PAR.
type PARResult struct {
	// RequestURI is the one-time request_uri to hand to the authorization endpoint.
	RequestURI string
	// DPoPNonce is the server nonce from the PAR response, carried into the token
	// request so its first DPoP proof already includes the nonce.
	DPoPNonce string
}

// parResponse is the PAR success body.
type parResponse struct {
	RequestURI string `json:"request_uri"`
	ExpiresIn  int    `json:"expires_in"`
}

// PushAuthRequest performs the mandatory PAR: a DPoP-bound, form-encoded POST to
// the auth server's pushed_authorization_request_endpoint. It handles the
// use_dpop_nonce dance (a 400 with a DPoP-Nonce header → rebuild the proof with
// the nonce claim and retry once) and returns the request_uri plus the server
// nonce to carry into the token exchange.
func (c *OAuthClient) PushAuthRequest(ctx context.Context, meta AuthServerMeta, in PARInput) (PARResult, error) {
	key, err := parseDPoPKey(in.DPoPKeyJWK)
	if err != nil {
		return PARResult{}, err
	}
	form := url.Values{
		"client_id":             {in.ClientID},
		"response_type":         {"code"},
		"code_challenge":        {oauth2.S256ChallengeFromVerifier(in.Verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {in.State},
		"redirect_uri":          {in.RedirectURI},
		"scope":                 {loginScope},
	}
	if in.LoginHint != "" {
		form.Set("login_hint", in.LoginHint)
	}
	body, nonce, err := c.doDPoPForm(ctx, "par", meta.PAREndpoint, key, "", form)
	if err != nil {
		return PARResult{}, err
	}
	var pr parResponse
	if err := json.Unmarshal(body, &pr); err != nil || pr.RequestURI == "" {
		return PARResult{}, ErrOAuthUpstream
	}
	return PARResult{RequestURI: pr.RequestURI, DPoPNonce: nonce}, nil
}

// ExchangeInput carries the authorization-code token-exchange parameters.
type ExchangeInput struct {
	ClientID    string
	RedirectURI string
	Code        string
	Verifier    string
	DPoPKeyJWK  string
	// DPoPNonce is the nonce captured from the PAR response (or empty).
	DPoPNonce string
}

// ExchangeResult is the identity extracted from a successful, scope-checked token
// response. The access/refresh tokens themselves are deliberately discarded here.
type ExchangeResult struct {
	// DID is the token response `sub` — the caller MUST verify it equals the DID
	// bound at Begin (the identity invariant this whole flow protects).
	DID string
	// Scope is the granted scope (already checked to include atproto).
	Scope string
}

// tokenResponse is the token-endpoint success body.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Sub         string `json:"sub"`
	Scope       string `json:"scope"`
}

// ExchangeCode performs the DPoP-bound authorization-code exchange and returns
// only the verified identity (sub + scope). It runs the use_dpop_nonce dance,
// enforces the `atproto` scope invariant, and then DISCARDS the tokens: we never
// keep or use the PDS access/refresh tokens.
func (c *OAuthClient) ExchangeCode(ctx context.Context, meta AuthServerMeta, in ExchangeInput) (ExchangeResult, error) {
	key, err := parseDPoPKey(in.DPoPKeyJWK)
	if err != nil {
		return ExchangeResult{}, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {in.Code},
		"redirect_uri":  {in.RedirectURI},
		"code_verifier": {in.Verifier},
		"client_id":     {in.ClientID},
	}
	body, _, err := c.doDPoPForm(ctx, "token", meta.TokenEndpoint, key, in.DPoPNonce, form)
	if err != nil {
		return ExchangeResult{}, err
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil || tr.Sub == "" {
		return ExchangeResult{}, ErrOAuthUpstream
	}
	if !scopeContains(tr.Scope, loginScope) {
		return ExchangeResult{}, ErrOAuthScope
	}
	return ExchangeResult{DID: tr.Sub, Scope: tr.Scope}, nil
}

// doDPoPForm POSTs a form with a DPoP proof, running the single-retry
// use_dpop_nonce dance. It returns the (bounded) response body and the
// DPoP-Nonce from the final response (to carry forward). op names the hop safely.
func (c *OAuthClient) doDPoPForm(ctx context.Context, op, endpoint string, key *dpopKey, initialNonce string, form url.Values) ([]byte, string, error) {
	safeURL, err := c.validateFetchURL(endpoint)
	if err != nil {
		return nil, "", err
	}
	client := c.guard().NewClient(loginHTTPTimeout)
	nonce := initialNonce
	for attempt := 0; attempt < 2; attempt++ {
		proof, err := key.Proof(http.MethodPost, safeURL, nonce)
		if err != nil {
			return nil, "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, safeURL, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, "", &loginHTTPError{Op: op}
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("DPoP", proof)
		resp, err := client.Do(req)
		if err != nil {
			return nil, "", ErrOAuthUpstream
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBody))
		_ = resp.Body.Close()
		respNonce := resp.Header.Get("DPoP-Nonce")
		if attempt == 0 && resp.StatusCode == http.StatusBadRequest && respNonce != "" && isUseDPoPNonce(respBody) {
			// Server wants us to bind the proof to its nonce: rebuild and retry once.
			nonce = respNonce
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, respNonce, &loginHTTPError{Op: op, Status: resp.StatusCode}
		}
		return respBody, respNonce, nil
	}
	return nil, "", ErrOAuthUpstream
}

// isUseDPoPNonce reports whether an OAuth error body is {"error":"use_dpop_nonce"}.
func isUseDPoPNonce(body []byte) bool {
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return false
	}
	return out.Error == "use_dpop_nonce"
}

// validOrigin reports whether raw is a bare origin (scheme://host[:port], no
// path/query/fragment/userinfo). Production requires https; the allowPrivate
// dev/test knob also accepts http so httptest servers can stand in for a PDS or
// auth server.
func (c *OAuthClient) validOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Host == "" || u.User != nil {
		return false
	}
	if u.Scheme != "https" && !(c.allowPrivate && u.Scheme == "http") {
		return false
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return true
}

// contains reports whether s is present in list (case-sensitive exact match).
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// scopeContains reports whether a space-delimited scope string grants scope.
func scopeContains(scope, want string) bool {
	for _, s := range strings.Fields(scope) {
		if s == want {
			return true
		}
	}
	return false
}
