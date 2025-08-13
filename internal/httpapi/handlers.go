package httpapi

import (
    "encoding/json"
    "net/http"
    "time"

    "github.com/google/uuid"
    "golang.org/x/crypto/bcrypt"

    "athena/internal/domain"
    "athena/internal/generated"
    "athena/internal/middleware"
    "athena/internal/usecase"
)

// Server implements the generated ServerInterface
type Server struct{
    userRepo usecase.UserRepository
    authRepo usecase.AuthRepository
}

// NewServer creates a new server instance
func NewServer(userRepo usecase.UserRepository, authRepo usecase.AuthRepository) *Server {
    return &Server{userRepo: userRepo, authRepo: authRepo}
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

    // Lookup user and verify password
    dUser, err := s.userRepo.GetByEmail(r.Context(), req.Email)
    if err != nil {
        WriteError(w, http.StatusUnauthorized, domain.ErrInvalidCredentials)
        return
    }
    hash, err := s.userRepo.GetPasswordHash(r.Context(), dUser.ID)
    if err != nil {
        WriteError(w, http.StatusUnauthorized, domain.ErrInvalidCredentials)
        return
    }
    if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
        WriteError(w, http.StatusUnauthorized, domain.ErrInvalidCredentials)
        return
    }

    // Issue tokens
    access := generateJWT(dUser.ID, 15*time.Minute)
    refresh := uuid.NewString()
    if s.authRepo != nil {
        rt := &usecase.RefreshToken{
            ID:        uuid.NewString(),
            UserID:    dUser.ID,
            Token:     refresh,
            ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
            CreatedAt: time.Now(),
        }
        if err := s.authRepo.CreateRefreshToken(r.Context(), rt); err != nil {
            WriteError(w, http.StatusInternalServerError, domain.NewDomainError("TOKEN_ISSUE_FAILED", "Failed to create refresh token"))
            return
        }
    }

    // Map to generated.User
    var dispPtr *string
    if dUser.DisplayName != "" { disp := dUser.DisplayName; dispPtr = &disp }
    gUser := generated.User{
        ID:          dUser.ID,
        Username:    dUser.Username,
        Email:       dUser.Email,
        DisplayName: dispPtr,
        Role:        generated.UserRoleUser,
        IsActive:    dUser.IsActive,
        CreatedAt:   dUser.CreatedAt,
        UpdatedAt:   dUser.UpdatedAt,
    }

    response := generated.AuthResponse{
        User:         gUser,
        AccessToken:  access,
        RefreshToken: refresh,
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

    // Optional pre-check for clearer 409s
    if s.userRepo != nil {
        if _, err := s.userRepo.GetByEmail(r.Context(), req.Email); err == nil {
            WriteError(w, http.StatusConflict, domain.NewDomainError("USER_EXISTS", "Email already in use"))
            return
        }
        if _, err := s.userRepo.GetByUsername(r.Context(), req.Username); err == nil {
            WriteError(w, http.StatusConflict, domain.NewDomainError("USER_EXISTS", "Username already in use"))
            return
        }

        now := time.Now()
        userID := uuid.NewString()
        displayName := ""
        if req.DisplayName != nil { displayName = *req.DisplayName }

        dUser := &domain.User{
            ID:          userID,
            Username:    req.Username,
            Email:       req.Email,
            DisplayName: displayName,
            Role:        domain.RoleUser,
            IsActive:    true,
            CreatedAt:   now,
            UpdatedAt:   now,
        }

        // Hash password
        hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
        if err != nil {
            WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to process password"))
            return
        }

        if err := s.userRepo.Create(r.Context(), dUser, string(hash)); err != nil {
            status := MapDomainErrorToHTTP(domain.ErrConflict)
            WriteError(w, status, domain.NewDomainError("CREATE_FAILED", "Failed to create user"))
            return
        }

        // Map to generated.User
        var dispPtr *string
        if dUser.DisplayName != "" { disp := dUser.DisplayName; dispPtr = &disp }
        gUser := generated.User{
            ID:          dUser.ID,
            Username:    dUser.Username,
            Email:       dUser.Email,
            DisplayName: dispPtr,
            Role:        generated.UserRoleUser,
            IsActive:    dUser.IsActive,
            CreatedAt:   dUser.CreatedAt,
            UpdatedAt:   dUser.UpdatedAt,
        }

        // Set Location header to new resource
        w.Header().Set("Location", "/api/v1/users/"+gUser.ID)

        response := generated.AuthResponse{
            User:         gUser,
            AccessToken:  generateJWT(gUser.ID, 15*time.Minute),
            RefreshToken: generateJWT(gUser.ID, 24*time.Hour),
            ExpiresIn:    15 * 60,
        }

        WriteJSON(w, http.StatusCreated, response)
        return
    }

    // Fallback if repo not set (shouldn't happen in production wiring)
    WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "User repository not configured"))
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

    if s.authRepo == nil {
        WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Auth repository not configured"))
        return
    }

    // Validate existing token
    existing, err := s.authRepo.GetRefreshToken(r.Context(), req.RefreshToken)
    if err != nil {
        WriteError(w, http.StatusUnauthorized, domain.ErrInvalidToken)
        return
    }

    // Rotate: revoke old and issue new
    _ = s.authRepo.RevokeRefreshToken(r.Context(), req.RefreshToken)

    newRefresh := uuid.NewString()
    rt := &usecase.RefreshToken{
        ID:        uuid.NewString(),
        UserID:    existing.UserID,
        Token:     newRefresh,
        ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
        CreatedAt: time.Now(),
    }
    if err := s.authRepo.CreateRefreshToken(r.Context(), rt); err != nil {
        WriteError(w, http.StatusInternalServerError, domain.NewDomainError("TOKEN_ISSUE_FAILED", "Failed to issue refresh token"))
        return
    }

    response := generated.TokenResponse{
        AccessToken:  generateJWT(existing.UserID, 15*time.Minute),
        RefreshToken: newRefresh,
        ExpiresIn:    15 * 60,
    }

    WriteJSON(w, http.StatusOK, response)
}

// Logout implements ServerInterface.Logout
func (s *Server) Logout(w http.ResponseWriter, r *http.Request) {
    userID := r.Context().Value(middleware.UserIDKey).(string)

    if s.authRepo != nil {
        _ = s.authRepo.RevokeAllUserTokens(r.Context(), userID)
        _ = s.authRepo.DeleteAllUserSessions(r.Context(), userID)
    }

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
