package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/port"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockOAuthRepoForAdmin struct {
	listClientsFunc        func(ctx context.Context) ([]*port.OAuthClient, error)
	createClientFunc       func(ctx context.Context, client *port.OAuthClient) error
	updateClientSecretFunc func(ctx context.Context, clientID string, secretHash *string, isConfidential bool) error
	deleteClientFunc       func(ctx context.Context, clientID string) error
}

func (m *mockOAuthRepoForAdmin) GetClientByClientID(ctx context.Context, clientID string) (*port.OAuthClient, error) {
	return nil, nil
}

func (m *mockOAuthRepoForAdmin) ListClients(ctx context.Context) ([]*port.OAuthClient, error) {
	if m.listClientsFunc != nil {
		return m.listClientsFunc(ctx)
	}
	return nil, nil
}

func (m *mockOAuthRepoForAdmin) CreateClient(ctx context.Context, client *port.OAuthClient) error {
	if m.createClientFunc != nil {
		return m.createClientFunc(ctx, client)
	}
	return nil
}

func (m *mockOAuthRepoForAdmin) UpdateClientSecret(ctx context.Context, clientID string, secretHash *string, isConfidential bool) error {
	if m.updateClientSecretFunc != nil {
		return m.updateClientSecretFunc(ctx, clientID, secretHash, isConfidential)
	}
	return nil
}

func (m *mockOAuthRepoForAdmin) DeleteClient(ctx context.Context, clientID string) error {
	if m.deleteClientFunc != nil {
		return m.deleteClientFunc(ctx, clientID)
	}
	return nil
}

func (m *mockOAuthRepoForAdmin) CreateAuthorizationCode(ctx context.Context, code *port.OAuthAuthorizationCode) error {
	return nil
}
func (m *mockOAuthRepoForAdmin) GetAuthorizationCode(ctx context.Context, code string) (*port.OAuthAuthorizationCode, error) {
	return nil, nil
}
func (m *mockOAuthRepoForAdmin) MarkCodeAsUsed(ctx context.Context, code string) error {
	return nil
}
func (m *mockOAuthRepoForAdmin) DeleteExpiredCodes(ctx context.Context) error {
	return nil
}
func (m *mockOAuthRepoForAdmin) CreateAccessToken(ctx context.Context, token *port.OAuthAccessToken) error {
	return nil
}
func (m *mockOAuthRepoForAdmin) GetAccessToken(ctx context.Context, tokenHash string) (*port.OAuthAccessToken, error) {
	return nil, nil
}
func (m *mockOAuthRepoForAdmin) RevokeAccessToken(ctx context.Context, tokenHash string) error {
	return nil
}
func (m *mockOAuthRepoForAdmin) ListUserTokens(ctx context.Context, userID string) ([]*port.OAuthAccessToken, error) {
	return nil, nil
}
func (m *mockOAuthRepoForAdmin) DeleteExpiredTokens(ctx context.Context) error {
	return nil
}

func TestAdminListOAuthClients_Success(t *testing.T) {
	secretHash := "hashed-secret"
	mockRepo := &mockOAuthRepoForAdmin{
		listClientsFunc: func(ctx context.Context) ([]*port.OAuthClient, error) {
			return []*port.OAuthClient{
				{
					ID:               "1",
					ClientID:         "client-1",
					Name:             "Test Client",
					GrantTypes:       []string{"password"},
					Scopes:           []string{"basic"},
					RedirectURIs:     []string{"http://localhost"},
					IsConfidential:   true,
					ClientSecretHash: &secretHash,
				},
			}, nil
		},
	}

	handler := &AuthHandlers{oauthRepo: mockRepo}
	req := httptest.NewRequest(http.MethodGet, "/admin/oauth/clients", nil)
	rec := httptest.NewRecorder()

	handler.AdminListOAuthClients(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, true, response["success"])

	data, ok := response["data"].(map[string]interface{})
	require.True(t, ok)
	clientsData, ok := data["data"].([]interface{})
	require.True(t, ok)
	assert.Len(t, clientsData, 1)
}

func TestAdminListOAuthClients_NoRepo(t *testing.T) {
	handler := &AuthHandlers{oauthRepo: nil}
	req := httptest.NewRequest(http.MethodGet, "/admin/oauth/clients", nil)
	rec := httptest.NewRecorder()

	handler.AdminListOAuthClients(rec, req)

	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestAdminListOAuthClients_Error(t *testing.T) {
	mockRepo := &mockOAuthRepoForAdmin{
		listClientsFunc: func(ctx context.Context) ([]*port.OAuthClient, error) {
			return nil, assert.AnError
		},
	}

	handler := &AuthHandlers{oauthRepo: mockRepo}
	req := httptest.NewRequest(http.MethodGet, "/admin/oauth/clients", nil)
	rec := httptest.NewRecorder()

	handler.AdminListOAuthClients(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestAdminCreateOAuthClient_Success(t *testing.T) {
	isConf := true
	mockRepo := &mockOAuthRepoForAdmin{
		createClientFunc: func(ctx context.Context, client *port.OAuthClient) error {
			assert.Equal(t, "test-client", client.ClientID)
			assert.Equal(t, "Test Client", client.Name)
			assert.NotNil(t, client.ClientSecretHash)
			return nil
		},
	}

	handler := &AuthHandlers{oauthRepo: mockRepo}
	reqBody := map[string]interface{}{
		"client_id":       "test-client",
		"client_secret":   "secret123",
		"name":            "Test Client",
		"is_confidential": &isConf,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.AdminCreateOAuthClient(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestAdminCreateOAuthClient_NoRepo(t *testing.T) {
	handler := &AuthHandlers{oauthRepo: nil}
	reqBody := map[string]interface{}{"client_id": "test", "name": "Test"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.AdminCreateOAuthClient(rec, req)

	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestAdminCreateOAuthClient_InvalidJSON(t *testing.T) {
	mockRepo := &mockOAuthRepoForAdmin{}
	handler := &AuthHandlers{oauthRepo: mockRepo}
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", bytes.NewReader([]byte("invalid-json")))
	rec := httptest.NewRecorder()

	handler.AdminCreateOAuthClient(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminCreateOAuthClient_MissingClientID(t *testing.T) {
	mockRepo := &mockOAuthRepoForAdmin{}
	handler := &AuthHandlers{oauthRepo: mockRepo}
	reqBody := map[string]string{"name": "Test"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.AdminCreateOAuthClient(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminCreateOAuthClient_MissingName(t *testing.T) {
	mockRepo := &mockOAuthRepoForAdmin{}
	handler := &AuthHandlers{oauthRepo: mockRepo}
	reqBody := map[string]string{"client_id": "test"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.AdminCreateOAuthClient(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminCreateOAuthClient_MissingSecretForConfidential(t *testing.T) {
	isConf := true
	mockRepo := &mockOAuthRepoForAdmin{}
	handler := &AuthHandlers{oauthRepo: mockRepo}
	reqBody := map[string]interface{}{
		"client_id":       "test",
		"name":            "Test",
		"is_confidential": &isConf,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.AdminCreateOAuthClient(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminCreateOAuthClient_RepoError(t *testing.T) {
	isConf := true
	mockRepo := &mockOAuthRepoForAdmin{
		createClientFunc: func(ctx context.Context, client *port.OAuthClient) error {
			return assert.AnError
		},
	}
	handler := &AuthHandlers{oauthRepo: mockRepo}
	reqBody := map[string]interface{}{
		"client_id":       "test",
		"client_secret":   "secret",
		"name":            "Test",
		"is_confidential": &isConf,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.AdminCreateOAuthClient(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminRotateOAuthClientSecret_Success(t *testing.T) {
	isConf := true
	mockRepo := &mockOAuthRepoForAdmin{
		updateClientSecretFunc: func(ctx context.Context, clientID string, secretHash *string, isConfidential bool) error {
			assert.Equal(t, "client-123", clientID)
			assert.NotNil(t, secretHash)
			assert.True(t, isConfidential)
			return nil
		},
	}

	handler := &AuthHandlers{oauthRepo: mockRepo}
	reqBody := map[string]interface{}{
		"client_secret":   "new-secret",
		"is_confidential": &isConf,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPut, "/admin/oauth/clients/client-123/secret", bytes.NewReader(body))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "client-123")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.AdminRotateOAuthClientSecret(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAdminRotateOAuthClientSecret_NoRepo(t *testing.T) {
	handler := &AuthHandlers{oauthRepo: nil}
	reqBody := map[string]string{"client_secret": "new-secret"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPut, "/admin/oauth/clients/client-123/secret", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.AdminRotateOAuthClientSecret(rec, req)

	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestAdminRotateOAuthClientSecret_MissingClientID(t *testing.T) {
	mockRepo := &mockOAuthRepoForAdmin{}
	handler := &AuthHandlers{oauthRepo: mockRepo}
	reqBody := map[string]string{"client_secret": "new-secret"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPut, "/admin/oauth/clients//secret", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.AdminRotateOAuthClientSecret(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminRotateOAuthClientSecret_InvalidJSON(t *testing.T) {
	mockRepo := &mockOAuthRepoForAdmin{}
	handler := &AuthHandlers{oauthRepo: mockRepo}
	req := httptest.NewRequest(http.MethodPut, "/admin/oauth/clients/client-123/secret", bytes.NewReader([]byte("invalid")))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "client-123")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.AdminRotateOAuthClientSecret(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminRotateOAuthClientSecret_MissingSecretForConfidential(t *testing.T) {
	isConf := true
	mockRepo := &mockOAuthRepoForAdmin{}
	handler := &AuthHandlers{oauthRepo: mockRepo}
	reqBody := map[string]interface{}{"is_confidential": &isConf}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPut, "/admin/oauth/clients/client-123/secret", bytes.NewReader(body))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "client-123")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.AdminRotateOAuthClientSecret(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminRotateOAuthClientSecret_RepoError(t *testing.T) {
	isConf := true
	mockRepo := &mockOAuthRepoForAdmin{
		updateClientSecretFunc: func(ctx context.Context, clientID string, secretHash *string, isConfidential bool) error {
			return assert.AnError
		},
	}
	handler := &AuthHandlers{oauthRepo: mockRepo}
	reqBody := map[string]interface{}{
		"client_secret":   "new-secret",
		"is_confidential": &isConf,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPut, "/admin/oauth/clients/client-123/secret", bytes.NewReader(body))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "client-123")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.AdminRotateOAuthClientSecret(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminDeleteOAuthClient_Success(t *testing.T) {
	mockRepo := &mockOAuthRepoForAdmin{
		deleteClientFunc: func(ctx context.Context, clientID string) error {
			assert.Equal(t, "client-123", clientID)
			return nil
		},
	}

	handler := &AuthHandlers{oauthRepo: mockRepo}
	req := httptest.NewRequest(http.MethodDelete, "/admin/oauth/clients/client-123", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "client-123")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.AdminDeleteOAuthClient(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAdminDeleteOAuthClient_NoRepo(t *testing.T) {
	handler := &AuthHandlers{oauthRepo: nil}
	req := httptest.NewRequest(http.MethodDelete, "/admin/oauth/clients/client-123", nil)
	rec := httptest.NewRecorder()

	handler.AdminDeleteOAuthClient(rec, req)

	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestAdminDeleteOAuthClient_MissingClientID(t *testing.T) {
	mockRepo := &mockOAuthRepoForAdmin{}
	handler := &AuthHandlers{oauthRepo: mockRepo}
	req := httptest.NewRequest(http.MethodDelete, "/admin/oauth/clients/", nil)
	rec := httptest.NewRecorder()

	handler.AdminDeleteOAuthClient(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminDeleteOAuthClient_RepoError(t *testing.T) {
	mockRepo := &mockOAuthRepoForAdmin{
		deleteClientFunc: func(ctx context.Context, clientID string) error {
			return assert.AnError
		},
	}
	handler := &AuthHandlers{oauthRepo: mockRepo}
	req := httptest.NewRequest(http.MethodDelete, "/admin/oauth/clients/client-123", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "client-123")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.AdminDeleteOAuthClient(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
