package api

import (
    "encoding/json"
    "net/http"

    "github.com/go-chi/chi/v5"

    "gotube/internal/usecase"
)

// AuthHandler holds dependencies for authentication endpoints.
type AuthHandler struct {
    Usecase *usecase.AuthUsecase
}

// RegisterRoutes registers auth-related routes on the router under /auth.
func (h *AuthHandler) RegisterRoutes(r chi.Router) {
    r.Post("/signup", h.SignUp)
    r.Post("/login", h.Login)
    // Additional routes (verification) could be added here
}

// SignUp handles POST /auth/signup requests. It expects a JSON body
// containing "email" and "password" fields. It returns the created
// user (without password) in the response body.
func (h *AuthHandler) SignUp(w http.ResponseWriter, r *http.Request) {
    var req struct {
        Email    string `json:"email"`
        Password string `json:"password"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid request", http.StatusBadRequest)
        return
    }
    user, err := h.Usecase.SignUp(r.Context(), req.Email, req.Password)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    // Clear password
    user.PasswordHash = ""
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(user)
}

// Login handles POST /auth/login. It expects JSON with email/password and
// returns a token if credentials are valid. The token is included in
// the response body.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
    var req struct {
        Email    string `json:"email"`
        Password string `json:"password"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid request", http.StatusBadRequest)
        return
    }
    token, err := h.Usecase.Login(r.Context(), req.Email, req.Password)
    if err != nil {
        http.Error(w, err.Error(), http.StatusUnauthorized)
        return
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"token": token})
}