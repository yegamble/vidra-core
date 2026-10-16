package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/oidcproviders"
)

const oidcProvidersPath = "/api/v1/admin/oidc-providers"

// fakeOIDCProviders is the provider service as this layer sees it: the tests
// are about authz, step-up, typed errors, audit rows and what a response holds.
type fakeOIDCProviders struct {
	rows    []oidcproviders.Provider
	err     error
	created []oidcproviders.Input
	deleted []string
	existed bool
	keyless bool
}

func (f *fakeOIDCProviders) List(context.Context) ([]oidcproviders.Provider, error) {
	return f.rows, nil
}

func (f *fakeOIDCProviders) Create(_ context.Context, name string, in oidcproviders.Input, _ uuid.UUID) (oidcproviders.Provider, error) {
	f.created = append(f.created, in)
	if f.err != nil {
		return oidcproviders.Provider{}, f.err
	}
	return oidcproviders.Provider{Name: name, IssuerURL: in.IssuerURL, SecretConfigured: true, SecretStatus: "ok"}, nil
}

func (f *fakeOIDCProviders) Update(_ context.Context, name string, _ oidcproviders.Input, _ uuid.UUID) (oidcproviders.Provider, oidcproviders.Change, error) {
	if f.err != nil {
		return oidcproviders.Provider{}, oidcproviders.Change{}, f.err
	}
	return oidcproviders.Provider{Name: name}, oidcproviders.Change{ChangedFields: []string{"client_id", "enabled"}, SecretChanged: true}, nil
}

func (f *fakeOIDCProviders) Delete(_ context.Context, name string) (bool, error) {
	f.deleted = append(f.deleted, name)
	return f.existed, nil
}

func (f *fakeOIDCProviders) SecretsAvailable() bool { return !f.keyless }

func oidcProvidersServer(t *testing.T, buf *bytes.Buffer, svc *fakeOIDCProviders) (*Server, *authFakeRepo) {
	t.Helper()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(repo, issuer, 720*time.Hour)
	cfg := testConfig()
	cfg.OAuthProviders = []config.OAuthProviderConfig{
		{Name: "corp", IssuerURL: "https://env.example", ClientSecret: "env-secret-value"},
		{Name: "google", IssuerURL: "https://accounts.google.com"},
	}
	return New(cfg, nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithOIDCProviderService(svc),
		WithLogger(slog.New(slog.NewJSONHandler(buf, nil))),
	), repo
}

const oidcDeleteProof = `{"current_password":"supersecret"}`

const oidcCreateBody = `{"name":"corp","issuer_url":"https://sso.example","client_id":"vidra","client_secret":"top-secret-value"}`

func TestOIDCProvidersAdminOnly(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeOIDCProviders{}
	srv, repo := oidcProvidersServer(t, &buf, svc)
	registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	mod := registerTokens(t, srv, `{"username":"moses","email":"moses@example.test","password":"supersecret"}`)
	setRole(t, repo, "moses@example.test", "moderator")

	for _, v := range []struct{ method, path, body string }{
		{http.MethodGet, oidcProvidersPath, ""},
		{http.MethodPost, oidcProvidersPath, withCurrentPassword(oidcCreateBody)},
		{http.MethodPut, oidcProvidersPath + "/corp", withCurrentPassword(`{"client_id":"x"}`)},
		{http.MethodDelete, oidcProvidersPath + "/keycloak", oidcDeleteProof},
	} {
		if rec := doJSON(srv, v.method, v.path, "", v.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s = %d, want 401", v.method, rec.Code)
		}
		if rec := doJSON(srv, v.method, v.path, mod.Token, v.body); rec.Code != http.StatusForbidden {
			t.Errorf("moderator %s = %d, want 403", v.method, rec.Code)
		}
	}
	if len(svc.created) != 0 || len(svc.deleted) != 0 {
		t.Error("the service was reached despite the refusals")
	}
}

func TestOIDCProvidersCreateNeedsFreshCredentialAndNeverEchoesSecret(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeOIDCProviders{}
	srv, _ := oidcProvidersServer(t, &buf, svc)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := doJSON(srv, http.MethodPost, oidcProvidersPath, tok, oidcCreateBody)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "step_up_required" {
		t.Fatalf("no proof: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(srv, http.MethodPost, oidcProvidersPath, tok, withCurrentPassword(oidcCreateBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if len(svc.created) != 1 || svc.created[0].ClientSecret == nil || *svc.created[0].ClientSecret != "top-secret-value" {
		t.Fatalf("service input = %+v", svc.created)
	}
	if strings.Contains(rec.Body.String(), "top-secret-value") || strings.Contains(buf.String(), "top-secret-value") {
		t.Error("the client secret appeared in a response or a log line")
	}
	ev := findAudit(auditEvents(t, &buf), observability.ActionAdminOIDCProviderCreate, observability.ResultSuccess)
	if ev == nil {
		t.Fatal("create was not audited")
	}

	if rec := doJSON(srv, http.MethodPost, oidcProvidersPath, tok, withCurrentPassword(`{"name":"x","bogus":1}`)); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field = %d, want 400", rec.Code)
	}
}

func TestOIDCProvidersTypedErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{oidcproviders.ErrSecretsKeyMissing, http.StatusConflict, "oidc_secrets_key_missing"},
		{oidcproviders.ErrExists, http.StatusConflict, ""},
		{oidcproviders.ErrNotFound, http.StatusNotFound, ""},
		{&oidcproviders.ValidationError{Fields: []oidcproviders.FieldError{{Field: "issuer_url", Msg: "must use https"}}}, http.StatusUnprocessableEntity, ""},
	} {
		var buf bytes.Buffer
		srv, _ := oidcProvidersServer(t, &buf, &fakeOIDCProviders{err: tc.err})
		tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
		rec := doJSON(srv, http.MethodPut, oidcProvidersPath+"/corp", tok, withCurrentPassword(`{"client_id":"x"}`))
		if rec.Code != tc.status {
			t.Errorf("%v: status = %d, want %d; body=%s", tc.err, rec.Code, tc.status, rec.Body.String())
		}
		if tc.code != "" && errorCode(t, rec) != tc.code {
			t.Errorf("%v: code = %q, want %q", tc.err, errorCode(t, rec), tc.code)
		}
		if findAudit(auditEvents(t, &buf), observability.ActionAdminOIDCProviderUpdate, observability.ResultFailure) == nil {
			t.Errorf("%v: refused update not audited", tc.err)
		}
	}
}

func TestOIDCProvidersListAndDelete(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeOIDCProviders{rows: []oidcproviders.Provider{{Name: "corp", SecretConfigured: true}}, existed: true, keyless: true}
	srv, _ := oidcProvidersServer(t, &buf, svc)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := doJSON(srv, http.MethodGet, oidcProvidersPath, tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	var body struct {
		Providers        []oidcproviders.Provider `json:"providers"`
		SecretsAvailable bool                     `json:"secrets_available"`
		Environment      []struct {
			Name       string `json:"name"`
			IssuerURL  string `json:"issuer_url"`
			Overridden bool   `json:"overridden"`
		} `json:"environment"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Providers) != 1 || body.SecretsAvailable || len(body.Environment) != 2 ||
		!body.Environment[0].Overridden || body.Environment[1].Overridden {
		t.Errorf("list body = %+v", body)
	}
	if strings.Contains(rec.Body.String(), "env-secret-value") {
		t.Error("an environment client secret appeared in the list")
	}

	// A row that overrides an environment provider is disabled or edited, not
	// deleted: deleting it would silently restore the environment settings.
	if rec := doJSON(srv, http.MethodDelete, oidcProvidersPath+"/corp", tok, oidcDeleteProof); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("delete of an environment override = %d, want 422", rec.Code)
	}
	if rec := doJSON(srv, http.MethodDelete, oidcProvidersPath+"/keycloak", tok, ""); rec.Code != http.StatusForbidden || errorCode(t, rec) != "step_up_required" {
		t.Errorf("delete without a fresh credential = %d, want 403 step_up_required", rec.Code)
	}
	if len(svc.deleted) != 0 {
		t.Fatalf("refused deletes reached the service: %v", svc.deleted)
	}
	if rec := doJSON(srv, http.MethodDelete, oidcProvidersPath+"/keycloak", tok, oidcDeleteProof); rec.Code != http.StatusNoContent {
		t.Errorf("delete = %d, want 204", rec.Code)
	}
	if findAudit(auditEvents(t, &buf), observability.ActionAdminOIDCProviderDelete, observability.ResultSuccess) == nil {
		t.Error("delete was not audited")
	}
	svc.existed = false
	if rec := doJSON(srv, http.MethodDelete, oidcProvidersPath+"/nope", tok, oidcDeleteProof); rec.Code != http.StatusNotFound {
		t.Errorf("delete missing = %d, want 404", rec.Code)
	}
}
