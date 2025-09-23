package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"
)

// GetCurrentUserHandler returns the current user using the repository
func GetCurrentUserHandler(repo usecase.UserRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		user, err := repo.GetByID(r.Context(), userID)
		if err != nil {
			if err == domain.ErrUserNotFound {
				WriteError(w, http.StatusNotFound, domain.NewDomainError("USER_NOT_FOUND", "User not found"))
				return
			}
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load user"))
			return
		}

		WriteJSON(w, http.StatusOK, user)
	}
}

// UpdateCurrentUserHandler updates the current user using the repository
func UpdateCurrentUserHandler(repo usecase.UserRepository) http.HandlerFunc {
	type updateRequest struct {
		DisplayName   string `json:"display_name"`
		Bio           string `json:"bio"`
		BitcoinWallet string `json:"bitcoin_wallet"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		var req updateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
			return
		}

		user, err := repo.GetByID(r.Context(), userID)
		if err != nil {
			if err == domain.ErrUserNotFound {
				WriteError(w, http.StatusNotFound, domain.NewDomainError("USER_NOT_FOUND", "User not found"))
				return
			}
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load user"))
			return
		}

		// Update allowed fields
		if req.DisplayName != "" {
			user.DisplayName = req.DisplayName
		}
		if req.Bio != "" {
			user.Bio = req.Bio
		}
		if req.BitcoinWallet != "" {
			user.BitcoinWallet = req.BitcoinWallet
		}
		user.UpdatedAt = time.Now()

		if err := repo.Update(r.Context(), user); err != nil {
			if err == domain.ErrUserNotFound {
				WriteError(w, http.StatusNotFound, domain.NewDomainError("USER_NOT_FOUND", "User not found"))
				return
			}
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update user"))
			return
		}

		WriteJSON(w, http.StatusOK, user)
	}
}

// GetUserHandler returns a public user by ID using the repository
func GetUserHandler(repo usecase.UserRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := requireUUIDParam(w, r, "id", "MISSING_USER_ID", "INVALID_USER_ID", "User ID is required", "Invalid user ID format")
		if !ok {
			return
		}

		user, err := repo.GetByID(r.Context(), userID)
		if err != nil {
			if err == domain.ErrUserNotFound {
				WriteError(w, http.StatusNotFound, domain.NewDomainError("USER_NOT_FOUND", "User not found"))
				return
			}
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load user"))
			return
		}

		WriteJSON(w, http.StatusOK, user)
	}
}

// GetUserVideos remains a stub until video repository is implemented
func GetUserVideos(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUUIDParam(w, r, "id", "MISSING_USER_ID", "INVALID_USER_ID", "User ID is required", "Invalid user ID format")
	if !ok {
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 || limit > 100 {
		limit = 20
	}

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	videos := []domain.Video{
		{
			ID:          "1",
			Title:       "User Video 1",
			Description: "This is a video by the user",
			Duration:    300,
			Views:       500,
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
			UploadDate:  time.Now().AddDate(0, 0, -5),
			UserID:      userID,
			Tags:        []string{"user", "content"},
			CategoryID:  nil,
			Language:    "en",
		},
		{
			ID:          "2",
			Title:       "User Video 2",
			Description: "Another video by the user",
			Duration:    180,
			Views:       250,
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
			UploadDate:  time.Now().AddDate(0, 0, -10),
			UserID:      userID,
			Tags:        []string{"tutorial"},
			CategoryID:  nil,
			Language:    "en",
		},
	}

	meta := &Meta{
		Total:  int64(len(videos)),
		Limit:  limit,
		Offset: offset,
	}

	WriteJSONWithMeta(w, http.StatusOK, videos, meta)
}

// CreateUserHandler creates a new user in the database
func CreateUserHandler(repo usecase.UserRepository) http.HandlerFunc {
	type createUserRequest struct {
		Username      string          `json:"username"`
		Email         string          `json:"email"`
		Password      string          `json:"password"`
		DisplayName   string          `json:"display_name"`
		Bio           string          `json:"bio"`
		BitcoinWallet string          `json:"bitcoin_wallet"`
		Role          domain.UserRole `json:"role"`
		IsActive      *bool           `json:"is_active"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req createUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
			return
		}

		if req.Username == "" || req.Email == "" || req.Password == "" {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FIELDS", "Username, email, and password are required"))
			return
		}

		// Optional: check for duplicates for clearer 409s
		if _, err := repo.GetByEmail(r.Context(), req.Email); err == nil {
			WriteError(w, http.StatusConflict, domain.NewDomainError("USER_EXISTS", "Email already in use"))
			return
		}
		if _, err := repo.GetByUsername(r.Context(), req.Username); err == nil {
			WriteError(w, http.StatusConflict, domain.NewDomainError("USER_EXISTS", "Username already in use"))
			return
		}

		now := time.Now()
		id := uuid.NewString()
		role := req.Role
		if role == "" {
			role = domain.RoleUser
		}
		isActive := true
		if req.IsActive != nil {
			isActive = *req.IsActive
		}

		user := &domain.User{
			ID:            id,
			Username:      req.Username,
			Email:         req.Email,
			DisplayName:   req.DisplayName,
			Bio:           req.Bio,
			BitcoinWallet: req.BitcoinWallet,
			Role:          role,
			IsActive:      isActive,
			CreatedAt:     now,
			UpdatedAt:     now,
		}

		// Hash password
		pwHashBytes, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to process password"))
			return
		}

		if err := repo.Create(r.Context(), user, string(pwHashBytes)); err != nil {
			// Fallback conflict mapping if repo enforces uniqueness at DB level
			WriteError(w, MapDomainErrorToHTTP(domain.ErrConflict), domain.NewDomainError("CREATE_FAILED", "Failed to create user"))
			return
		}

		// Set Location header to new resource
		w.Header().Set("Location", "/api/v1/users/"+user.ID)
		WriteJSON(w, http.StatusCreated, user)
	}
}
