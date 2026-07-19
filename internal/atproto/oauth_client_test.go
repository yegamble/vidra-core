package atproto

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAuthServer is an httptest ATProto OAuth auth server: it serves the two
// discovery documents, a PAR endpoint that runs the use_dpop_nonce dance, and a
// token endpoint. Handlers are overridable per-test to exercise failure modes.
type fakeAuthServer struct {
	srv *httptest.Server

	// metadataOverride, when set, replaces the authorization-server metadata body.
	metadataOverride map[string]any
	// tokenBody, when set, replaces the token-endpoint response body.
	tokenBody map[string]any

	parCalls   int
	parProofs  []string // captured DPoP header per PAR call
	tokenProof string   // captured DPoP header on the token call
}

func newFakeAuthServer(t *testing.T) *fakeAuthServer {
	t.Helper()
	f := &fakeAuthServer{}
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"authorization_servers": []string{f.srv.URL}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		if f.metadataOverride != nil {
			writeJSON(w, f.metadataOverride)
			return
		}
		writeJSON(w, f.defaultMetadata())
	})
	mux.HandleFunc("/par", func(w http.ResponseWriter, r *http.Request) {
		f.parCalls++
		f.parProofs = append(f.parProofs, r.Header.Get("DPoP"))
		// First call carries no nonce → demand one (the dance). Second succeeds.
		if f.parCalls == 1 {
			w.Header().Set("DPoP-Nonce", "par-nonce-1")
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "use_dpop_nonce"})
			return
		}
		w.Header().Set("DPoP-Nonce", "token-nonce-1")
		writeJSON(w, map[string]any{"request_uri": "urn:ietf:params:oauth:request_uri:abc", "expires_in": 60})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		f.tokenProof = r.Header.Get("DPoP")
		if f.tokenBody != nil {
			writeJSON(w, f.tokenBody)
			return
		}
		writeJSON(w, map[string]any{
			"access_token": "at", "token_type": "DPoP", "expires_in": 3600,
			"sub": "did:plc:subject", "scope": "atproto",
		})
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAuthServer) defaultMetadata() map[string]any {
	return map[string]any{
		"issuer":                                         f.srv.URL,
		"pushed_authorization_request_endpoint":          f.srv.URL + "/par",
		"authorization_endpoint":                         f.srv.URL + "/authorize",
		"token_endpoint":                                 f.srv.URL + "/token",
		"scopes_supported":                               []string{"atproto", "transition:generic"},
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"dpop_signing_alg_values_supported":              []string{"ES256"},
		"authorization_response_iss_parameter_supported": true,
		"require_pushed_authorization_requests":          true,
		"client_id_metadata_document_supported":          true,
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func testLoginClient() *OAuthClient { return NewOAuthClient(WithOAuthClientAllowPrivate(true)) }

func TestDiscoverAuthServerHappyPath(t *testing.T) {
	f := newFakeAuthServer(t)
	meta, err := testLoginClient().DiscoverAuthServer(context.Background(), f.srv.URL)
	if err != nil {
		t.Fatalf("DiscoverAuthServer: %v", err)
	}
	if meta.Issuer != f.srv.URL || meta.PAREndpoint != f.srv.URL+"/par" || meta.TokenEndpoint != f.srv.URL+"/token" {
		t.Errorf("meta = %+v", meta)
	}
}

func TestDiscoverAuthServerValidationFailures(t *testing.T) {
	tests := map[string]func(m map[string]any){
		"issuer mismatch":             func(m map[string]any) { m["issuer"] = "https://evil.example" },
		"missing atproto scope":       func(m map[string]any) { m["scopes_supported"] = []string{"transition:generic"} },
		"no ES256":                    func(m map[string]any) { m["dpop_signing_alg_values_supported"] = []string{"RS256"} },
		"no S256":                     func(m map[string]any) { m["code_challenge_methods_supported"] = []string{"plain"} },
		"iss param not supported":     func(m map[string]any) { m["authorization_response_iss_parameter_supported"] = false },
		"PAR not required":            func(m map[string]any) { m["require_pushed_authorization_requests"] = false },
		"no client-id metadata doc":   func(m map[string]any) { m["client_id_metadata_document_supported"] = false },
		"missing token endpoint":      func(m map[string]any) { delete(m, "token_endpoint") },
		"missing par endpoint":        func(m map[string]any) { delete(m, "pushed_authorization_request_endpoint") },
		"no authorization_code grant": func(m map[string]any) { m["grant_types_supported"] = []string{"refresh_token"} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFakeAuthServer(t)
			f.metadataOverride = f.defaultMetadata()
			mutate(f.metadataOverride)
			if _, err := testLoginClient().DiscoverAuthServer(context.Background(), f.srv.URL); !errors.Is(err, ErrBadAuthServerMeta) {
				t.Fatalf("err = %v, want ErrBadAuthServerMeta", err)
			}
		})
	}
}

func TestDiscoverAuthServerRejectsMultipleAuthServers(t *testing.T) {
	f := newFakeAuthServer(t)
	// Override the protected-resource handler to return two auth servers.
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"authorization_servers": []string{f.srv.URL, "https://other.example"}})
	})
	alt := httptest.NewServer(mux)
	defer alt.Close()
	if _, err := testLoginClient().DiscoverAuthServer(context.Background(), alt.URL); !errors.Is(err, ErrBadAuthServerMeta) {
		t.Fatalf("err = %v, want ErrBadAuthServerMeta (exactly one auth server)", err)
	}
}

func TestPushAuthRequestNonceDanceAndExchange(t *testing.T) {
	f := newFakeAuthServer(t)
	c := testLoginClient()
	meta, err := c.DiscoverAuthServer(context.Background(), f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	key, err := newDPoPKey()
	if err != nil {
		t.Fatal(err)
	}
	jwk, err := key.marshalPrivateJWK()
	if err != nil {
		t.Fatal(err)
	}

	par, err := c.PushAuthRequest(context.Background(), meta, PARInput{
		ClientID:    "https://vidra.test/client-metadata.json",
		RedirectURI: "https://vidra.test/callback",
		State:       "state-123",
		LoginHint:   "alice.example",
		Verifier:    "verifier-verifier-verifier-verifier-1234",
		DPoPKeyJWK:  jwk,
	})
	if err != nil {
		t.Fatalf("PushAuthRequest: %v", err)
	}
	// The nonce dance ran: two PAR calls, and the retry carried the server nonce.
	if f.parCalls != 2 {
		t.Fatalf("PAR calls = %d, want 2 (nonce retry)", f.parCalls)
	}
	assertProofNonce(t, f.parProofs[0], "", f.srv.URL+"/par", "POST")
	assertProofNonce(t, f.parProofs[1], "par-nonce-1", f.srv.URL+"/par", "POST")
	if par.RequestURI == "" {
		t.Error("PAR returned no request_uri")
	}
	// The PAR response nonce is carried forward to the token request.
	if par.DPoPNonce != "token-nonce-1" {
		t.Errorf("carried nonce = %q, want token-nonce-1", par.DPoPNonce)
	}

	res, err := c.ExchangeCode(context.Background(), meta, ExchangeInput{
		ClientID:    "https://vidra.test/client-metadata.json",
		RedirectURI: "https://vidra.test/callback",
		Code:        "auth-code",
		Verifier:    "verifier-verifier-verifier-verifier-1234",
		DPoPKeyJWK:  jwk,
		DPoPNonce:   par.DPoPNonce,
	})
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if res.DID != "did:plc:subject" || !scopeContains(res.Scope, "atproto") {
		t.Errorf("exchange result = %+v", res)
	}
	// The token DPoP proof was bound to the carried nonce and the token htu.
	assertProofNonce(t, f.tokenProof, "token-nonce-1", f.srv.URL+"/token", "POST")
}

func TestExchangeRejectsWrongScope(t *testing.T) {
	f := newFakeAuthServer(t)
	f.tokenBody = map[string]any{
		"access_token": "at", "token_type": "DPoP",
		"sub": "did:plc:subject", "scope": "transition:generic",
	}
	c := testLoginClient()
	meta, err := c.DiscoverAuthServer(context.Background(), f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := newDPoPKey()
	jwk, _ := key.marshalPrivateJWK()
	if _, err := c.ExchangeCode(context.Background(), meta, ExchangeInput{
		ClientID: "id", RedirectURI: "https://vidra.test/callback", Code: "c",
		Verifier: "v", DPoPKeyJWK: jwk,
	}); !errors.Is(err, ErrOAuthScope) {
		t.Fatalf("err = %v, want ErrOAuthScope", err)
	}
}

func TestSSRFGuardEngagedByDefault(t *testing.T) {
	// A loopback backend resolves fine under the dev/test knob...
	f := newFakeAuthServer(t)
	if _, err := testLoginClient().DiscoverAuthServer(context.Background(), f.srv.URL); err != nil {
		t.Fatalf("allowPrivate client: %v", err)
	}
	// ...but the DEFAULT (production) client refuses the non-https, private-IP hop:
	// the SSRF guard is engaged on every login fetch.
	if _, err := NewOAuthClient().DiscoverAuthServer(context.Background(), f.srv.URL); !errors.Is(err, ErrOAuthUpstream) {
		t.Fatalf("default client err = %v, want ErrOAuthUpstream (SSRF guard must block)", err)
	}
}

func TestLoginErrorsDoNotLeakUpstreamBody(t *testing.T) {
	const secret = "SUPER-SECRET-BEARER-TOKEN"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom","leak":"` + secret + `"}`))
	}))
	defer srv.Close()

	_, err := testLoginClient().DiscoverAuthServer(context.Background(), srv.URL)
	if !errors.Is(err, ErrOAuthUpstream) {
		t.Fatalf("err = %v, want ErrOAuthUpstream", err)
	}
	// The error carries only a safe {Op, Status} — never the upstream body.
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaks the upstream body: %q", err.Error())
	}
}

// assertProofNonce decodes a DPoP proof header and checks its nonce/htu/htm.
func assertProofNonce(t *testing.T, proof, wantNonce, wantHTU, wantHTM string) {
	t.Helper()
	if proof == "" {
		t.Fatal("request carried no DPoP header")
	}
	parts := strings.Split(proof, ".")
	if len(parts) != 3 {
		t.Fatalf("DPoP proof has %d segments, want 3", len(parts))
	}
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(cb, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if wantNonce == "" {
		if _, present := claims["nonce"]; present {
			t.Errorf("proof carried a nonce %v, want none", claims["nonce"])
		}
	} else if claims["nonce"] != wantNonce {
		t.Errorf("nonce = %v, want %q", claims["nonce"], wantNonce)
	}
	if claims["htm"] != wantHTM {
		t.Errorf("htm = %v, want %q", claims["htm"], wantHTM)
	}
	if wantNonce != "" && claims["htu"] != wantHTU {
		t.Errorf("htu = %v, want %q", claims["htu"], wantHTU)
	}
}
