package auth

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/bcrypt"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

// MockAccountDeletionRepo mocks the AccountDeletionRepository interface.
type MockAccountDeletionRepo struct {
	getByIDFunc     func(ctx context.Context, id string) (*domain.User, error)
	getPasswordHash func(ctx context.Context, userID string) (string, error)
	anonymizeFunc   func(ctx context.Context, userID string) error
}

func (m *MockAccountDeletionRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	return m.getByIDFunc(ctx, id)
}

func (m *MockAccountDeletionRepo) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	return m.getPasswordHash(ctx, userID)
}

func (m *MockAccountDeletionRepo) Anonymize(ctx context.Context, userID string) error {
	return m.anonymizeFunc(ctx, userID)
}

func TestDeleteAccountHandler_Success(t *testing.T) {
	// Hash a test password
	hash, _ := bcrypt.GenerateFromPassword([]byte("correctpassword"), bcrypt.MinCost)

	repo := &MockAccountDeletionRepo{
		getByIDFunc: func(ctx context.Context, id string) (*domain.User, error) {
			return &domain.User{ID: id, Username: "testuser", Email: "test@example.com"}, nil
		},
		getPasswordHash: func(ctx context.Context, userID string) (string, error) {
			return string(hash), nil
		},
		anonymizeFunc: func(ctx context.Context, userID string) error {
			return nil
		},
	}

	handler := DeleteAccountHandler(repo)

	body := `{"password":"correctpassword"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/me", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestDeleteAccountHandler_WrongPassword(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("correctpassword"), bcrypt.MinCost)

	repo := &MockAccountDeletionRepo{
		getByIDFunc: func(ctx context.Context, id string) (*domain.User, error) {
			return &domain.User{ID: id}, nil
		},
		getPasswordHash: func(ctx context.Context, userID string) (string, error) {
			return string(hash), nil
		},
		anonymizeFunc: func(ctx context.Context, userID string) error {
			return nil
		},
	}

	handler := DeleteAccountHandler(repo)

	body := `{"password":"wrongpassword"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/me", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDeleteAccountHandler_Unauthenticated(t *testing.T) {
	repo := &MockAccountDeletionRepo{}

	handler := DeleteAccountHandler(repo)

	body := `{"password":"anything"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/me", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestDeleteAccountHandler_MissingPassword(t *testing.T) {
	repo := &MockAccountDeletionRepo{
		getByIDFunc: func(ctx context.Context, id string) (*domain.User, error) {
			return &domain.User{ID: id}, nil
		},
	}

	handler := DeleteAccountHandler(repo)

	body := `{"password":""}`
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/me", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
