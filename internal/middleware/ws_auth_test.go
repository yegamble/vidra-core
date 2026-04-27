package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// signedToken builds a valid HS256 JWT with the given subject.
func signedToken(t *testing.T, secret, sub string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": sub,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	s, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

func TestWSAuth_Subprotocol(t *testing.T) {
	secret := "test-ws-secret"
	userID := uuid.New().String()
	token := signedToken(t, secret, userID)

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "access_token, "+token)
	w := httptest.NewRecorder()

	var capturedUserID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Context().Value(UserIDKey); v != nil {
			capturedUserID, _ = v.(string)
		}
		w.WriteHeader(http.StatusOK)
	})

	WSAuth(secret)(next).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if capturedUserID != userID {
		t.Fatalf("expected userID %q in context, got %q", userID, capturedUserID)
	}
}

func TestWSAuth_QueryParam(t *testing.T) {
	secret := "test-ws-secret"
	userID := uuid.New().String()
	token := signedToken(t, secret, userID)

	req := httptest.NewRequest(http.MethodGet, "/ws?token="+token, nil)
	w := httptest.NewRecorder()

	var capturedUserID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Context().Value(UserIDKey); v != nil {
			capturedUserID, _ = v.(string)
		}
		w.WriteHeader(http.StatusOK)
	})

	WSAuth(secret)(next).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if capturedUserID != userID {
		t.Fatalf("expected userID %q in context, got %q", userID, capturedUserID)
	}
}

func TestWSAuth_AuthorizationHeader(t *testing.T) {
	secret := "test-ws-secret"
	userID := uuid.New().String()
	token := signedToken(t, secret, userID)

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	var capturedUserID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Context().Value(UserIDKey); v != nil {
			capturedUserID, _ = v.(string)
		}
		w.WriteHeader(http.StatusOK)
	})

	WSAuth(secret)(next).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if capturedUserID != userID {
		t.Fatalf("expected userID %q in context, got %q", userID, capturedUserID)
	}
}

func TestWSAuth_PrefersSubprotocolOverQueryParam(t *testing.T) {
	secret := "test-ws-secret"
	subprotoUser := uuid.New().String()
	queryUser := uuid.New().String()
	subprotoToken := signedToken(t, secret, subprotoUser)
	queryToken := signedToken(t, secret, queryUser)

	req := httptest.NewRequest(http.MethodGet, "/ws?token="+queryToken, nil)
	req.Header.Set("Sec-WebSocket-Protocol", "access_token, "+subprotoToken)
	w := httptest.NewRecorder()

	var capturedUserID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Context().Value(UserIDKey); v != nil {
			capturedUserID, _ = v.(string)
		}
		w.WriteHeader(http.StatusOK)
	})

	WSAuth(secret)(next).ServeHTTP(w, req)

	if capturedUserID != subprotoUser {
		t.Fatalf("expected subprotocol user %q, got %q", subprotoUser, capturedUserID)
	}
}

func TestWSAuth_InvalidToken(t *testing.T) {
	secret := "test-ws-secret"

	tests := []struct {
		name      string
		setupReq  func(*http.Request)
	}{
		{
			name: "invalid subprotocol token",
			setupReq: func(r *http.Request) {
				r.Header.Set("Sec-WebSocket-Protocol", "access_token, not.a.real.token")
			},
		},
		{
			name: "invalid query token",
			setupReq: func(r *http.Request) {
				q := r.URL.Query()
				q.Set("token", "not.a.real.token")
				r.URL.RawQuery = q.Encode()
			},
		},
		{
			name: "invalid bearer token",
			setupReq: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer not.a.real.token")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ws", nil)
			tt.setupReq(req)
			w := httptest.NewRecorder()

			handlerCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				w.WriteHeader(http.StatusOK)
			})

			WSAuth(secret)(next).ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", w.Code)
			}
			if handlerCalled {
				t.Fatal("next handler should not run on invalid token")
			}
		})
	}
}

func TestWSAuth_MissingToken(t *testing.T) {
	secret := "test-ws-secret"

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	w := httptest.NewRecorder()

	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	WSAuth(secret)(next).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if handlerCalled {
		t.Fatal("next handler should not run when no token present")
	}
}

func TestWSAuth_SubprotocolMalformed(t *testing.T) {
	secret := "test-ws-secret"

	tests := []struct {
		name  string
		value string
	}{
		{"missing access_token marker", "bearer-v1, anything"},
		{"only marker no token", "access_token"},
		{"empty value", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ws", nil)
			if tt.value != "" {
				req.Header.Set("Sec-WebSocket-Protocol", tt.value)
			}
			w := httptest.NewRecorder()

			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			WSAuth(secret)(next).ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 for malformed subprotocol, got %d", w.Code)
			}
		})
	}
}
