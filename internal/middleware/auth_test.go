package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vidra-core/internal/domain"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestAuth(t *testing.T) {
	jwtSecret := "test-secret-key"

	tests := []struct {
		name           string
		authHeader     string
		expectedStatus int
		checkContext   bool
	}{
		{
			name:           "missing authorization header",
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
			checkContext:   false,
		},
		{
			name:           "invalid authorization format",
			authHeader:     "InvalidFormat token123",
			expectedStatus: http.StatusUnauthorized,
			checkContext:   false,
		},
		{
			name:           "invalid token",
			authHeader:     "Bearer invalid.token.here",
			expectedStatus: http.StatusUnauthorized,
			checkContext:   false,
		},
		{
			name:           "valid token",
			authHeader:     generateValidToken(jwtSecret),
			expectedStatus: http.StatusOK,
			checkContext:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			w := httptest.NewRecorder()

			handlerCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true

				if tt.checkContext {
					userID := r.Context().Value(UserIDKey)
					if userID == nil {
						t.Error("Expected userID in context, got nil")
					}
				}

				w.WriteHeader(http.StatusOK)
			})

			authMiddleware := Auth(jwtSecret)
			handler := authMiddleware(next)
			handler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.expectedStatus == http.StatusOK && !handlerCalled {
				t.Error("Expected next handler to be called")
			}

			if tt.expectedStatus != http.StatusOK && handlerCalled {
				t.Error("Expected next handler not to be called")
			}
		})
	}
}

func TestOptionalAuth(t *testing.T) {
	jwtSecret := "test-secret-key"

	tests := []struct {
		name         string
		authHeader   string
		expectUserID bool
	}{
		{
			name:         "no authorization header",
			authHeader:   "",
			expectUserID: false,
		},
		{
			name:         "invalid token format",
			authHeader:   "InvalidFormat token123",
			expectUserID: false,
		},
		{
			name:         "invalid token",
			authHeader:   "Bearer invalid.token.here",
			expectUserID: false,
		},
		{
			name:         "valid token",
			authHeader:   generateValidToken(jwtSecret),
			expectUserID: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			w := httptest.NewRecorder()

			handlerCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true

				userID := r.Context().Value(UserIDKey)
				if tt.expectUserID && userID == nil {
					t.Error("Expected userID in context, got nil")
				}
				if !tt.expectUserID && userID != nil {
					t.Error("Expected no userID in context, got one")
				}

				w.WriteHeader(http.StatusOK)
			})

			authMiddleware := OptionalAuth(jwtSecret)
			handler := authMiddleware(next)
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
			}

			if !handlerCalled {
				t.Error("Expected next handler to be called")
			}
		})
	}
}

func TestValidateJWT(t *testing.T) {
	jwtSecret := "test-secret-key"
	userID := uuid.New().String()
	role := "admin"

	t.Run("valid token with role", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub":  userID,
			"role": role,
			"exp":  time.Now().Add(time.Hour).Unix(),
		})

		tokenString, err := token.SignedString([]byte(jwtSecret))
		if err != nil {
			t.Fatalf("Failed to sign token: %v", err)
		}

		gotUserID, gotRole, err := validateJWT(tokenString, jwtSecret)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}

		if gotUserID != userID {
			t.Errorf("Expected userID %s, got %s", userID, gotUserID)
		}

		if gotRole != role {
			t.Errorf("Expected role %s, got %s", role, gotRole)
		}
	})

	t.Run("valid token without role", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": userID,
			"exp": time.Now().Add(time.Hour).Unix(),
		})

		tokenString, err := token.SignedString([]byte(jwtSecret))
		if err != nil {
			t.Fatalf("Failed to sign token: %v", err)
		}

		gotUserID, gotRole, err := validateJWT(tokenString, jwtSecret)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}

		if gotUserID != userID {
			t.Errorf("Expected userID %s, got %s", userID, gotUserID)
		}

		if gotRole != "" {
			t.Errorf("Expected empty role, got %s", gotRole)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": userID,
			"exp": time.Now().Add(-time.Hour).Unix(),
		})

		tokenString, err := token.SignedString([]byte(jwtSecret))
		if err != nil {
			t.Fatalf("Failed to sign token: %v", err)
		}

		_, _, err = validateJWT(tokenString, jwtSecret)
		if err == nil {
			t.Error("Expected error for expired token")
		}
	})

	t.Run("wrong signing method", func(t *testing.T) {
		_, _, err := validateJWT("eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.test.test", jwtSecret)
		if err == nil {
			t.Error("Expected error for wrong signing method")
		}
	})
}

func TestGetUserIDFromContext(t *testing.T) {
	t.Run("valid user ID", func(t *testing.T) {
		expectedID := uuid.New()
		ctx := context.WithValue(context.Background(), UserIDKey, expectedID.String())

		gotID, ok := GetUserIDFromContext(ctx)
		if !ok {
			t.Error("Expected ok=true")
		}

		if gotID != expectedID {
			t.Errorf("Expected ID %s, got %s", expectedID, gotID)
		}
	})

	t.Run("no user ID in context", func(t *testing.T) {
		ctx := context.Background()

		_, ok := GetUserIDFromContext(ctx)
		if ok {
			t.Error("Expected ok=false for missing user ID")
		}
	})

	t.Run("invalid UUID format", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), UserIDKey, "not-a-uuid")

		_, ok := GetUserIDFromContext(ctx)
		if ok {
			t.Error("Expected ok=false for invalid UUID")
		}
	})
}

func TestRequireAuth(t *testing.T) {
	t.Run("with user ID in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := context.WithValue(req.Context(), UserIDKey, uuid.New().String())
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()

		handlerCalled := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		handler := RequireAuth(next)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}

		if !handlerCalled {
			t.Error("Expected next handler to be called")
		}
	})

	t.Run("without user ID in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()

		handlerCalled := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		handler := RequireAuth(next)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
		}

		if handlerCalled {
			t.Error("Expected next handler not to be called")
		}
	})
}

func TestRequireRole(t *testing.T) {
	requiredRole := "admin"

	t.Run("with matching role", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := context.WithValue(req.Context(), UserIDKey, uuid.New().String())
		ctx = context.WithValue(ctx, UserRoleKey, requiredRole)
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()

		handlerCalled := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		handler := RequireRole(requiredRole)(next)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}

		if !handlerCalled {
			t.Error("Expected next handler to be called")
		}
	})

	t.Run("without user ID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()

		handlerCalled := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		handler := RequireRole(requiredRole)(next)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
		}

		if handlerCalled {
			t.Error("Expected next handler not to be called")
		}
	})

	t.Run("without role in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := context.WithValue(req.Context(), UserIDKey, uuid.New().String())
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()

		handlerCalled := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		handler := RequireRole(requiredRole)(next)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("Expected status %d, got %d", http.StatusForbidden, w.Code)
		}

		if handlerCalled {
			t.Error("Expected next handler not to be called")
		}
	})

	t.Run("with non-matching role", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := context.WithValue(req.Context(), UserIDKey, uuid.New().String())
		ctx = context.WithValue(ctx, UserRoleKey, "user")
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()

		handlerCalled := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		handler := RequireRole(requiredRole)(next)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("Expected status %d, got %d", http.StatusForbidden, w.Code)
		}

		if handlerCalled {
			t.Error("Expected next handler not to be called")
		}
	})

	t.Run("with one of multiple allowed roles", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := context.WithValue(req.Context(), UserIDKey, uuid.New().String())
		ctx = context.WithValue(ctx, UserRoleKey, "moderator")
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()

		handlerCalled := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		handler := RequireRole("admin", "moderator")(next)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}

		if !handlerCalled {
			t.Error("Expected next handler to be called")
		}
	})
}

func TestWriteError(t *testing.T) {
	t.Run("domain error", func(t *testing.T) {
		w := httptest.NewRecorder()
		err := domain.NewDomainError("TEST_CODE", "Test message")

		writeError(w, http.StatusBadRequest, err)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
		}

		contentType := w.Header().Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("Expected Content-Type application/json, got %s", contentType)
		}
	})

	t.Run("regular error", func(t *testing.T) {
		w := httptest.NewRecorder()
		err := domain.NewDomainError("GENERIC_ERROR", "Generic error message")

		writeError(w, http.StatusInternalServerError, err)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("Expected status %d, got %d", http.StatusInternalServerError, w.Code)
		}
	})
}

func generateValidToken(jwtSecret string) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": uuid.New().String(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	tokenString, _ := token.SignedString([]byte(jwtSecret))
	return "Bearer " + tokenString
}

func generateTokenWithRole(jwtSecret, sub, role string) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  sub,
		"role": role,
		"exp":  time.Now().Add(time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString([]byte(jwtSecret))
	return "Bearer " + tokenString
}

func TestAuth_WithUserLookup(t *testing.T) {
	jwtSecret := "test-secret-key"
	userIDStr := uuid.New().String()

	t.Run("nil lookup falls back to JWT role", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", generateTokenWithRole(jwtSecret, userIDStr, "admin"))
		w := httptest.NewRecorder()

		var gotRole string
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotRole, _ = r.Context().Value(UserRoleKey).(string)
			w.WriteHeader(http.StatusOK)
		})

		handler := AuthWithUserLookup(jwtSecret, nil)(next)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", w.Code)
		}
		if gotRole != "admin" {
			t.Errorf("Expected role=admin, got %q", gotRole)
		}
	})

	t.Run("lookup returns DB role overriding JWT role", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", generateTokenWithRole(jwtSecret, userIDStr, "admin"))
		w := httptest.NewRecorder()

		lookup := func(_ context.Context, id string) (*domain.User, error) {
			return &domain.User{ID: id, Role: domain.RoleUser, IsActive: true}, nil
		}

		var gotRole string
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotRole, _ = r.Context().Value(UserRoleKey).(string)
			w.WriteHeader(http.StatusOK)
		})

		handler := AuthWithUserLookup(jwtSecret, lookup)(next)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", w.Code)
		}
		if gotRole != string(domain.RoleUser) {
			t.Errorf("Expected DB role %q, got %q", domain.RoleUser, gotRole)
		}
	})

	t.Run("banned user rejected with 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", generateTokenWithRole(jwtSecret, userIDStr, "user"))
		w := httptest.NewRecorder()

		lookup := func(_ context.Context, id string) (*domain.User, error) {
			return &domain.User{ID: id, Role: domain.RoleUser, IsActive: false}, nil
		}

		handlerCalled := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		handler := AuthWithUserLookup(jwtSecret, lookup)(next)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401 for banned user, got %d", w.Code)
		}
		if handlerCalled {
			t.Error("Expected next handler not to be called for banned user")
		}
	})

	t.Run("user not found in DB rejected with 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", generateTokenWithRole(jwtSecret, userIDStr, "user"))
		w := httptest.NewRecorder()

		lookup := func(_ context.Context, id string) (*domain.User, error) {
			return nil, domain.ErrUserNotFound
		}

		handlerCalled := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		handler := AuthWithUserLookup(jwtSecret, lookup)(next)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401 for missing user, got %d", w.Code)
		}
		if handlerCalled {
			t.Error("Expected next handler not to be called for missing user")
		}
	})
}
