package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

func TestRequireScopes_WithValidScopes(t *testing.T) {
	tests := []struct {
		name           string
		tokenScopes    string
		requiredScopes []string
		shouldPass     bool
	}{
		{
			name:           "single scope matches",
			tokenScopes:    "upload",
			requiredScopes: []string{ScopeUpload},
			shouldPass:     true,
		},
		{
			name:           "multiple scopes all match",
			tokenScopes:    "upload write comment",
			requiredScopes: []string{ScopeUpload, ScopeWrite},
			shouldPass:     true,
		},
		{
			name:           "admin scope grants all permissions",
			tokenScopes:    "admin",
			requiredScopes: []string{ScopeUpload, ScopeWrite, ScopeModerate},
			shouldPass:     true,
		},
		{
			name:           "missing required scope",
			tokenScopes:    "basic profile",
			requiredScopes: []string{ScopeUpload},
			shouldPass:     false,
		},
		{
			name:           "partial match fails",
			tokenScopes:    "upload",
			requiredScopes: []string{ScopeUpload, ScopeWrite},
			shouldPass:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := createTokenWithScopes(tt.tokenScopes)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			ctx := context.WithValue(req.Context(), tokenKey, token)
			req = req.WithContext(ctx)

			w := httptest.NewRecorder()

			handlerCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				w.WriteHeader(http.StatusOK)
			})

			middleware := RequireScopes(tt.requiredScopes...)
			handler := middleware(next)
			handler.ServeHTTP(w, req)

			if tt.shouldPass {
				if w.Code != http.StatusOK {
					t.Errorf("Expected status OK, got %d", w.Code)
				}
				if !handlerCalled {
					t.Error("Expected next handler to be called")
				}
			} else {
				if w.Code != http.StatusForbidden {
					t.Errorf("Expected status Forbidden, got %d", w.Code)
				}
				if handlerCalled {
					t.Error("Expected next handler not to be called")
				}
			}
		})
	}
}

func TestRequireScopes_NoToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequireScopes(ScopeUpload)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status Unauthorized, got %d", w.Code)
	}

	if handlerCalled {
		t.Error("Expected next handler not to be called")
	}
}

func TestRequireScopes_InvalidTokenClaims(t *testing.T) {
	// Create a token without proper claims
	token := &jwt.Token{
		Claims: jwt.MapClaims{
			"sub": "user-123",
			// No scope claim
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), tokenKey, token)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequireScopes(ScopeUpload)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected status Forbidden, got %d", w.Code)
	}

	if handlerCalled {
		t.Error("Expected next handler not to be called")
	}
}

func TestRequireScopes_ScopesAsArray(t *testing.T) {
	// Test with scopes as array instead of space-separated string
	token := &jwt.Token{
		Claims: jwt.MapClaims{
			"sub":   "user-123",
			"scope": []interface{}{"upload", "write"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), tokenKey, token)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequireScopes(ScopeUpload)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", w.Code)
	}

	if !handlerCalled {
		t.Error("Expected next handler to be called")
	}
}

func TestGetScopes(t *testing.T) {
	t.Run("with scopes in context", func(t *testing.T) {
		expectedScopes := []string{"upload", "write", "comment"}
		ctx := context.WithValue(context.Background(), scopesKey{}, expectedScopes)

		scopes := GetScopes(ctx)

		if len(scopes) != len(expectedScopes) {
			t.Errorf("Expected %d scopes, got %d", len(expectedScopes), len(scopes))
		}

		for i, scope := range scopes {
			if scope != expectedScopes[i] {
				t.Errorf("Expected scope %s at index %d, got %s", expectedScopes[i], i, scope)
			}
		}
	})

	t.Run("without scopes in context", func(t *testing.T) {
		ctx := context.Background()

		scopes := GetScopes(ctx)

		if len(scopes) != 0 {
			t.Errorf("Expected empty scopes, got %d", len(scopes))
		}
	})
}

func TestHasScope(t *testing.T) {
	tests := []struct {
		name       string
		scopes     []string
		checkScope string
		expected   bool
	}{
		{
			name:       "scope exists",
			scopes:     []string{"upload", "write", "comment"},
			checkScope: "upload",
			expected:   true,
		},
		{
			name:       "scope does not exist",
			scopes:     []string{"upload", "write"},
			checkScope: "moderate",
			expected:   false,
		},
		{
			name:       "admin scope grants permission",
			scopes:     []string{"admin"},
			checkScope: "upload",
			expected:   true,
		},
		{
			name:       "empty scopes",
			scopes:     []string{},
			checkScope: "upload",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), scopesKey{}, tt.scopes)

			result := HasScope(ctx, tt.checkScope)

			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestOptionalScopes_WithToken(t *testing.T) {
	token := createTokenWithScopes("upload write")

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), tokenKey, token)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := OptionalScopes(ScopeUpload)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", w.Code)
	}

	if !handlerCalled {
		t.Error("Expected next handler to be called")
	}
}

func TestOptionalScopes_WithoutToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := OptionalScopes(ScopeUpload)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", w.Code)
	}

	if !handlerCalled {
		t.Error("Expected next handler to be called even without token")
	}
}

func TestOptionalScopes_WithTokenButMissingScope(t *testing.T) {
	token := createTokenWithScopes("basic profile")

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), tokenKey, token)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := OptionalScopes(ScopeUpload)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected status Forbidden, got %d", w.Code)
	}

	if handlerCalled {
		t.Error("Expected next handler not to be called when scope is missing")
	}
}

func TestScopeConstants(t *testing.T) {
	tests := []struct {
		name     string
		scope    string
		expected string
	}{
		{"ScopeBasic", ScopeBasic, "basic"},
		{"ScopeProfile", ScopeProfile, "profile"},
		{"ScopeEmail", ScopeEmail, "email"},
		{"ScopeUpload", ScopeUpload, "upload"},
		{"ScopeWrite", ScopeWrite, "write"},
		{"ScopeModerate", ScopeModerate, "moderate"},
		{"ScopeAdmin", ScopeAdmin, "admin"},
		{"ScopeMessages", ScopeMessages, "messages"},
		{"ScopeSubscribe", ScopeSubscribe, "subscribe"},
		{"ScopeComment", ScopeComment, "comment"},
		{"ScopeRate", ScopeRate, "rate"},
		{"ScopePlaylist", ScopePlaylist, "playlist"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.scope != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, tt.scope)
			}
		})
	}
}

func TestRequireScopes_WWWAuthenticateHeader(t *testing.T) {
	token := createTokenWithScopes("basic")

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), tokenKey, token)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequireScopes(ScopeUpload, ScopeWrite)
	handler := middleware(next)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected status Forbidden, got %d", w.Code)
	}

	wwwAuth := w.Header().Get("WWW-Authenticate")
	if wwwAuth == "" {
		t.Error("Expected WWW-Authenticate header to be set")
	}

	if !strings.Contains(wwwAuth, "insufficient_scope") {
		t.Error("WWW-Authenticate header should contain 'insufficient_scope'")
	}

	if !strings.Contains(wwwAuth, "upload") || !strings.Contains(wwwAuth, "write") {
		t.Error("WWW-Authenticate header should contain required scopes")
	}
}

// Helper function to create a JWT token with scopes
func createTokenWithScopes(scopes string) *jwt.Token {
	return &jwt.Token{
		Claims: jwt.MapClaims{
			"sub":   "user-123",
			"scope": scopes,
			"exp":   time.Now().Add(time.Hour).Unix(),
		},
	}
}
