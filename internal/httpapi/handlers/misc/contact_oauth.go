package misc

import (
	"encoding/json"
	"net/http"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
)

type contactFormRequest struct {
	FromName  string `json:"fromName"`
	FromEmail string `json:"fromEmail"`
	Body      string `json:"body"`
}

// ContactFormHandler handles POST /api/v1/server/contact.
// Accepts a contact form submission; in this stub, it validates fields and returns 204.
func ContactFormHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req contactFormRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "invalid request body"))
			return
		}
		if req.FromEmail == "" || req.Body == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "fromEmail and body are required"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// GetOAuthLocalHandler handles GET /api/v1/oauth-clients/local.
// Returns the local OAuth client credentials used for password-grant flows.
func GetOAuthLocalHandler(clientID, clientSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"client_id":     clientID,
			"client_secret": clientSecret,
		})
	}
}
