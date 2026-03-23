package moderation

import (
	"context"
	"net/http"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
)

// BlocklistStatusRepository defines the blocklist query needed for status endpoint.
type BlocklistStatusRepository interface {
	ListBlocklistEntries(ctx context.Context, blockType string, activeOnly bool, limit, offset int) ([]*domain.BlocklistEntry, int64, error)
}

// BlocklistStatusHandler handles GET /api/v1/blocklist/status.
// Returns a summary of the instance blocklist split by accounts and servers.
func BlocklistStatusHandler(repo BlocklistStatusRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
			return
		}

		// Fetch user-type blocks (accounts)
		accounts, _, err := repo.ListBlocklistEntries(r.Context(), string(domain.BlockTypeUser), true, 100, 0)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load blocklist"))
			return
		}

		// Fetch domain-type blocks (servers)
		servers, _, err := repo.ListBlocklistEntries(r.Context(), string(domain.BlockTypeDomain), true, 100, 0)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load blocklist"))
			return
		}

		if accounts == nil {
			accounts = []*domain.BlocklistEntry{}
		}
		if servers == nil {
			servers = []*domain.BlocklistEntry{}
		}

		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"accounts": accounts,
			"servers":  servers,
		})
	}
}
