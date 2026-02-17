package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegister_InvalidEmail(t *testing.T) {
	tests := []struct {
		name     string
		email    string
		wantCode int
	}{
		{"missing at sign", "notanemail", http.StatusBadRequest},
		{"missing domain", "user@", http.StatusBadRequest},
		{"empty email", "", http.StatusBadRequest},
	}

	s := &Server{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{
				"username": "testuser",
				"email":    tt.email,
				"password": "ValidPass1",
			})
			req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.Register(w, req)
			assert.Equal(t, tt.wantCode, w.Code, "email=%q", tt.email)
		})
	}
}

func TestRegister_WeakPassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantCode int
	}{
		{"too short", "Ab1", http.StatusBadRequest},
		{"no uppercase", "password1", http.StatusBadRequest},
		{"no lowercase", "PASSWORD1", http.StatusBadRequest},
		{"no digit", "PasswordOnly", http.StatusBadRequest},
	}

	s := &Server{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{
				"username": "testuser",
				"email":    "valid@example.com",
				"password": tt.password,
			})
			req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.Register(w, req)
			assert.Equal(t, tt.wantCode, w.Code, "password=%q", tt.password)
		})
	}
}
