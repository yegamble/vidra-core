package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"athena/internal/domain"
	"athena/internal/middleware"
)

func Login(w http.ResponseWriter, r *http.Request) {
	var req domain.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.Email == "" || req.Password == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CREDENTIALS", "Email and password are required"))
		return
	}

	user := domain.User{
		ID:          "user123",
		Username:    "testuser",
		Email:       req.Email,
		DisplayName: "Test User",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now().AddDate(0, 0, -30),
		UpdatedAt:   time.Now(),
	}

	response := domain.AuthResponse{
		User:         user,
		AccessToken:  generateJWT(user.ID, 15*time.Minute),
		RefreshToken: generateJWT(user.ID, 24*time.Hour),
		ExpiresIn:    15 * 60,
	}

	WriteJSON(w, http.StatusOK, response)
}

func Register(w http.ResponseWriter, r *http.Request) {
	var req domain.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.Username == "" || req.Email == "" || req.Password == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FIELDS", "Username, email, and password are required"))
		return
	}

	user := domain.User{
		ID:          "user" + time.Now().Format("20060102150405"),
		Username:    req.Username,
		Email:       req.Email,
		DisplayName: req.DisplayName,
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	response := domain.AuthResponse{
		User:         user,
		AccessToken:  generateJWT(user.ID, 15*time.Minute),
		RefreshToken: generateJWT(user.ID, 24*time.Hour),
		ExpiresIn:    15 * 60,
	}

	WriteJSON(w, http.StatusCreated, response)
}

func RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.RefreshToken == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_TOKEN", "Refresh token is required"))
		return
	}

	response := map[string]interface{}{
		"access_token":  generateJWT("user123", 15*time.Minute),
		"refresh_token": generateJWT("user123", 24*time.Hour),
		"expires_in":    15 * 60,
	}

	WriteJSON(w, http.StatusOK, response)
}

func Logout(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(middleware.UserIDKey).(string)
	
	response := map[string]interface{}{
		"message": "Logged out successfully",
		"user_id": userID,
	}

	WriteJSON(w, http.StatusOK, response)
}

func generateJWT(userID string, duration time.Duration) string {
	return "jwt-token-" + userID + "-" + time.Now().Format("20060102150405")
}