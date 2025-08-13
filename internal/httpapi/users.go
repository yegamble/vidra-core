package httpapi

import (
    "encoding/json"
    "net/http"
    "strconv"
    "time"

    "github.com/go-chi/chi/v5"

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
        DisplayName string `json:"display_name"`
        Bio         string `json:"bio"`
        Avatar      string `json:"avatar"`
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
        if req.Avatar != "" {
            user.Avatar = req.Avatar
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
        userID := chi.URLParam(r, "id")
        if userID == "" {
            WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_USER_ID", "User ID is required"))
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
    userID := chi.URLParam(r, "id")
    if userID == "" {
        WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_USER_ID", "User ID is required"))
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
            Category:    "personal",
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
            Category:    "education",
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
