package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"athena/internal/domain"
	"athena/internal/usecase"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// OAuthToken handles POST /oauth/token supporting password and refresh_token grants.
// Content-Type: application/x-www-form-urlencoded per RFC 6749.
func (s *Server) OAuthToken(w http.ResponseWriter, r *http.Request) {
	// Ensure form is parsed
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Invalid form data")
		return
	}

	grantType := r.FormValue("grant_type")
	if grantType == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Missing grant_type")
		return
	}

	// Authenticate client (Basic auth or form fields)
	clientID, clientSecret, ok := parseClientAuth(r)
	if !ok {
		unauthorizedOAuth(w, "invalid_client", "Client authentication required")
		return
	}

	// Lookup client via configured repository
	oauthRepo := s.oauthRepo
	if oauthRepo == nil {
		writeOAuthError(w, http.StatusNotImplemented, "server_error", "OAuth client repository not configured")
		return
	}

	client, err := oauthRepo.GetClientByClientID(r.Context(), clientID)
	if err != nil {
		unauthorizedOAuth(w, "invalid_client", "Unknown client")
		return
	}
	if client.IsConfidential {
		if client.ClientSecretHash == nil || *client.ClientSecretHash == "" {
			unauthorizedOAuth(w, "invalid_client", "Client misconfigured")
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(*client.ClientSecretHash), []byte(clientSecret)) != nil {
			unauthorizedOAuth(w, "invalid_client", "Bad client credentials")
			return
		}
	}

	// Verify grant type is allowed for this client
	allowed := false
	for _, g := range client.GrantTypes {
		if strings.EqualFold(g, grantType) {
			allowed = true
			break
		}
	}
	if !allowed {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "Grant not allowed for client")
		return
	}

	switch grantType {
	case "password":
		s.handlePasswordGrant(w, r)
		return
	case "refresh_token":
		s.handleRefreshTokenGrant(w, r)
		return
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "Unsupported grant_type")
		return
	}
}

// handlePasswordGrant issues tokens using resource owner password credentials.
func (s *Server) handlePasswordGrant(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")
	if username == "" || password == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "username and password are required")
		return
	}

	// Authenticate user by email or username
	var dUser *domain.User
	var err error
	if strings.Contains(username, "@") {
		dUser, err = s.userRepo.GetByEmail(r.Context(), username)
	} else {
		dUser, err = s.userRepo.GetByUsername(r.Context(), username)
		if err != nil {
			// Fallback to email if not found
			dUser, err = s.userRepo.GetByEmail(r.Context(), username)
		}
	}
	if err != nil || dUser == nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_grant", "Invalid credentials")
		return
	}
	hash, err := s.userRepo.GetPasswordHash(r.Context(), dUser.ID)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_grant", "Invalid credentials")
		return
	}

	// Issue tokens (reuse JWT + refresh storage)
	access := s.generateJWTWithRole(dUser.ID, string(dUser.Role), 15*time.Minute)
	refresh := uuid.NewString()
	if refresh == "" {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to issue token")
		return
	}
	refreshExpires := time.Now().Add(7 * 24 * time.Hour)
	if s.authRepo != nil {
		rt := &usecase.RefreshToken{ID: uuid.NewString(), UserID: dUser.ID, Token: refresh, ExpiresAt: refreshExpires, CreatedAt: time.Now()}
		if err := s.authRepo.CreateRefreshToken(r.Context(), rt); err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to persist refresh token")
			return
		}
		if err := s.authRepo.CreateSession(r.Context(), refresh, dUser.ID, refreshExpires); err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to persist session")
			return
		}
	}

	// OAuth recommended: prevent caching
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	resp := map[string]interface{}{
		"access_token":  access,
		"token_type":    "bearer",
		"expires_in":    15 * 60,
		"refresh_token": refresh,
		// Optional scope echo-back; omitted for now
	}
	WriteJSON(w, http.StatusOK, resp)
}

// handleRefreshTokenGrant rotates refresh and returns new access token.
func (s *Server) handleRefreshTokenGrant(w http.ResponseWriter, r *http.Request) {
	rt := r.FormValue("refresh_token")
	if rt == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Missing refresh_token")
		return
	}
	if s.authRepo == nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Auth repository not configured")
		return
	}
	existing, err := s.authRepo.GetRefreshToken(r.Context(), rt)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_grant", "Invalid or expired refresh_token")
		return
	}
	_ = s.authRepo.RevokeRefreshToken(r.Context(), rt)

	newRefresh := uuid.NewString()
	refreshExpires := time.Now().Add(7 * 24 * time.Hour)
	if err := s.authRepo.CreateRefreshToken(r.Context(), &usecase.RefreshToken{ID: uuid.NewString(), UserID: existing.UserID, Token: newRefresh, ExpiresAt: refreshExpires, CreatedAt: time.Now()}); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to issue refresh token")
		return
	}
	_ = s.authRepo.DeleteSession(r.Context(), rt)
	if err := s.authRepo.CreateSession(r.Context(), newRefresh, existing.UserID, refreshExpires); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to create session")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	// Fetch user role for token
	var role string
	if s.userRepo != nil {
		if u, err := s.userRepo.GetByID(r.Context(), existing.UserID); err == nil {
			role = string(u.Role)
		}
	}
	resp := map[string]interface{}{
		"access_token":  s.generateJWTWithRole(existing.UserID, role, 15*time.Minute),
		"token_type":    "bearer",
		"expires_in":    15 * 60,
		"refresh_token": newRefresh,
	}
	WriteJSON(w, http.StatusOK, resp)
}

func parseClientAuth(r *http.Request) (clientID, clientSecret string, ok bool) {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(auth), "basic ") {
		b64 := strings.TrimSpace(auth[6:])
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err == nil {
			parts := strings.SplitN(string(raw), ":", 2)
			if len(parts) == 2 {
				return parts[0], parts[1], true
			}
		}
	}
	// Fallback to form
	cid := r.FormValue("client_id")
	csec := r.FormValue("client_secret")
	if cid != "" {
		return cid, csec, true
	}
	return "", "", false
}

func unauthorizedOAuth(w http.ResponseWriter, code, desc string) {
	w.Header().Set("WWW-Authenticate", "Basic realm=\"oauth\", error=\""+code+"\", error_description=\""+desc+"\"")
	writeOAuthError(w, http.StatusUnauthorized, code, desc)
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": desc,
	})
}

// no-op
