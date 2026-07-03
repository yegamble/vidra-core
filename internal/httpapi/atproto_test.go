package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/atproto"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// atprotoFakeRepo is a minimal atproto.Repository for handler tests (only the
// account CRUD is exercised by the HTTP layer; the queue methods are stubs).
type atprotoFakeRepo struct {
	accounts map[uuid.UUID]sqlcgen.AtprotoAccount
}

func (r *atprotoFakeRepo) UpsertATProtoAccount(_ context.Context, a sqlcgen.UpsertATProtoAccountParams) (sqlcgen.AtprotoAccount, error) {
	acct := sqlcgen.AtprotoAccount{
		UserID: a.UserID, Handle: a.Handle, Did: a.Did, PdsUrl: a.PdsUrl,
		AppPasswordSealed: a.AppPasswordSealed, AutoPost: a.AutoPost, CreatedAt: time.Now(),
	}
	r.accounts[a.UserID] = acct
	return acct, nil
}

func (r *atprotoFakeRepo) GetATProtoAccount(_ context.Context, userID uuid.UUID) (sqlcgen.AtprotoAccount, error) {
	a, ok := r.accounts[userID]
	if !ok {
		return sqlcgen.AtprotoAccount{}, errors.New("not found")
	}
	return a, nil
}

func (r *atprotoFakeRepo) DeleteATProtoAccount(_ context.Context, userID uuid.UUID) error {
	delete(r.accounts, userID)
	return nil
}

func (r *atprotoFakeRepo) TouchATProtoAccountPosted(context.Context, uuid.UUID) error { return nil }
func (r *atprotoFakeRepo) EnqueueATProtoPost(context.Context, uuid.UUID) error        { return nil }
func (r *atprotoFakeRepo) ClaimDueATProtoPosts(context.Context, int32) ([]sqlcgen.ClaimDueATProtoPostsRow, error) {
	return nil, nil
}
func (r *atprotoFakeRepo) MarkATProtoPostDone(context.Context, sqlcgen.MarkATProtoPostDoneParams) error {
	return nil
}
func (r *atprotoFakeRepo) RescheduleATProtoPost(context.Context, sqlcgen.RescheduleATProtoPostParams) error {
	return nil
}
func (r *atprotoFakeRepo) FailATProtoPost(context.Context, sqlcgen.FailATProtoPostParams) error {
	return nil
}
func (r *atprotoFakeRepo) GetATProtoPostVideo(context.Context, uuid.UUID) (sqlcgen.GetATProtoPostVideoRow, error) {
	return sqlcgen.GetATProtoPostVideoRow{}, errors.New("none")
}

// fakePDSServer is an httptest server that answers com.atproto.server.createSession
// and records the submitted credentials for assertions.
type fakePDSServer struct {
	server         *httptest.Server
	gotIdentifier  string
	gotAppPassword string
	calls          int
}

func newFakePDSServer(t *testing.T) *fakePDSServer {
	t.Helper()
	f := &fakePDSServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, r *http.Request) {
		f.calls++
		body, _ := io.ReadAll(r.Body)
		var in struct {
			Identifier string `json:"identifier"`
			Password   string `json:"password"`
		}
		_ = json.Unmarshal(body, &in)
		f.gotIdentifier = in.Identifier
		f.gotAppPassword = in.Password
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"did":       "did:plc:webtest",
			"handle":    in.Identifier,
			"accessJwt": "jwt-token",
		})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// atprotoServer wires real auth + a real (enabled) atproto service over the
// in-memory fake repo. The atproto service uses the REAL HTTP PDS client with the
// SSRF guard relaxed so it can reach the loopback httptest fake PDS.
func atprotoServer(t *testing.T, enabled bool) (*Server, *atprotoFakeRepo) {
	t.Helper()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	repo := &atprotoFakeRepo{accounts: map[uuid.UUID]sqlcgen.AtprotoAccount{}}
	atsvc := atproto.NewService(repo,
		atproto.WithEnabled(enabled),
		atproto.WithBaseURL("https://videos.example"),
		atproto.WithAllowPrivateFetch(true), // reach the loopback fake PDS
	)
	return New(testConfig(), nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithATProtoService(atsvc),
	), repo
}

func putJSONAuth(srv *Server, path, body, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestATProtoLinkViaFakePDS(t *testing.T) {
	pds := newFakePDSServer(t)
	srv, repo := atprotoServer(t, true)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	body := `{"handle":"ada.bsky.social","app_password":"topsecret-app-pw","pds_url":"` + pds.server.URL + `","auto_post":true}`
	rec := putJSONAuth(srv, "/api/v1/me/atproto", body, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("link status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// The PDS createSession call was made with the exact submitted credentials.
	if pds.calls != 1 {
		t.Fatalf("createSession calls = %d, want 1", pds.calls)
	}
	if pds.gotIdentifier != "ada.bsky.social" {
		t.Errorf("createSession identifier = %q", pds.gotIdentifier)
	}
	if pds.gotAppPassword != "topsecret-app-pw" {
		t.Errorf("createSession app password not forwarded")
	}

	// The response never echoes the app password, and reports the resolved DID.
	if strings.Contains(rec.Body.String(), "topsecret-app-pw") {
		t.Fatalf("link response leaks the app password: %s", rec.Body.String())
	}
	var view struct {
		Handle   string `json:"handle"`
		DID      string `json:"did"`
		AutoPost bool   `json:"auto_post"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.DID != "did:plc:webtest" || !view.AutoPost {
		t.Errorf("view = %+v", view)
	}

	// The stored password is sealed-or-raw but the handler NEVER returns it; the
	// repo holds it, the API projection does not expose it.
	for _, a := range repo.accounts {
		if a.AppPasswordSealed == "" {
			t.Errorf("app password was not stored")
		}
	}
}

func TestATProtoStatusHidesSecretsAndUnlink(t *testing.T) {
	pds := newFakePDSServer(t)
	srv, _ := atprotoServer(t, true)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Nothing linked yet → 404.
	if rec := getWithAuth(srv, "/api/v1/me/atproto", tok); rec.Code != http.StatusNotFound {
		t.Fatalf("status before link = %d, want 404", rec.Code)
	}

	body := `{"handle":"ada.bsky.social","app_password":"hidden-pw","pds_url":"` + pds.server.URL + `"}`
	if rec := putJSONAuth(srv, "/api/v1/me/atproto", body, tok); rec.Code != http.StatusOK {
		t.Fatalf("link status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec := getWithAuth(srv, "/api/v1/me/atproto", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "hidden-pw") || strings.Contains(strings.ToLower(rec.Body.String()), "app_password") {
		t.Fatalf("status leaks the app password: %s", rec.Body.String())
	}

	// Unlink → 204, then status is 404 again.
	if rec := deleteJSONWithAuth(srv, "/api/v1/me/atproto", tok, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink status = %d, want 204", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/me/atproto", tok); rec.Code != http.StatusNotFound {
		t.Fatalf("status after unlink = %d, want 404", rec.Code)
	}
}

func TestATProtoDisabledReturns503(t *testing.T) {
	srv, _ := atprotoServer(t, false)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := putJSONAuth(srv, "/api/v1/me/atproto", `{"handle":"ada.bsky.social","app_password":"x"}`, tok)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("link when disabled = %d, want 503", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/me/atproto", tok); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status when disabled = %d, want 503", rec.Code)
	}
}

func TestATProtoLinkValidation(t *testing.T) {
	srv, _ := atprotoServer(t, true)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := putJSONAuth(srv, "/api/v1/me/atproto", `{"handle":"","app_password":""}`, tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty fields status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

func TestATProtoLinkRequiresAuth(t *testing.T) {
	srv, _ := atprotoServer(t, true)
	rec := putJSONAuth(srv, "/api/v1/me/atproto", `{"handle":"ada.bsky.social","app_password":"x"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d, want 401", rec.Code)
	}
}
