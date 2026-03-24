package federation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vidra-core/internal/domain"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRedundancyService struct {
	ListInstancePeersFunc     func(ctx context.Context, limit, offset int, activeOnly bool) ([]*domain.InstancePeer, error)
	RegisterInstancePeerFunc  func(ctx context.Context, peer *domain.InstancePeer) error
	GetInstancePeerFunc       func(ctx context.Context, id string) (*domain.InstancePeer, error)
	UpdateInstancePeerFunc    func(ctx context.Context, peer *domain.InstancePeer) error
	DeleteInstancePeerFunc    func(ctx context.Context, id string) error
	CreateRedundancyFunc      func(ctx context.Context, videoID, instanceID string, strategy domain.RedundancyStrategy, priority int) (*domain.VideoRedundancy, error)
	GetRedundancyFunc         func(ctx context.Context, id string) (*domain.VideoRedundancy, error)
	ListVideoRedundanciesFunc func(ctx context.Context, videoID string) ([]*domain.VideoRedundancy, error)
	CancelRedundancyFunc      func(ctx context.Context, id string) error
	DeleteRedundancyFunc      func(ctx context.Context, id string) error
	SyncRedundancyFunc        func(ctx context.Context, redundancyID string) error
	ListPoliciesFunc          func(ctx context.Context, enabledOnly bool) ([]*domain.RedundancyPolicy, error)
	CreatePolicyFunc          func(ctx context.Context, policy *domain.RedundancyPolicy) error
	GetPolicyFunc             func(ctx context.Context, id string) (*domain.RedundancyPolicy, error)
	UpdatePolicyFunc          func(ctx context.Context, policy *domain.RedundancyPolicy) error
	DeletePolicyFunc          func(ctx context.Context, id string) error
	EvaluatePoliciesFunc      func(ctx context.Context) (int, error)
	GetStatsFunc              func(ctx context.Context) (map[string]interface{}, error)
	GetVideoHealthFunc        func(ctx context.Context, videoID string) (float64, error)
}

func (m *mockRedundancyService) ListInstancePeers(ctx context.Context, limit, offset int, activeOnly bool) ([]*domain.InstancePeer, error) {
	if m.ListInstancePeersFunc != nil {
		return m.ListInstancePeersFunc(ctx, limit, offset, activeOnly)
	}
	return nil, nil
}

func (m *mockRedundancyService) RegisterInstancePeer(ctx context.Context, peer *domain.InstancePeer) error {
	if m.RegisterInstancePeerFunc != nil {
		return m.RegisterInstancePeerFunc(ctx, peer)
	}
	return nil
}

func (m *mockRedundancyService) GetInstancePeer(ctx context.Context, id string) (*domain.InstancePeer, error) {
	if m.GetInstancePeerFunc != nil {
		return m.GetInstancePeerFunc(ctx, id)
	}
	return nil, nil
}

func (m *mockRedundancyService) UpdateInstancePeer(ctx context.Context, peer *domain.InstancePeer) error {
	if m.UpdateInstancePeerFunc != nil {
		return m.UpdateInstancePeerFunc(ctx, peer)
	}
	return nil
}

func (m *mockRedundancyService) DeleteInstancePeer(ctx context.Context, id string) error {
	if m.DeleteInstancePeerFunc != nil {
		return m.DeleteInstancePeerFunc(ctx, id)
	}
	return nil
}

func (m *mockRedundancyService) CreateRedundancy(ctx context.Context, videoID, instanceID string, strategy domain.RedundancyStrategy, priority int) (*domain.VideoRedundancy, error) {
	if m.CreateRedundancyFunc != nil {
		return m.CreateRedundancyFunc(ctx, videoID, instanceID, strategy, priority)
	}
	return nil, nil
}

func (m *mockRedundancyService) GetRedundancy(ctx context.Context, id string) (*domain.VideoRedundancy, error) {
	if m.GetRedundancyFunc != nil {
		return m.GetRedundancyFunc(ctx, id)
	}
	return nil, nil
}

func (m *mockRedundancyService) ListVideoRedundancies(ctx context.Context, videoID string) ([]*domain.VideoRedundancy, error) {
	if m.ListVideoRedundanciesFunc != nil {
		return m.ListVideoRedundanciesFunc(ctx, videoID)
	}
	return nil, nil
}

func (m *mockRedundancyService) CancelRedundancy(ctx context.Context, id string) error {
	if m.CancelRedundancyFunc != nil {
		return m.CancelRedundancyFunc(ctx, id)
	}
	return nil
}

func (m *mockRedundancyService) DeleteRedundancy(ctx context.Context, id string) error {
	if m.DeleteRedundancyFunc != nil {
		return m.DeleteRedundancyFunc(ctx, id)
	}
	return nil
}

func (m *mockRedundancyService) SyncRedundancy(ctx context.Context, redundancyID string) error {
	if m.SyncRedundancyFunc != nil {
		return m.SyncRedundancyFunc(ctx, redundancyID)
	}
	return nil
}

func (m *mockRedundancyService) ListPolicies(ctx context.Context, enabledOnly bool) ([]*domain.RedundancyPolicy, error) {
	if m.ListPoliciesFunc != nil {
		return m.ListPoliciesFunc(ctx, enabledOnly)
	}
	return nil, nil
}

func (m *mockRedundancyService) CreatePolicy(ctx context.Context, policy *domain.RedundancyPolicy) error {
	if m.CreatePolicyFunc != nil {
		return m.CreatePolicyFunc(ctx, policy)
	}
	return nil
}

func (m *mockRedundancyService) GetPolicy(ctx context.Context, id string) (*domain.RedundancyPolicy, error) {
	if m.GetPolicyFunc != nil {
		return m.GetPolicyFunc(ctx, id)
	}
	return nil, nil
}

func (m *mockRedundancyService) UpdatePolicy(ctx context.Context, policy *domain.RedundancyPolicy) error {
	if m.UpdatePolicyFunc != nil {
		return m.UpdatePolicyFunc(ctx, policy)
	}
	return nil
}

func (m *mockRedundancyService) DeletePolicy(ctx context.Context, id string) error {
	if m.DeletePolicyFunc != nil {
		return m.DeletePolicyFunc(ctx, id)
	}
	return nil
}

func (m *mockRedundancyService) EvaluatePolicies(ctx context.Context) (int, error) {
	if m.EvaluatePoliciesFunc != nil {
		return m.EvaluatePoliciesFunc(ctx)
	}
	return 0, nil
}

func (m *mockRedundancyService) GetStats(ctx context.Context) (map[string]interface{}, error) {
	if m.GetStatsFunc != nil {
		return m.GetStatsFunc(ctx)
	}
	return nil, nil
}

func (m *mockRedundancyService) GetVideoHealth(ctx context.Context, videoID string) (float64, error) {
	if m.GetVideoHealthFunc != nil {
		return m.GetVideoHealthFunc(ctx, videoID)
	}
	return 0, nil
}

func TestListInstancePeers_Success(t *testing.T) {
	mockService := &mockRedundancyService{
		ListInstancePeersFunc: func(ctx context.Context, limit, offset int, activeOnly bool) ([]*domain.InstancePeer, error) {
			return []*domain.InstancePeer{
				{ID: "peer1", InstanceURL: "https://peer1.example.com"},
				{ID: "peer2", InstanceURL: "https://peer2.example.com"},
			}, nil
		},
	}

	handler := NewRedundancyHandler(mockService, nil)
	req := httptest.NewRequest("GET", "/redundancy/instances?limit=50&offset=0", nil)
	rec := httptest.NewRecorder()

	handler.ListInstancePeers(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var wrapper map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &wrapper)
	require.NoError(t, err)

	response, ok := wrapper["data"].(map[string]interface{})
	require.True(t, ok)

	instances, ok := response["instances"].([]interface{})
	require.True(t, ok)
	assert.Len(t, instances, 2)
}

func TestListInstancePeers_ServiceError(t *testing.T) {
	mockService := &mockRedundancyService{
		ListInstancePeersFunc: func(ctx context.Context, limit, offset int, activeOnly bool) ([]*domain.InstancePeer, error) {
			return nil, errors.New("database error")
		},
	}

	handler := NewRedundancyHandler(mockService, nil)
	req := httptest.NewRequest("GET", "/redundancy/instances", nil)
	rec := httptest.NewRecorder()

	handler.ListInstancePeers(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRegisterInstancePeer_Success(t *testing.T) {
	var capturedPeer *domain.InstancePeer

	mockService := &mockRedundancyService{
		RegisterInstancePeerFunc: func(ctx context.Context, peer *domain.InstancePeer) error {
			capturedPeer = peer
			return nil
		},
	}

	handler := NewRedundancyHandler(mockService, nil)
	body := `{"instance_url": "https://peer.example.com", "auto_accept_redundancy": true, "max_redundancy_size_gb": 100}`
	req := httptest.NewRequest("POST", "/redundancy/instances", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.RegisterInstancePeer(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	require.NotNil(t, capturedPeer)
	assert.Equal(t, "https://peer.example.com", capturedPeer.InstanceURL)
	assert.True(t, capturedPeer.AutoAcceptRedundancy)
	assert.Equal(t, 100, capturedPeer.MaxRedundancySizeGB)
}

func TestRegisterInstancePeer_InvalidBody(t *testing.T) {
	mockService := &mockRedundancyService{}
	handler := NewRedundancyHandler(mockService, nil)

	tests := []struct {
		name string
		body string
	}{
		{"invalid JSON", `{invalid json}`},
		{"missing instance_url", `{"auto_accept_redundancy": true}`},
		{"empty instance_url", `{"instance_url": ""}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/redundancy/instances", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			handler.RegisterInstancePeer(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestRegisterInstancePeer_AlreadyExists(t *testing.T) {
	mockService := &mockRedundancyService{
		RegisterInstancePeerFunc: func(ctx context.Context, peer *domain.InstancePeer) error {
			return domain.ErrInstancePeerAlreadyExists
		},
	}

	handler := NewRedundancyHandler(mockService, nil)
	body := `{"instance_url": "https://peer.example.com"}`
	req := httptest.NewRequest("POST", "/redundancy/instances", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.RegisterInstancePeer(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestGetInstancePeer_Success(t *testing.T) {
	mockService := &mockRedundancyService{
		GetInstancePeerFunc: func(ctx context.Context, id string) (*domain.InstancePeer, error) {
			if id == "peer1" {
				return &domain.InstancePeer{
					ID:          "peer1",
					InstanceURL: "https://peer1.example.com",
				}, nil
			}
			return nil, domain.ErrInstancePeerNotFound
		},
	}

	handler := NewRedundancyHandler(mockService, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "peer1")

	req := httptest.NewRequest("GET", "/redundancy/instances/peer1", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	handler.GetInstancePeer(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var wrapper map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &wrapper)
	require.NoError(t, err)

	peerData, ok := wrapper["data"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "peer1", peerData["id"])
}

func TestGetInstancePeer_NotFound(t *testing.T) {
	mockService := &mockRedundancyService{
		GetInstancePeerFunc: func(ctx context.Context, id string) (*domain.InstancePeer, error) {
			return nil, domain.ErrInstancePeerNotFound
		},
	}

	handler := NewRedundancyHandler(mockService, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "nonexistent")

	req := httptest.NewRequest("GET", "/redundancy/instances/nonexistent", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	handler.GetInstancePeer(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeleteInstancePeer_Success(t *testing.T) {
	var capturedID string

	mockService := &mockRedundancyService{
		DeleteInstancePeerFunc: func(ctx context.Context, id string) error {
			capturedID = id
			return nil
		},
	}

	handler := NewRedundancyHandler(mockService, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "peer1")

	req := httptest.NewRequest("DELETE", "/redundancy/instances/peer1", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	handler.DeleteInstancePeer(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "peer1", capturedID)
}

func TestDeleteInstancePeer_NotFound(t *testing.T) {
	mockService := &mockRedundancyService{
		DeleteInstancePeerFunc: func(ctx context.Context, id string) error {
			return domain.ErrInstancePeerNotFound
		},
	}

	handler := NewRedundancyHandler(mockService, nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "nonexistent")

	req := httptest.NewRequest("DELETE", "/redundancy/instances/nonexistent", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	handler.DeleteInstancePeer(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetStats_Success(t *testing.T) {
	mockService := &mockRedundancyService{
		GetStatsFunc: func(ctx context.Context) (map[string]interface{}, error) {
			return map[string]interface{}{
				"total_redundancies": 100,
				"active_peers":       10,
			}, nil
		},
	}

	handler := NewRedundancyHandler(mockService, nil)
	req := httptest.NewRequest("GET", "/redundancy/stats", nil)
	rec := httptest.NewRecorder()

	handler.GetStats(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var wrapper map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &wrapper)
	require.NoError(t, err)

	stats, ok := wrapper["data"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(100), stats["total_redundancies"])
	assert.Equal(t, float64(10), stats["active_peers"])
}

func TestGetStats_ServiceError(t *testing.T) {
	mockService := &mockRedundancyService{
		GetStatsFunc: func(ctx context.Context) (map[string]interface{}, error) {
			return nil, errors.New("database error")
		},
	}

	handler := NewRedundancyHandler(mockService, nil)
	req := httptest.NewRequest("GET", "/redundancy/stats", nil)
	rec := httptest.NewRecorder()

	handler.GetStats(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
