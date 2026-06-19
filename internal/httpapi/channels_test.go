package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// channelFakeRepo is an in-memory channel.Repository for handler tests.
type channelFakeRepo struct {
	byHandle map[string]sqlcgen.Channel
}

func (f *channelFakeRepo) CreateChannel(_ context.Context, a sqlcgen.CreateChannelParams) (sqlcgen.Channel, error) {
	key := strings.ToLower(a.Handle)
	if _, ok := f.byHandle[key]; ok {
		return sqlcgen.Channel{}, &pgconn.PgError{Code: "23505"}
	}
	ch := sqlcgen.Channel{
		ID: uuid.New(), OwnerID: a.OwnerID, Handle: a.Handle,
		DisplayName: a.DisplayName, Description: a.Description,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.byHandle[key] = ch
	return ch, nil
}

func (f *channelFakeRepo) GetChannelByHandle(_ context.Context, lowerHandle string) (sqlcgen.Channel, error) {
	ch, ok := f.byHandle[strings.ToLower(lowerHandle)]
	if !ok {
		return sqlcgen.Channel{}, errors.New("not found")
	}
	return ch, nil
}

func (f *channelFakeRepo) ListChannelsByOwner(_ context.Context, ownerID uuid.UUID) ([]sqlcgen.Channel, error) {
	var out []sqlcgen.Channel
	for _, ch := range f.byHandle {
		if ch.OwnerID == ownerID {
			out = append(out, ch)
		}
	}
	return out, nil
}

func (f *channelFakeRepo) UpdateChannel(_ context.Context, a sqlcgen.UpdateChannelParams) (sqlcgen.Channel, error) {
	for k, ch := range f.byHandle {
		if ch.ID == a.ID {
			if a.DisplayName != nil {
				ch.DisplayName = *a.DisplayName
			}
			if a.Description != nil {
				ch.Description = *a.Description
			}
			ch.UpdatedAt = time.Now()
			f.byHandle[k] = ch
			return ch, nil
		}
	}
	return sqlcgen.Channel{}, errors.New("not found")
}

func (f *channelFakeRepo) DeleteChannel(_ context.Context, id uuid.UUID) error {
	for k, ch := range f.byHandle {
		if ch.ID == id {
			delete(f.byHandle, k)
			return nil
		}
	}
	return nil
}

// channelServer wires real auth + channel services over in-memory fakes.
func channelServer(t *testing.T) *Server {
	t.Helper()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	chansvc := channel.NewService(&channelFakeRepo{byHandle: map[string]sqlcgen.Channel{}})
	return New(testConfig(), nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithChannelService(chansvc),
	)
}

func TestCreateChannelRequiresAuth(t *testing.T) {
	srv := channelServer(t)
	rec := postTo(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestCreateChannelValidation(t *testing.T) {
	srv := channelServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"a","display_name":""}`, token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if len(er.Error.Fields) == 0 {
		t.Errorf("expected field errors, got %+v", er.Error)
	}
}

func TestCreateChannelAndListAndGet(t *testing.T) {
	srv := channelServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Create
	rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes","description":"things"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created channelView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Handle != "ada_makes" || created.OwnerID == "" {
		t.Errorf("unexpected channel: %+v", created)
	}

	// List own
	list := getWithAuth(srv, "/api/v1/me/channels", token)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", list.Code)
	}
	var body channelListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Channels) != 1 || body.Channels[0].Handle != "ada_makes" {
		t.Errorf("unexpected list: %+v", body.Channels)
	}

	// Public get by handle (case-insensitive, no auth)
	get := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/ADA_MAKES", nil)
	srv.Handler().ServeHTTP(get, req)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", get.Code, get.Body.String())
	}
	var fetched channelView
	if err := json.Unmarshal(get.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fetched.ID != created.ID {
		t.Errorf("get returned %q, want %q", fetched.ID, created.ID)
	}
}

func TestCreateChannelDuplicateConflict(t *testing.T) {
	srv := channelServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	const body = `{"handle":"ada_makes","display_name":"Ada Makes"}`
	_ = postJSONAuth(srv, "/api/v1/channels", body, token)
	rec := postJSONAuth(srv, "/api/v1/channels", body, token)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestGetChannelNotFound(t *testing.T) {
	srv := channelServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/ghost", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error.Code != "not_found" {
		t.Errorf("code = %q, want not_found", er.Error.Code)
	}
}

func sendJSONAuth(srv *Server, method, path, body, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestUpdateChannelOwnerAndNonOwner(t *testing.T) {
	srv := channelServer(t)
	ownerTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	_ = postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes","description":"old"}`, ownerTok)

	// Owner partial update succeeds.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/channels/ada_makes", `{"description":"new"}`, ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner update = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var ch channelView
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)
	if ch.Description != "new" || ch.DisplayName != "Ada Makes" {
		t.Errorf("unexpected channel after update: %+v", ch)
	}

	// Non-owner is forbidden.
	bad := sendJSONAuth(srv, http.MethodPatch, "/api/v1/channels/ada_makes", `{"description":"hax"}`, otherTok)
	if bad.Code != http.StatusForbidden {
		t.Fatalf("non-owner update = %d, want 403", bad.Code)
	}

	// Unauthenticated is 401.
	if anon := sendJSONAuth(srv, http.MethodPatch, "/api/v1/channels/ada_makes", `{"description":"x"}`, ""); anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon update = %d, want 401", anon.Code)
	}
}

func TestUpdateChannelValidation(t *testing.T) {
	srv := channelServer(t)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	_ = postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes"}`, tok)
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/channels/ada_makes", `{}`, tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty patch = %d, want 422", rec.Code)
	}
}

func TestDeleteChannelOwnerAndNonOwner(t *testing.T) {
	srv := channelServer(t)
	ownerTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	_ = postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes"}`, ownerTok)

	// Non-owner cannot delete.
	if bad := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/ada_makes", "", otherTok); bad.Code != http.StatusForbidden {
		t.Fatalf("non-owner delete = %d, want 403", bad.Code)
	}

	// Owner deletes; then it is gone (public get 404).
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/ada_makes", "", ownerTok); rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete = %d, want 204", rec.Code)
	}
	get := httptest.NewRecorder()
	srv.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/channels/ada_makes", nil))
	if get.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", get.Code)
	}
}

// postJSONAuth posts a JSON body with a bearer token.
func postJSONAuth(srv *Server, path, body, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}
