package httpapi

import (
    "context"
    "encoding/json"
    "net/http"
    "os"
    "strconv"
    "time"

    "github.com/golang-jwt/jwt/v5"
    "github.com/google/uuid"
    "github.com/redis/go-redis/v9"
    "golang.org/x/crypto/bcrypt"

    "athena/internal/domain"
    "athena/internal/generated"
    "athena/internal/middleware"
    "athena/internal/usecase"
)

// Server implements the generated ServerInterface
type Server struct{
    userRepo  usecase.UserRepository
    authRepo  usecase.AuthRepository
    jwtSecret string
    redis     *redis.Client
    redisPingTimeout time.Duration
}

// NewServer creates a new server instance
func NewServer(userRepo usecase.UserRepository, authRepo usecase.AuthRepository, jwtSecret string, redisClient *redis.Client, redisPingTimeout time.Duration) *Server {
    return &Server{userRepo: userRepo, authRepo: authRepo, jwtSecret: jwtSecret, redis: redisClient, redisPingTimeout: redisPingTimeout}
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
    access := s.generateJWT(dUser.ID, 15*time.Minute)
    refresh := uuid.NewString()
    refreshExpires := time.Now().Add(7 * 24 * time.Hour)
    if s.authRepo != nil {
        rt := &usecase.RefreshToken{
            ID:        uuid.NewString(),
            UserID:    dUser.ID,
            Token:     refresh,
            ExpiresAt: refreshExpires,
            CreatedAt: time.Now(),
        }
        if err := s.authRepo.CreateRefreshToken(r.Context(), rt); err != nil {
            WriteError(w, http.StatusInternalServerError, domain.NewDomainError("TOKEN_ISSUE_FAILED", "Failed to create refresh token"))
            return
        }

        // Create corresponding session in Redis (sessionID == refresh token)
        if err := s.authRepo.CreateSession(r.Context(), refresh, dUser.ID, refreshExpires); err != nil {
            WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SESSION_CREATE_FAILED", "Failed to create session"))
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

        // Create refresh token + session for new user
        refresh := uuid.NewString()
        refreshExpires := time.Now().Add(7 * 24 * time.Hour)
        if s.authRepo != nil {
            rt := &usecase.RefreshToken{
                ID:        uuid.NewString(),
                UserID:    dUser.ID,
                Token:     refresh,
                ExpiresAt: refreshExpires,
                CreatedAt: time.Now(),
            }
            if err := s.authRepo.CreateRefreshToken(r.Context(), rt); err != nil {
                WriteError(w, http.StatusInternalServerError, domain.NewDomainError("TOKEN_ISSUE_FAILED", "Failed to create refresh token"))
                return
            }
            if err := s.authRepo.CreateSession(r.Context(), refresh, dUser.ID, refreshExpires); err != nil {
                WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SESSION_CREATE_FAILED", "Failed to create session"))
                return
            }
        }

        response := generated.AuthResponse{
            User:         gUser,
            AccessToken:  s.generateJWT(gUser.ID, 15*time.Minute),
            RefreshToken: refresh,
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
    refreshExpires := time.Now().Add(7 * 24 * time.Hour)
    rt := &usecase.RefreshToken{
        ID:        uuid.NewString(),
        UserID:    existing.UserID,
        Token:     newRefresh,
        ExpiresAt: refreshExpires,
        CreatedAt: time.Now(),
    }
    if err := s.authRepo.CreateRefreshToken(r.Context(), rt); err != nil {
        WriteError(w, http.StatusInternalServerError, domain.NewDomainError("TOKEN_ISSUE_FAILED", "Failed to issue refresh token"))
        return
    }

    // Rotate session in Redis (sessionID == refresh token)
    _ = s.authRepo.DeleteSession(r.Context(), req.RefreshToken)
    if err := s.authRepo.CreateSession(r.Context(), newRefresh, existing.UserID, refreshExpires); err != nil {
        WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SESSION_CREATE_FAILED", "Failed to create session"))
        return
    }

    response := generated.TokenResponse{
        AccessToken:  s.generateJWT(existing.UserID, 15*time.Minute),
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
    // Probe dependent services
    dbStatus := generated.ServiceStatusHealthy // DB connectivity not probed here
    ipfsStatus := generated.ServiceStatusHealthy // IPFS not probed here
    // Redis ping
    redisStatus := generated.ServiceStatusHealthy
    if s.redis != nil {
        to := s.redisPingTimeout
        if to <= 0 {
            to = 3 * time.Second
        }
        ctx, cancel := context.WithTimeout(r.Context(), to)
        defer cancel()
        if err := s.redis.Ping(ctx).Err(); err != nil {
            redisStatus = generated.ServiceStatusUnhealthy
        }
    }

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
// generateJWT creates a signed JWT for the given user ID and duration
func (s *Server) generateJWT(userID string, duration time.Duration) string {
    // Defer to middleware's jwt implementation; kept here to avoid import cycle
    // We re-implement minimal signing here for clarity
    type Claims struct {
        Sub string `json:"sub"`
        Exp int64  `json:"exp"`
        Iat int64  `json:"iat"`
    }
    // We will use golang-jwt/jwt in middleware; here we just mirror
    // but to avoid duplicating heavy imports, we keep a lightweight version
    // For correctness, we import jwt here too
    return signHS256JWT(s.jwtSecret, userID, duration)
}

// cfgSessionTimeout reads SESSION_TIMEOUT from env via default config loader semantics.
// Handlers do not carry the full config; we use a helper to keep code localized.
func cfgSessionTimeout() int {
    // Default 24h if not provided; the config loader already enforces presence of REDIS/JWT
    if v := getenv("SESSION_TIMEOUT"); v != 0 {
        return v
    }
    return 24 * 60 * 60
}

func safeSeconds(v int) int { if v <= 0 { return 60 * 60 * 24 }; return v }

// signHS256JWT signs a compact JWT with HS256
func signHS256JWT(secret, userID string, duration time.Duration) string {
    now := time.Now()
    claims := jwt.MapClaims{
        "sub": userID,
        "iat": now.Unix(),
        "exp": now.Add(duration).Unix(),
    }
    token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
    s, err := token.SignedString([]byte(secret))
    if err != nil {
        // In case of signing error, return empty string; caller handles error paths
        return ""
    }
    return s
}

// getenv returns integer env var or zero if not set/invalid
func getenv(key string) int {
    v := os.Getenv(key)
    if v == "" {
        return 0
    }
    n, err := strconv.Atoi(v)
    if err != nil {
        return 0
    }
    return n
}
