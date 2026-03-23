package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/middleware"

	"github.com/stretchr/testify/assert"
)

func TestDeleteAvatar_NoContent(t *testing.T) {
	repo := newMockUserRepo()
	repo.users["user-1"] = &domain.User{ID: "user-1", Username: "alice"}
	h := &AuthHandlers{userRepo: repo}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/me/avatar", nil)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, "user-1")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	h.DeleteAvatar(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestDeleteAvatar_Unauthenticated(t *testing.T) {
	h := &AuthHandlers{userRepo: newMockUserRepo()}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/me/avatar", nil)
	w := httptest.NewRecorder()
	h.DeleteAvatar(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
