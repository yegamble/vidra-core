package moderation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

// MockBlocklistStatusRepo implements BlocklistStatusRepository.
type MockBlocklistStatusRepo struct {
	entries []*domain.BlocklistEntry
	err     error
}

func (m *MockBlocklistStatusRepo) ListBlocklistEntries(ctx context.Context, blockType string, activeOnly bool, limit, offset int) ([]*domain.BlocklistEntry, int64, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	var filtered []*domain.BlocklistEntry
	for _, e := range m.entries {
		if blockType == "" || string(e.BlockType) == blockType {
			filtered = append(filtered, e)
		}
	}
	return filtered, int64(len(filtered)), nil
}

func TestBlocklistStatusHandler_Success(t *testing.T) {
	repo := &MockBlocklistStatusRepo{
		entries: []*domain.BlocklistEntry{
			{ID: "1", BlockType: domain.BlockTypeUser, BlockedValue: "user@example.com", IsActive: true},
			{ID: "2", BlockType: domain.BlockTypeDomain, BlockedValue: "spam.example.com", IsActive: true},
		},
	}

	handler := BlocklistStatusHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/blocklist/status", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data struct {
			Accounts []domain.BlocklistEntry `json:"accounts"`
			Servers  []domain.BlocklistEntry `json:"servers"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data.Accounts, 1)
	assert.Len(t, resp.Data.Servers, 1)
}

func TestBlocklistStatusHandler_Unauthenticated(t *testing.T) {
	repo := &MockBlocklistStatusRepo{}
	handler := BlocklistStatusHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/blocklist/status", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
