package httpapi

import (
    "net/http"

    "athena/internal/domain"
    "athena/internal/metrics"
    "athena/internal/middleware"
    "athena/internal/usecase"
)

// MetricsHandlerAdmin wraps the metrics handler and restricts access to admin users.
// Requires Auth middleware to set the user ID in context.
func MetricsHandlerAdmin(userRepo usecase.UserRepository) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        v := r.Context().Value(middleware.UserIDKey)
        userID, _ := v.(string)
        if userID == "" {
            WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing user context"))
            return
        }
        u, err := userRepo.GetByID(r.Context(), userID)
        if err != nil || u == nil {
            WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "User not found or unauthorized"))
            return
        }
        if u.Role != domain.RoleAdmin {
            WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Admin access required"))
            return
        }
        // Delegate to metrics handler
        metrics.Handler(w, r)
    }
}

