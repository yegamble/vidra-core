package middleware

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"

	"github.com/google/uuid"
)

type mockUserRepository struct {
	users       map[string]*domain.User
	shouldError bool
}

func (m *mockUserRepository) GetByID(ctx context.Context, userID string) (*domain.User, error) {
	if m.shouldError {
		return nil, errors.New("database error")
	}

	user, exists := m.users[userID]
	if !exists {
		return nil, domain.ErrUserNotFound
	}

	return user, nil
}

// Stub methods to satisfy port.UserRepository interface
func (m *mockUserRepository) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	return nil
}

func (m *mockUserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return nil, nil
}

func (m *mockUserRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	return nil, nil
}

func (m *mockUserRepository) Update(ctx context.Context, user *domain.User) error {
	return nil
}

func (m *mockUserRepository) Delete(ctx context.Context, id string) error {
	return nil
}

func (m *mockUserRepository) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	return "", nil
}

func (m *mockUserRepository) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	return nil
}

func (m *mockUserRepository) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	return nil, nil
}

func (m *mockUserRepository) Count(ctx context.Context) (int64, error) {
	return 0, nil
}

func (m *mockUserRepository) SetAvatarFields(ctx context.Context, userID string, ipfsCID, webpCID sql.NullString) error {
	return nil
}

func (m *mockUserRepository) MarkEmailAsVerified(ctx context.Context, userID string) error {
	return nil
}

func (m *mockUserRepository) Anonymize(_ context.Context, _ string) error { return nil }

func TestRequireEmailVerification_Success(t *testing.T) {
	userID := uuid.New().String()
	repo := &mockUserRepository{
		users: map[string]*domain.User{
			userID: {
				ID:            userID,
				Username:      "testuser",
				Email:         "test@example.com",
				EmailVerified: true,
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), UserIDKey, userID)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true

		// Check if email verified flag is in context
		verified, ok := GetEmailVerifiedFromContext(r.Context())
		if !ok {
			t.Error("Expected email verified status in context")
		}

		if !verified {
			t.Error("Expected verified to be true")
		}

		w.WriteHeader(http.StatusOK)
	})

	middleware := RequireEmailVerification(repo)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	if !handlerCalled {
		t.Error("Expected next handler to be called")
	}
}

func TestRequireEmailVerification_NoUserID(t *testing.T) {
	repo := &mockUserRepository{
		users: map[string]*domain.User{},
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequireEmailVerification(repo)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}

	if handlerCalled {
		t.Error("Expected next handler not to be called")
	}
}

func TestRequireEmailVerification_UserNotFound(t *testing.T) {
	userID := uuid.New().String()
	repo := &mockUserRepository{
		users: map[string]*domain.User{},
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), UserIDKey, userID)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequireEmailVerification(repo)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}

	if handlerCalled {
		t.Error("Expected next handler not to be called")
	}
}

func TestRequireEmailVerification_EmailNotVerified(t *testing.T) {
	userID := uuid.New().String()
	repo := &mockUserRepository{
		users: map[string]*domain.User{
			userID: {
				ID:            userID,
				Username:      "testuser",
				Email:         "test@example.com",
				EmailVerified: false,
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), UserIDKey, userID)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequireEmailVerification(repo)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected status %d, got %d", http.StatusForbidden, w.Code)
	}

	if handlerCalled {
		t.Error("Expected next handler not to be called")
	}
}

func TestRequireEmailVerification_DatabaseError(t *testing.T) {
	userID := uuid.New().String()
	repo := &mockUserRepository{
		users:       map[string]*domain.User{},
		shouldError: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), UserIDKey, userID)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequireEmailVerification(repo)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status %d, got %d", http.StatusInternalServerError, w.Code)
	}

	if handlerCalled {
		t.Error("Expected next handler not to be called")
	}
}

func TestOptionalEmailVerification_WithVerifiedUser(t *testing.T) {
	userID := uuid.New().String()
	repo := &mockUserRepository{
		users: map[string]*domain.User{
			userID: {
				ID:            userID,
				Username:      "testuser",
				Email:         "test@example.com",
				EmailVerified: true,
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), UserIDKey, userID)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true

		verified, ok := GetEmailVerifiedFromContext(r.Context())
		if !ok {
			t.Error("Expected email verified status in context")
		}

		if !verified {
			t.Error("Expected verified to be true")
		}

		w.WriteHeader(http.StatusOK)
	})

	middleware := OptionalEmailVerification(repo)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	if !handlerCalled {
		t.Error("Expected next handler to be called")
	}
}

func TestOptionalEmailVerification_WithUnverifiedUser(t *testing.T) {
	userID := uuid.New().String()
	repo := &mockUserRepository{
		users: map[string]*domain.User{
			userID: {
				ID:            userID,
				Username:      "testuser",
				Email:         "test@example.com",
				EmailVerified: false,
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), UserIDKey, userID)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true

		verified, ok := GetEmailVerifiedFromContext(r.Context())
		if !ok {
			t.Error("Expected email verified status in context")
		}

		if verified {
			t.Error("Expected verified to be false")
		}

		w.WriteHeader(http.StatusOK)
	})

	middleware := OptionalEmailVerification(repo)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	if !handlerCalled {
		t.Error("Expected next handler to be called")
	}
}

func TestOptionalEmailVerification_NoUserID(t *testing.T) {
	repo := &mockUserRepository{
		users: map[string]*domain.User{},
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true

		// Should still call next handler even without user ID
		w.WriteHeader(http.StatusOK)
	})

	middleware := OptionalEmailVerification(repo)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	if !handlerCalled {
		t.Error("Expected next handler to be called")
	}
}

func TestOptionalEmailVerification_UserNotFound(t *testing.T) {
	userID := uuid.New().String()
	repo := &mockUserRepository{
		users: map[string]*domain.User{},
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), UserIDKey, userID)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		// Should still call next handler even if user not found
		w.WriteHeader(http.StatusOK)
	})

	middleware := OptionalEmailVerification(repo)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	if !handlerCalled {
		t.Error("Expected next handler to be called")
	}
}

func TestGetEmailVerifiedFromContext(t *testing.T) {
	t.Run("with verified status true", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), EmailVerifiedContextKey, true)

		verified, ok := GetEmailVerifiedFromContext(ctx)
		if !ok {
			t.Error("Expected ok=true")
		}

		if !verified {
			t.Error("Expected verified=true")
		}
	})

	t.Run("with verified status false", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), EmailVerifiedContextKey, false)

		verified, ok := GetEmailVerifiedFromContext(ctx)
		if !ok {
			t.Error("Expected ok=true")
		}

		if verified {
			t.Error("Expected verified=false")
		}
	})

	t.Run("without verified status", func(t *testing.T) {
		ctx := context.Background()

		_, ok := GetEmailVerifiedFromContext(ctx)
		if ok {
			t.Error("Expected ok=false when status not in context")
		}
	})

	t.Run("with wrong type in context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), EmailVerifiedContextKey, "not a bool")

		_, ok := GetEmailVerifiedFromContext(ctx)
		if ok {
			t.Error("Expected ok=false when wrong type in context")
		}
	})
}
