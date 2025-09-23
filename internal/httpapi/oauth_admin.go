package httpapi

import (
	"encoding/json"
	"net/http"

	"athena/internal/domain"
	"athena/internal/usecase"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type createOAuthClientRequest struct {
	ClientID       string   `json:"client_id"`
	ClientSecret   string   `json:"client_secret"`
	Name           string   `json:"name"`
	GrantTypes     []string `json:"grant_types"`
	Scopes         []string `json:"scopes"`
	RedirectURIs   []string `json:"redirect_uris"`
	IsConfidential *bool    `json:"is_confidential"`
}

type rotateOAuthClientSecretRequest struct {
	ClientSecret   string `json:"client_secret"`
	IsConfidential *bool  `json:"is_confidential"`
}

// AdminListOAuthClients lists all OAuth clients (admin only)
func (s *Server) AdminListOAuthClients(w http.ResponseWriter, r *http.Request) {
	if s.oauthRepo == nil {
		WriteError(w, http.StatusNotImplemented, domain.NewDomainError("NOT_IMPLEMENTED", "OAuth repository not configured"))
		return
	}
	clients, err := s.oauthRepo.ListClients(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list clients"))
		return
	}
	// Redact secret hash in response
	type clientView struct {
		ID             string   `json:"id"`
		ClientID       string   `json:"client_id"`
		Name           string   `json:"name"`
		GrantTypes     []string `json:"grant_types"`
		Scopes         []string `json:"scopes"`
		RedirectURIs   []string `json:"redirect_uris"`
		IsConfidential bool     `json:"is_confidential"`
		CreatedAt      *string  `json:"created_at,omitempty"`
		UpdatedAt      *string  `json:"updated_at,omitempty"`
	}
	out := make([]clientView, 0, len(clients))
	for _, c := range clients {
		out = append(out, clientView{
			ID:             c.ID,
			ClientID:       c.ClientID,
			Name:           c.Name,
			GrantTypes:     c.GrantTypes,
			Scopes:         c.Scopes,
			RedirectURIs:   c.RedirectURIs,
			IsConfidential: c.IsConfidential,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"data":    out,
		"success": true,
	})
}

// AdminCreateOAuthClient registers a new OAuth client (admin only)
func (s *Server) AdminCreateOAuthClient(w http.ResponseWriter, r *http.Request) {
	if s.oauthRepo == nil {
		WriteError(w, http.StatusNotImplemented, domain.NewDomainError("NOT_IMPLEMENTED", "OAuth repository not configured"))
		return
	}
	var req createOAuthClientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid JSON"))
		return
	}
	if req.ClientID == "" || req.Name == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "client_id and name are required"))
		return
	}
	isConf := true
	if req.IsConfidential != nil {
		isConf = *req.IsConfidential
	}
	var hashPtr *string
	if isConf {
		if req.ClientSecret == "" {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "client_secret required for confidential clients"))
			return
		}
		h, err := bcrypt.GenerateFromPassword([]byte(req.ClientSecret), bcrypt.DefaultCost)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "failed to hash secret"))
			return
		}
		s := string(h)
		hashPtr = &s
	}
	if len(req.GrantTypes) == 0 {
		req.GrantTypes = []string{"password", "refresh_token"}
	}
	if len(req.Scopes) == 0 {
		req.Scopes = []string{"basic"}
	}
	if req.RedirectURIs == nil {
		req.RedirectURIs = []string{}
	}
	c := &usecase.OAuthClient{
		ID:               uuid.NewString(),
		ClientID:         req.ClientID,
		ClientSecretHash: hashPtr,
		Name:             req.Name,
		GrantTypes:       req.GrantTypes,
		Scopes:           req.Scopes,
		RedirectURIs:     req.RedirectURIs,
		IsConfidential:   isConf,
	}
	if err := s.oauthRepo.CreateClient(r.Context(), c); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainErrorWithDetails("BAD_REQUEST", "failed to create client", err.Error()))
		return
	}
	// Return without secret
	WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"data": map[string]interface{}{
			"id":              c.ID,
			"client_id":       c.ClientID,
			"name":            c.Name,
			"grant_types":     c.GrantTypes,
			"scopes":          c.Scopes,
			"redirect_uris":   c.RedirectURIs,
			"is_confidential": c.IsConfidential,
		},
		"success": true,
	})
}

// AdminRotateOAuthClientSecret rotates or clears a client's secret (admin only)
// Path param: {clientId}
func (s *Server) AdminRotateOAuthClientSecret(w http.ResponseWriter, r *http.Request) {
	if s.oauthRepo == nil {
		WriteError(w, http.StatusNotImplemented, domain.NewDomainError("NOT_IMPLEMENTED", "OAuth repository not configured"))
		return
	}
	clientID := chi.URLParam(r, "clientId")
	if clientID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing clientId"))
		return
	}
	var req rotateOAuthClientSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid JSON"))
		return
	}
	isConf := true
	if req.IsConfidential != nil {
		isConf = *req.IsConfidential
	}
	var hashPtr *string
	if isConf {
		if req.ClientSecret == "" {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "client_secret required for confidential client"))
			return
		}
		h, err := bcrypt.GenerateFromPassword([]byte(req.ClientSecret), bcrypt.DefaultCost)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "failed to hash secret"))
			return
		}
		s := string(h)
		hashPtr = &s
	} else {
		// public client: clear secret
		hashPtr = nil
	}
	if err := s.oauthRepo.UpdateClientSecret(r.Context(), clientID, hashPtr, isConf); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainErrorWithDetails("BAD_REQUEST", "failed to update client", err.Error()))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"message": "client secret updated"})
}

// AdminDeleteOAuthClient deletes a client by client_id (admin only)
func (s *Server) AdminDeleteOAuthClient(w http.ResponseWriter, r *http.Request) {
	if s.oauthRepo == nil {
		WriteError(w, http.StatusNotImplemented, domain.NewDomainError("NOT_IMPLEMENTED", "OAuth repository not configured"))
		return
	}
	clientID := chi.URLParam(r, "clientId")
	if clientID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing clientId"))
		return
	}
	if err := s.oauthRepo.DeleteClient(r.Context(), clientID); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainErrorWithDetails("BAD_REQUEST", "failed to delete client", err.Error()))
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"message": "client deleted"})
}
