package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/atproto"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeATProtoFlow fakes the whole ATProto protocol so the login service is tested
// without any network. Fields are set per-test to drive each branch.
type fakeATProtoFlow struct {
	identity    atproto.Identity
	meta        atproto.AuthServerMeta
	par         atproto.PARResult
	exchange    atproto.ExchangeResult
	resolveErr  error
	exchangeErr error
}

func (f *fakeATProtoFlow) ResolveIdentity(_ context.Context, _ string) (atproto.Identity, error) {
	if f.resolveErr != nil {
		return atproto.Identity{}, f.resolveErr
	}
	return f.identity, nil
}

func (f *fakeATProtoFlow) DiscoverAuthServer(_ context.Context, _ string) (atproto.AuthServerMeta, error) {
	return f.meta, nil
}

func (f *fakeATProtoFlow) PushAuthRequest(_ context.Context, _ atproto.AuthServerMeta, _ atproto.PARInput) (atproto.PARResult, error) {
	return f.par, nil
}

func (f *fakeATProtoFlow) ExchangeCode(_ context.Context, _ atproto.AuthServerMeta, _ atproto.ExchangeInput) (atproto.ExchangeResult, error) {
	if f.exchangeErr != nil {
		return atproto.ExchangeResult{}, f.exchangeErr
	}
	return f.exchange, nil
}

// defaultFlow is a happy-path flow for handle alice.example / did:plc:alice.
func defaultFlow() *fakeATProtoFlow {
	return &fakeATProtoFlow{
		identity: atproto.Identity{DID: "did:plc:alice", Handle: "alice.example", PDSURL: "https://pds.example"},
		meta: atproto.AuthServerMeta{
			Issuer:        "https://auth.example",
			PAREndpoint:   "https://auth.example/par",
			AuthEndpoint:  "https://auth.example/authorize",
			TokenEndpoint: "https://auth.example/token",
		},
		par:      atproto.PARResult{RequestURI: "urn:ietf:params:oauth:request_uri:abc", DPoPNonce: "nonce-1"},
		exchange: atproto.ExchangeResult{DID: "did:plc:alice", Scope: "atproto"},
	}
}

func newATProtoTestService(repo *oauthFakeRepo, flow ATProtoFlow, enabled bool) *ATProtoOAuthService {
	issuer := NewTokenIssuer("atproto-test-secret-atproto-test-0", "vidra", "vidra", time.Minute)
	svc := NewService(repo.fakeRepo, issuer, time.Hour)
	return NewATProtoOAuthService(repo, svc, flow,
		WithATProtoEnabled(enabled),
		WithATProtoPublicBaseURL("https://vidra.test"),
	)
}

func TestATProtoBeginBuildsAuthorizeURL(t *testing.T) {
	repo := newOAuthFakeRepo()
	flow := defaultFlow()
	s := newATProtoTestService(repo, flow, true)

	authURL, st, err := s.Begin(context.Background(), "alice.example", "/home")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authURL: %v", err)
	}
	if u.Scheme+"://"+u.Host+u.Path != "https://auth.example/authorize" {
		t.Errorf("authorize endpoint = %q", u.Scheme+"://"+u.Host+u.Path)
	}
	q := u.Query()
	if q.Get("client_id") != "https://vidra.test/api/v1/auth/atproto/client-metadata.json" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("request_uri") != "urn:ietf:params:oauth:request_uri:abc" {
		t.Errorf("request_uri = %q", q.Get("request_uri"))
	}
	// The authorize URL carries ONLY client_id + request_uri.
	if len(q) != 2 {
		t.Errorf("authorize query params = %v, want only client_id + request_uri", q)
	}
	// State binds everything Complete needs.
	if st.DID != "did:plc:alice" || st.Handle != "alice.example" || st.Issuer != "https://auth.example" ||
		st.TokenEndpoint != "https://auth.example/token" || st.DPoPNonce != "nonce-1" || st.ReturnTo != "/home" {
		t.Errorf("state = %+v", st)
	}
	if st.Verifier == "" || st.DPoPKeyJWK == "" || st.State == "" {
		t.Error("state must carry a PKCE verifier, a DPoP key JWK, and a state token")
	}
	// The DPoP JWK in state must be a usable private key (not just any string).
	if !strings.Contains(st.DPoPKeyJWK, `"d"`) {
		t.Errorf("state DPoP JWK is not a private key: %s", st.DPoPKeyJWK)
	}
}

func TestATProtoCompleteCreatesThenLogsIn(t *testing.T) {
	repo := newOAuthFakeRepo()
	flow := defaultFlow()
	s := newATProtoTestService(repo, flow, true)
	ctx := context.Background()

	_, st, err := s.Begin(ctx, "alice.example", "/home")
	if err != nil {
		t.Fatal(err)
	}

	user, tokens, outcome, err := s.Complete(ctx, st, "auth-code", st.Issuer, "ua")
	if err != nil || outcome != OAuthCreated {
		t.Fatalf("create: outcome=%v err=%v", outcome, err)
	}
	// Username derives from the handle's first label; first account bootstraps admin.
	if user.Username != "alice" || user.Role != "admin" {
		t.Errorf("created user = %+v; want username alice, admin", user)
	}
	// Passwordless, synthetic non-routable email, NOT marked verified.
	if user.PasswordHash != "" {
		t.Error("atproto-created account must have no password hash")
	}
	if user.Email != "did-plc-alice@atproto.invalid" {
		t.Errorf("email = %q, want did-plc-alice@atproto.invalid", user.Email)
	}
	if user.EmailVerified {
		t.Error("atproto-created account must not be marked email-verified")
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Error("create must issue a full session")
	}

	// The same DID coming back logs into the same account (no duplicate).
	user2, _, outcome, err := s.Complete(ctx, st, "auth-code-2", st.Issuer, "ua")
	if err != nil || outcome != OAuthLogin || user2.ID != user.ID {
		t.Fatalf("login: outcome=%v err=%v same-id=%v", outcome, err, user2.ID == user.ID)
	}
	if n, _ := repo.CountUsers(ctx); n != 1 {
		t.Errorf("users = %d, want 1 (login must not duplicate)", n)
	}
}

// TestATProtoCompleteCapturesAndRefreshesHandle proves the resolved handle is
// persisted on the oauth_identities link at creation time and refreshed on a
// later re-login (remote handles are mutable). The DID stays the identity key.
func TestATProtoCompleteCapturesAndRefreshesHandle(t *testing.T) {
	repo := newOAuthFakeRepo()
	flow := defaultFlow()
	s := newATProtoTestService(repo, flow, true)
	ctx := context.Background()

	_, st, err := s.Begin(ctx, "alice.example", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, outcome, err := s.Complete(ctx, st, "code", st.Issuer, "ua"); err != nil || outcome != OAuthCreated {
		t.Fatalf("create: outcome=%v err=%v", outcome, err)
	}
	ident, err := repo.GetOAuthIdentity(ctx, sqlcgen.GetOAuthIdentityParams{Provider: "atproto", Subject: "did:plc:alice"})
	if err != nil {
		t.Fatalf("identity not stored: %v", err)
	}
	if ident.Handle == nil || *ident.Handle != "alice.example" {
		t.Fatalf("handle captured at link = %v, want alice.example", ident.Handle)
	}

	// The handle moved upstream; a re-login (same DID) must refresh the stored copy.
	flow.identity.Handle = "alice.moved.example"
	_, st2, err := s.Begin(ctx, "alice.moved.example", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, outcome, err := s.Complete(ctx, st2, "code2", st2.Issuer, "ua"); err != nil || outcome != OAuthLogin {
		t.Fatalf("re-login: outcome=%v err=%v", outcome, err)
	}
	ident2, _ := repo.GetOAuthIdentity(ctx, sqlcgen.GetOAuthIdentityParams{Provider: "atproto", Subject: "did:plc:alice"})
	if ident2.Handle == nil || *ident2.Handle != "alice.moved.example" {
		t.Fatalf("handle after re-login = %v, want alice.moved.example (refreshed)", ident2.Handle)
	}
}

func TestATProtoCompleteUsernameDedupe(t *testing.T) {
	repo := newOAuthFakeRepo()
	repo.names["alice"] = true // a local "alice" already exists
	flow := defaultFlow()
	s := newATProtoTestService(repo, flow, true)
	ctx := context.Background()

	_, st, err := s.Begin(ctx, "alice.example", "")
	if err != nil {
		t.Fatal(err)
	}
	user, _, outcome, err := s.Complete(ctx, st, "code", st.Issuer, "ua")
	if err != nil || outcome != OAuthCreated {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	if user.Username != "alice2" {
		t.Errorf("username = %q, want alice2 (deduped)", user.Username)
	}
}

func TestATProtoCompleteRejectsInvariantFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("iss mismatch", func(t *testing.T) {
		repo := newOAuthFakeRepo()
		s := newATProtoTestService(repo, defaultFlow(), true)
		_, st, _ := s.Begin(ctx, "alice.example", "")
		if _, _, _, err := s.Complete(ctx, st, "code", "https://evil.example", "ua"); !errors.Is(err, ErrATProtoIdentityMismatch) {
			t.Fatalf("err = %v, want ErrATProtoIdentityMismatch", err)
		}
		// An absent iss is equally fatal.
		if _, _, _, err := s.Complete(ctx, st, "code", "", "ua"); !errors.Is(err, ErrATProtoIdentityMismatch) {
			t.Fatalf("absent iss err = %v, want ErrATProtoIdentityMismatch", err)
		}
		if n, _ := repo.CountUsers(ctx); n != 0 {
			t.Error("a rejected attempt must not create an account")
		}
	})

	t.Run("sub mismatch", func(t *testing.T) {
		repo := newOAuthFakeRepo()
		flow := defaultFlow()
		flow.exchange = atproto.ExchangeResult{DID: "did:plc:someoneelse", Scope: "atproto"}
		s := newATProtoTestService(repo, flow, true)
		_, st, _ := s.Begin(ctx, "alice.example", "")
		if _, _, _, err := s.Complete(ctx, st, "code", st.Issuer, "ua"); !errors.Is(err, ErrATProtoIdentityMismatch) {
			t.Fatalf("err = %v, want ErrATProtoIdentityMismatch", err)
		}
	})

	t.Run("scope missing", func(t *testing.T) {
		repo := newOAuthFakeRepo()
		flow := defaultFlow()
		flow.exchange = atproto.ExchangeResult{DID: "did:plc:alice", Scope: "transition:generic"}
		s := newATProtoTestService(repo, flow, true)
		_, st, _ := s.Begin(ctx, "alice.example", "")
		if _, _, _, err := s.Complete(ctx, st, "code", st.Issuer, "ua"); !errors.Is(err, ErrATProtoIdentityMismatch) {
			t.Fatalf("err = %v, want ErrATProtoIdentityMismatch", err)
		}
	})

	t.Run("upstream exchange failure", func(t *testing.T) {
		repo := newOAuthFakeRepo()
		flow := defaultFlow()
		flow.exchangeErr = atproto.ErrOAuthUpstream
		s := newATProtoTestService(repo, flow, true)
		_, st, _ := s.Begin(ctx, "alice.example", "")
		if _, _, _, err := s.Complete(ctx, st, "code", st.Issuer, "ua"); !errors.Is(err, ErrATProtoUpstream) {
			t.Fatalf("err = %v, want ErrATProtoUpstream", err)
		}
	})
}

func TestATProtoBeginMapsResolutionErrors(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		resolveErr error
		want       error
	}{
		"invalid handle": {atproto.ErrInvalidHandle, ErrATProtoInvalidHandle},
		"unresolvable":   {atproto.ErrHandleNotFound, ErrATProtoResolution},
		"did mismatch":   {atproto.ErrDIDMismatch, ErrATProtoResolution},
		"upstream":       {atproto.ErrOAuthUpstream, ErrATProtoUpstream},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			repo := newOAuthFakeRepo()
			flow := defaultFlow()
			flow.resolveErr = tc.resolveErr
			s := newATProtoTestService(repo, flow, true)
			if _, _, err := s.Begin(ctx, "alice.example", ""); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestATProtoDisabled(t *testing.T) {
	repo := newOAuthFakeRepo()
	s := newATProtoTestService(repo, defaultFlow(), false)
	if _, _, err := s.Begin(context.Background(), "alice.example", ""); !errors.Is(err, ErrATProtoDisabled) {
		t.Fatalf("Begin disabled err = %v, want ErrATProtoDisabled", err)
	}
	if _, _, _, err := s.Complete(context.Background(), ATProtoState{}, "c", "i", "ua"); !errors.Is(err, ErrATProtoDisabled) {
		t.Fatalf("Complete disabled err = %v, want ErrATProtoDisabled", err)
	}
	if s.Enabled() {
		t.Error("Enabled() should be false")
	}
}

func TestATProtoEmptyHandleRejected(t *testing.T) {
	repo := newOAuthFakeRepo()
	s := newATProtoTestService(repo, defaultFlow(), true)
	if _, _, err := s.Begin(context.Background(), "   ", ""); !errors.Is(err, ErrATProtoInvalidHandle) {
		t.Fatalf("empty handle err = %v, want ErrATProtoInvalidHandle", err)
	}
}

func TestATProtoClientIdentityProdAndDev(t *testing.T) {
	prod := NewATProtoOAuthService(nil, nil, nil, WithATProtoPublicBaseURL("https://vidra.test/"))
	if got := prod.ClientID(); got != "https://vidra.test/api/v1/auth/atproto/client-metadata.json" {
		t.Errorf("prod ClientID = %q", got)
	}
	if got := prod.RedirectURI(); got != "https://vidra.test/api/v1/auth/atproto/callback" {
		t.Errorf("prod RedirectURI = %q", got)
	}
	meta := prod.ClientMetadata()
	if meta.TokenEndpointAuthMethod != "none" || !meta.DPoPBoundAccessTokens || meta.Scope != "atproto" {
		t.Errorf("client metadata = %+v", meta)
	}

	dev := NewATProtoOAuthService(nil, nil, nil, WithATProtoPublicBaseURL("http://localhost:8080"))
	if got := dev.RedirectURI(); got != "http://127.0.0.1:8080/api/v1/auth/atproto/callback" {
		t.Errorf("dev RedirectURI = %q, want the 127.0.0.1 rewrite", got)
	}
	cid := dev.ClientID()
	if !strings.HasPrefix(cid, "http://localhost?") || !strings.Contains(cid, "scope=atproto") {
		t.Errorf("dev ClientID = %q", cid)
	}
	// The dev client_id embeds the exact 127.0.0.1 redirect_uri.
	u, _ := url.Parse(cid)
	if u.Query().Get("redirect_uri") != "http://127.0.0.1:8080/api/v1/auth/atproto/callback" {
		t.Errorf("dev client_id redirect_uri = %q", u.Query().Get("redirect_uri"))
	}
}

func TestSanitizeDIDForEmail(t *testing.T) {
	if got := sanitizeDIDForEmail("did:plc:abc123"); got != "did-plc-abc123" {
		t.Errorf("sanitizeDIDForEmail = %q", got)
	}
	// A very long DID collapses to a bounded hash-based local part.
	long := "did:web:" + strings.Repeat("a", 200)
	got := sanitizeDIDForEmail(long)
	if len(got) > 60 || !strings.HasPrefix(got, "did-") {
		t.Errorf("long DID local part = %q (len %d)", got, len(got))
	}
}
