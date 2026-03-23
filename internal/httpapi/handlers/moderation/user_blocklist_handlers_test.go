package moderation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/handlers/moderation"
	"vidra-core/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// --- minimal mock ---

type mockUserBlockRepo struct {
	accountBlocks []*domain.UserBlock
	serverBlocks  []*domain.UserBlock
	blockErr      error
	unblockErr    error
}

func (m *mockUserBlockRepo) BlockAccount(ctx context.Context, userID, targetAccountID uuid.UUID) (*domain.UserBlock, error) {
	if m.blockErr != nil {
		return nil, m.blockErr
	}
	b := &domain.UserBlock{ID: uuid.New(), UserID: userID, BlockType: domain.BlockTypeAccount, TargetAccountID: &targetAccountID}
	return b, nil
}
func (m *mockUserBlockRepo) UnblockAccount(ctx context.Context, userID uuid.UUID, targetAccountName string) error {
	return m.unblockErr
}
func (m *mockUserBlockRepo) ListAccountBlocks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.UserBlock, int64, error) {
	return m.accountBlocks, int64(len(m.accountBlocks)), nil
}
func (m *mockUserBlockRepo) BlockServer(ctx context.Context, userID uuid.UUID, host string) (*domain.UserBlock, error) {
	if m.blockErr != nil {
		return nil, m.blockErr
	}
	b := &domain.UserBlock{ID: uuid.New(), UserID: userID, BlockType: domain.BlockTypeServer, TargetServerHost: &host}
	return b, nil
}
func (m *mockUserBlockRepo) UnblockServer(ctx context.Context, userID uuid.UUID, host string) error {
	return m.unblockErr
}
func (m *mockUserBlockRepo) ListServerBlocks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.UserBlock, int64, error) {
	return m.serverBlocks, int64(len(m.serverBlocks)), nil
}

// --- helpers ---

func newBlocklistHandlers(repo moderation.UserBlockRepository) *moderation.UserBlocklistHandlers {
	return moderation.NewUserBlocklistHandlers(repo)
}

func reqWithUser(method, path string, body []byte) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, uuid.New().String())
	return req.WithContext(ctx)
}

// --- tests ---

func TestListAccountBlocks_ReturnsEmpty(t *testing.T) {
	h := newBlocklistHandlers(&mockUserBlockRepo{})
	req := reqWithUser(http.MethodGet, "/api/v1/blocklist/accounts", nil)
	w := httptest.NewRecorder()
	h.ListAccountBlocks(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestBlockAccount_Created(t *testing.T) {
	h := newBlocklistHandlers(&mockUserBlockRepo{})
	body, _ := json.Marshal(map[string]string{"accountId": uuid.New().String()})
	req := reqWithUser(http.MethodPost, "/api/v1/blocklist/accounts", body)
	w := httptest.NewRecorder()
	h.BlockAccount(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestUnblockAccount_NoContent(t *testing.T) {
	h := newBlocklistHandlers(&mockUserBlockRepo{})
	req := reqWithUser(http.MethodDelete, "/api/v1/blocklist/accounts/someone", nil)
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("accountName", "someone")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.UnblockAccount(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestListServerBlocks_ReturnsEmpty(t *testing.T) {
	h := newBlocklistHandlers(&mockUserBlockRepo{})
	req := reqWithUser(http.MethodGet, "/api/v1/blocklist/servers", nil)
	w := httptest.NewRecorder()
	h.ListServerBlocks(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestBlockServer_Created(t *testing.T) {
	h := newBlocklistHandlers(&mockUserBlockRepo{})
	body, _ := json.Marshal(map[string]string{"host": "evil.example.com"})
	req := reqWithUser(http.MethodPost, "/api/v1/blocklist/servers", body)
	w := httptest.NewRecorder()
	h.BlockServer(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestUnblockServer_NoContent(t *testing.T) {
	h := newBlocklistHandlers(&mockUserBlockRepo{})
	req := reqWithUser(http.MethodDelete, "/api/v1/blocklist/servers/evil.example.com", nil)
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("host", "evil.example.com")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.UnblockServer(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}
