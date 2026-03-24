package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/mail"
	"time"
	"unicode"
	"vidra-core/internal/httpapi/shared"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	oapitypes "github.com/oapi-codegen/runtime/types"
	redis "github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/generated"
	"vidra-core/internal/middleware"
	"vidra-core/internal/usecase"
)

type Server struct {
	userRepo            usecase.UserRepository
	authRepo            usecase.AuthRepository
	oauthRepo           usecase.OAuthRepository
	verificationService *usecase.EmailVerificationService
	twoFAService        *usecase.TwoFAService
	jwtSecret           string
	redis               *redis.Client
	redisPingTimeout    time.Duration
	ipfsAPI             string
	ipfsClusterAPI      string
	ipfsPingTimeout     time.Duration
	cfg                 *config.Config
	db                  *sql.DB
}

func NewServer(userRepo usecase.UserRepository, authRepo usecase.AuthRepository, jwtSecret string, redisClient *redis.Client, redisPingTimeout time.Duration, ipfsAPI string, ipfsClusterAPI string, ipfsPingTimeout time.Duration, cfg *config.Config) *Server {
	return &Server{
		userRepo:         userRepo,
		authRepo:         authRepo,
		oauthRepo:        nil,
		jwtSecret:        jwtSecret,
		redis:            redisClient,
		redisPingTimeout: redisPingTimeout,
		ipfsAPI:          ipfsAPI,
		ipfsClusterAPI:   ipfsClusterAPI,
		ipfsPingTimeout:  ipfsPingTimeout,
		cfg:              cfg,
	}
}

func NewServerWithOAuth(userRepo usecase.UserRepository, authRepo usecase.AuthRepository, oauthRepo usecase.OAuthRepository, jwtSecret string, redisClient *redis.Client, redisPingTimeout time.Duration, ipfsAPI string, ipfsClusterAPI string, ipfsPingTimeout time.Duration, cfg *config.Config) *Server {
	s := NewServer(userRepo, authRepo, jwtSecret, redisClient, redisPingTimeout, ipfsAPI, ipfsClusterAPI, ipfsPingTimeout, cfg)
	s.oauthRepo = oauthRepo
	return s
}

func (s *Server) SetVerificationService(service *usecase.EmailVerificationService) {
	s.verificationService = service
}

func (s *Server) SetTwoFAService(service *usecase.TwoFAService) {
	s.twoFAService = service
}

func (s *Server) SetDB(db *sql.DB) {
	s.db = db
}

func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var reqData map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&reqData); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	email, _ := reqData["email"].(string)
	username, _ := reqData["username"].(string)
	password, _ := reqData["password"].(string)
	twoFACode, _ := reqData["twofa_code"].(string)

	if (email == "" && username == "") || password == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CREDENTIALS", "Email or username, and password are required"))
		return
	}

	var dUser *domain.User
	var err error
	if email != "" {
		dUser, err = s.userRepo.GetByEmail(r.Context(), email)
	} else {
		dUser, err = s.userRepo.GetByUsername(r.Context(), username)
	}
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrInvalidCredentials)
		return
	}
	hash, err := s.userRepo.GetPasswordHash(r.Context(), dUser.ID)
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrInvalidCredentials)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrInvalidCredentials)
		return
	}

	if !dUser.IsActive {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("ACCOUNT_DEACTIVATED", "account deactivated"))
		return
	}

	if dUser.TwoFAEnabled {
		if twoFACode == "" {
			shared.WriteError(w, http.StatusForbidden, domain.ErrTwoFARequired)
			return
		}

		if s.twoFAService != nil {
			if err := s.twoFAService.VerifyCode(r.Context(), dUser.ID, twoFACode); err != nil {
				shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("INVALID_TWOFA_CODE", "Invalid two-factor authentication code"))
				return
			}
		} else {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Two-factor authentication service not available"))
			return
		}
	}

	access := s.generateJWTWithRole(dUser.ID, string(dUser.Role), 15*time.Minute)
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
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainErrorWithDetails("TOKEN_ISSUE_FAILED", "Failed to create refresh token", err.Error()))
			return
		}

		if err := s.authRepo.CreateSession(r.Context(), refresh, dUser.ID, refreshExpires); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SESSION_CREATE_FAILED", "Failed to create session"))
			return
		}
	}

	var dispPtr *string
	if dUser.DisplayName != "" {
		disp := dUser.DisplayName
		dispPtr = &disp
	}
	gUser := generated.User{
		Id:          dUser.ID,
		Username:    dUser.Username,
		Email:       oapitypes.Email(dUser.Email),
		DisplayName: dispPtr,
		Role:        generated.UserRoleUser,
		IsActive:    dUser.IsActive,
		CreatedAt:   dUser.CreatedAt,
		UpdatedAt:   dUser.UpdatedAt,
	}
	if dUser.Avatar != nil {
		gUser.Avatar = &generated.Avatar{
			Id:          stringToUUIDPtr(dUser.Avatar.ID),
			IpfsCid:     nullStringToPtr(dUser.Avatar.IPFSCID),
			WebpIpfsCid: nullStringToPtr(dUser.Avatar.WebPIPFSCID),
		}
	}

	response := generated.AuthResponse{
		User:         gUser,
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    15 * 60,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	var req generated.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.Username == "" || req.Email == "" || req.Password == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FIELDS", "Username, email, and password are required"))
		return
	}

	if _, err := mail.ParseAddress(string(req.Email)); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_EMAIL", "Invalid email format"))
		return
	}

	if err := validatePassword(req.Password); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("WEAK_PASSWORD", err.Error()))
		return
	}

	if s.userRepo != nil {
		if _, err := s.userRepo.GetByEmail(r.Context(), string(req.Email)); err == nil {
			shared.WriteError(w, http.StatusConflict, domain.NewDomainError("USER_EXISTS", "Email already in use"))
			return
		}
		if _, err := s.userRepo.GetByUsername(r.Context(), req.Username); err == nil {
			shared.WriteError(w, http.StatusConflict, domain.NewDomainError("USER_EXISTS", "Username already in use"))
			return
		}

		now := time.Now()
		userID := uuid.NewString()
		displayName := ""
		if req.DisplayName != nil {
			displayName = *req.DisplayName
		}

		dUser := &domain.User{
			ID:          userID,
			Username:    req.Username,
			Email:       string(req.Email),
			DisplayName: displayName,
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to process password"))
			return
		}

		if err := s.userRepo.Create(r.Context(), dUser, string(hash)); err != nil {
			status := shared.MapDomainErrorToHTTP(domain.ErrConflict)
			shared.WriteError(w, status, domain.NewDomainError("CREATE_FAILED", "Failed to create user"))
			return
		}

		var dispPtr *string
		if dUser.DisplayName != "" {
			disp := dUser.DisplayName
			dispPtr = &disp
		}
		gUser := generated.User{
			Id:          dUser.ID,
			Username:    dUser.Username,
			Email:       oapitypes.Email(dUser.Email),
			DisplayName: dispPtr,
			Role:        generated.UserRoleUser,
			IsActive:    dUser.IsActive,
			CreatedAt:   dUser.CreatedAt,
			UpdatedAt:   dUser.UpdatedAt,
		}
		if dUser.Avatar != nil {
			gUser.Avatar = &generated.Avatar{
				Id:          stringToUUIDPtr(dUser.Avatar.ID),
				IpfsCid:     nullStringToPtr(dUser.Avatar.IPFSCID),
				WebpIpfsCid: nullStringToPtr(dUser.Avatar.WebPIPFSCID),
			}
		}

		w.Header().Set("Location", "/api/v1/users/"+gUser.Id)

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
				shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainErrorWithDetails("TOKEN_ISSUE_FAILED", "Failed to create refresh token", err.Error()))
				return
			}
			if err := s.authRepo.CreateSession(r.Context(), refresh, dUser.ID, refreshExpires); err != nil {
				shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SESSION_CREATE_FAILED", "Failed to create session"))
				return
			}
		}

		if s.verificationService != nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_ = s.verificationService.SendVerificationEmail(ctx, dUser.ID)
			}()
		}

		response := generated.AuthResponse{
			User:         gUser,
			AccessToken:  s.generateJWTWithRole(gUser.Id, string(dUser.Role), 15*time.Minute),
			RefreshToken: refresh,
			ExpiresIn:    15 * 60,
		}

		shared.WriteJSON(w, http.StatusCreated, response)
		return
	}

	shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "User repository not configured"))
}

func (s *Server) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req generated.RefreshTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.RefreshToken == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_TOKEN", "Refresh token is required"))
		return
	}

	if s.authRepo == nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Auth repository not configured"))
		return
	}

	existing, err := s.authRepo.GetRefreshToken(r.Context(), req.RefreshToken)
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrInvalidToken)
		return
	}

	var role string
	if s.userRepo != nil {
		u, err := s.userRepo.GetByID(r.Context(), existing.UserID)
		if err == nil {
			if !u.IsActive {
				_ = s.authRepo.RevokeRefreshToken(r.Context(), req.RefreshToken)
				shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("ACCOUNT_DEACTIVATED", "account deactivated"))
				return
			}
			role = string(u.Role)
		}
	}

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
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainErrorWithDetails("TOKEN_ISSUE_FAILED", "Failed to issue refresh token", err.Error()))
		return
	}

	_ = s.authRepo.DeleteSession(r.Context(), req.RefreshToken)
	if err := s.authRepo.CreateSession(r.Context(), newRefresh, existing.UserID, refreshExpires); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SESSION_CREATE_FAILED", "Failed to create session"))
		return
	}
	response := generated.TokenResponse{
		AccessToken:  s.generateJWTWithRole(existing.UserID, role, 15*time.Minute),
		RefreshToken: newRefresh,
		ExpiresIn:    15 * 60,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

func (s *Server) Logout(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(middleware.UserIDKey).(string)

	if s.authRepo != nil {
		_ = s.authRepo.RevokeAllUserTokens(r.Context(), userID)
		_ = s.authRepo.DeleteAllUserSessions(r.Context(), userID)
	}

	response := generated.LogoutResponse{
		Message: "Logged out successfully",
		UserId:  &userID,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	response := generated.HealthResponse{
		Status:    generated.HealthResponseStatusHealthy,
		Timestamp: time.Now(),
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

func (s *Server) ReadinessCheck(w http.ResponseWriter, r *http.Request) {
	dbStatus := generated.ReadinessResponseChecksDatabaseHealthy
	if s.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.PingContext(ctx); err != nil {
			dbStatus = generated.ReadinessResponseChecksDatabaseUnhealthy
		}
	}

	ipfsStatus := generated.ReadinessResponseChecksIpfsHealthy
	redisStatus := generated.ReadinessResponseChecksRedisHealthy
	if s.redis != nil {
		to := s.redisPingTimeout
		if to <= 0 {
			to = 3 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), to)
		defer cancel()
		if err := s.redis.Ping(ctx).Err(); err != nil {
			redisStatus = generated.ReadinessResponseChecksRedisUnhealthy
		}
	}

	if s.ipfsAPI != "" {
		to := s.ipfsPingTimeout
		if to <= 0 {
			to = 5 * time.Second
		}
		client := &http.Client{Timeout: to}
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, s.ipfsAPI+"/api/v0/version", nil)
		resp, err := client.Do(req)
		if resp != nil && resp.Body != nil {
			defer resp.Body.Close()
		}
		if err != nil || resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			ipfsStatus = generated.ReadinessResponseChecksIpfsUnhealthy
		}
	}

	overallStatus := generated.Ready
	httpStatus := http.StatusOK
	if dbStatus == generated.ReadinessResponseChecksDatabaseUnhealthy ||
		redisStatus == generated.ReadinessResponseChecksRedisUnhealthy ||
		ipfsStatus == generated.ReadinessResponseChecksIpfsUnhealthy {
		overallStatus = generated.NotReady
		httpStatus = http.StatusServiceUnavailable
	}

	response := generated.ReadinessResponse{
		Status: overallStatus,
		Checks: struct {
			Database *generated.ReadinessResponseChecksDatabase `json:"database,omitempty"`
			Ipfs     *generated.ReadinessResponseChecksIpfs     `json:"ipfs,omitempty"`
			Redis    *generated.ReadinessResponseChecksRedis    `json:"redis,omitempty"`
		}{
			Database: &dbStatus,
			Redis:    &redisStatus,
			Ipfs:     &ipfsStatus,
		},
		Timestamp: time.Now(),
	}

	shared.WriteJSON(w, httpStatus, response)
}

func (s *Server) generateJWTWithRole(userID string, role string, duration time.Duration) string {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": userID,
		"iat": now.Unix(),
		"exp": now.Add(duration).Unix(),
		"jti": uuid.NewString(),
	}
	if role != "" {
		claims["role"] = role
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	sgn, err := token.SignedString([]byte(s.jwtSecret))
	if err != nil {
		return ""
	}
	return sgn
}

func (s *Server) GenerateJWTForTests(userID string, role string, duration time.Duration) string {
	return s.generateJWTWithRole(userID, role, duration)
}

func nullStringToPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

func stringToUUIDPtr(s string) *oapitypes.UUID {
	if s == "" {
		return nil
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &u
}

func validatePassword(password string) error {
	if len(password) < 8 {
		return domain.NewDomainError("WEAK_PASSWORD", "Password must be at least 8 characters")
	}
	var hasUpper, hasLower, hasDigit bool
	for _, ch := range password {
		switch {
		case unicode.IsUpper(ch):
			hasUpper = true
		case unicode.IsLower(ch):
			hasLower = true
		case unicode.IsDigit(ch):
			hasDigit = true
		}
	}
	if !hasUpper {
		return domain.NewDomainError("WEAK_PASSWORD", "Password must contain at least one uppercase letter")
	}
	if !hasLower {
		return domain.NewDomainError("WEAK_PASSWORD", "Password must contain at least one lowercase letter")
	}
	if !hasDigit {
		return domain.NewDomainError("WEAK_PASSWORD", "Password must contain at least one digit")
	}
	return nil
}
