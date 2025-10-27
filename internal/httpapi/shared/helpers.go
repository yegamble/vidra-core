package shared

import (
	"net/http"
	"strconv"

	"athena/internal/domain"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// GetBoolParam extracts a boolean query parameter with a default value
func GetBoolParam(r *http.Request, key string, defaultValue bool) bool {
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultValue
	}

	b, err := strconv.ParseBool(val)
	if err != nil {
		return defaultValue
	}

	return b
}

// GetIntParam extracts an integer query parameter with a default value
func GetIntParam(r *http.Request, key string, defaultValue int) int {
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultValue
	}

	n, err := strconv.Atoi(val)
	if err != nil {
		return defaultValue
	}

	return n
}

// ParsePagination extracts pagination parameters from the request with backward compatibility
// Supports both page/pageSize (modern) and limit/offset (legacy) parameters
// Returns: page, limit, offset, pageSize
func ParsePagination(r *http.Request, defaultPageSize int) (page, limit, offset, pageSize int) {
	// Parse all parameters
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ = strconv.Atoi(r.URL.Query().Get("pageSize"))
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))

	// Set pageSize with fallbacks
	if pageSize <= 0 || pageSize > 100 {
		if limit > 0 {
			pageSize = limit
		} else {
			pageSize = defaultPageSize
		}
	}

	// Calculate page from offset if not provided
	if page <= 0 {
		if offset < 0 {
			offset = 0
		}
		page = (offset / pageSize) + 1
		if page <= 0 {
			page = 1
		}
	}

	// Compute final limit/offset from page
	limit = pageSize
	offset = (page - 1) * pageSize

	return page, limit, offset, pageSize
}

// RequireUUIDParam extracts a path parameter and validates it as a UUID.
// On failure it writes an error response and returns ok=false.
func RequireUUIDParam(w http.ResponseWriter, r *http.Request, param, missingCode, invalidCode, missingMsg, invalidMsg string) (string, bool) {
	id := chi.URLParam(r, param)
	if id == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError(missingCode, missingMsg))
		return "", false
	}
	if _, err := uuid.Parse(id); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError(invalidCode, invalidMsg))
		return "", false
	}
	return id, true
}
