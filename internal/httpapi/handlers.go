package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"athena/internal/domain"
	"athena/internal/generated"
	"athena/internal/middleware"
)

// Server implements the generated ServerInterface
type Server struct{}

// NewServer creates a new server instance
func NewServer() *Server {
	return &Server{}
}

// Login implements ServerInterface.Login
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var req generated.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.Email == "" || req.Password == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CREDENTIALS", "Email and password are required"))
		return
	}

	user := generated.User{
		ID:          "user123",
		Username:    "testuser",
		Email:       req.Email,
		DisplayName: stringPtr("Test User"),
		Role:        generated.UserRoleUser,
		IsActive:    true,
		CreatedAt:   time.Now().AddDate(0, 0, -30),
		UpdatedAt:   time.Now(),
	}

	response := generated.AuthResponse{
		User:         user,
		AccessToken:  generateJWT("user123", 15*time.Minute),
		RefreshToken: generateJWT("user123", 24*time.Hour),
		ExpiresIn:    15 * 60,
	}

	WriteJSON(w, http.StatusOK, response)
}

// Register implements ServerInterface.Register
func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	var req generated.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.Username == "" || req.Email == "" || req.Password == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FIELDS", "Username, email, and password are required"))
		return
	}

	user := generated.User{
		ID:          "user" + time.Now().Format("20060102150405"),
		Username:    req.Username,
		Email:       req.Email,
		DisplayName: req.DisplayName,
		Role:        generated.UserRoleUser,
		IsActive:    true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	response := generated.AuthResponse{
		User:         user,
		AccessToken:  generateJWT(user.ID, 15*time.Minute),
		RefreshToken: generateJWT(user.ID, 24*time.Hour),
		ExpiresIn:    15 * 60,
	}

	WriteJSON(w, http.StatusCreated, response)
}

// RefreshToken implements ServerInterface.RefreshToken
func (s *Server) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req generated.RefreshTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.RefreshToken == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_TOKEN", "Refresh token is required"))
		return
	}

	response := generated.TokenResponse{
		AccessToken:  generateJWT("user123", 15*time.Minute),
		RefreshToken: generateJWT("user123", 24*time.Hour),
		ExpiresIn:    15 * 60,
	}

	WriteJSON(w, http.StatusOK, response)
}

// Logout implements ServerInterface.Logout
func (s *Server) Logout(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(middleware.UserIDKey).(string)

	response := generated.LogoutResponse{
		Message: "Logged out successfully",
		UserID:  &userID,
	}

	WriteJSON(w, http.StatusOK, response)
}

// HealthCheck implements ServerInterface.HealthCheck
func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	response := generated.HealthResponse{
		Status:    generated.HealthStatusHealthy,
		Timestamp: time.Now(),
	}

	WriteJSON(w, http.StatusOK, response)
}

// ReadinessCheck implements ServerInterface.ReadinessCheck
func (s *Server) ReadinessCheck(w http.ResponseWriter, r *http.Request) {
	// In a real implementation, you would check actual services
	dbStatus := generated.ServiceStatusHealthy
	redisStatus := generated.ServiceStatusHealthy
	ipfsStatus := generated.ServiceStatusHealthy

	response := generated.ReadinessResponse{
		Status: generated.ReadinessStatusReady,
		Checks: generated.ReadinessResponseChecks{
			Database: &dbStatus,
			Redis:    &redisStatus,
			IPFS:     &ipfsStatus,
		},
		Timestamp: time.Now(),
	}

	WriteJSON(w, http.StatusOK, response)
}

// Helper functions
func generateJWT(userID string, duration time.Duration) string {
	return "jwt-token-" + userID + "-" + time.Now().Format("20060102150405")
}

func stringPtr(s string) *string {
	return &s
}