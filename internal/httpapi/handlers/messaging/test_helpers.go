package messaging

import (
	"context"
	"time"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
)

// Response is an alias for shared.Response for tests
type Response = shared.Response

// ErrorInfo is an alias for shared.ErrorInfo for tests
type ErrorInfo = shared.ErrorInfo

// Meta is an alias for shared.Meta for tests
type Meta = shared.Meta

// MockLiveStreamRepository is a mock implementation for tests
type MockLiveStreamRepository struct{}

// Placeholder methods for MockLiveStreamRepository
func (m *MockLiveStreamRepository) GetByID(ctx context.Context, id string) (*domain.LiveStream, error) {
	return nil, nil
}

// createTestUser is a helper to create test users
func createTestUser(id, username string) *domain.User {
	return &domain.User{
		ID:        id,
		Username:  username,
		Email:     username + "@test.com",
		Role:      domain.RoleUser,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}
