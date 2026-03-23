package config

import (
	"encoding/json"
	"net/http"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
)

// ClientConfigHandlers handles client configuration endpoints.
type ClientConfigHandlers struct{}

// NewClientConfigHandlers returns a new ClientConfigHandlers.
func NewClientConfigHandlers() *ClientConfigHandlers {
	return &ClientConfigHandlers{}
}

type updateLanguageRequest struct {
	Language string `json:"language"`
}

// UpdateInterfaceLanguage handles POST /api/v1/client-config/update-interface-language.
func (h *ClientConfigHandlers) UpdateInterfaceLanguage(w http.ResponseWriter, r *http.Request) {
	var req updateLanguageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	if req.Language == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Language is required"))
		return
	}

	// Validate language code format (basic BCP 47 check)
	if len(req.Language) < 2 || len(req.Language) > 10 {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid language code"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"language": req.Language,
	})
}
