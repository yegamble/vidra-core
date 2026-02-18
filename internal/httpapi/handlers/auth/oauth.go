package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/usecase"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// OAuthToken handles POST /oauth/token supporting password and refresh_token grants.
// Content-Type: application/x-www-form-urlencoded per RFC 6749.
func (h *AuthHandlers) OAuthToken(w http.ResponseWriter, r *http.Request) {
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
	oauthRepo := h.oauthRepo
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
		h.handlePasswordGrant(w, r)
		return
	case "refresh_token":
		h.handleRefreshTokenGrant(w, r)
		return
	case "authorization_code":
		h.handleAuthorizationCodeGrant(w, r)
		return
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "Unsupported grant_type")
		return
	}
}

// handlePasswordGrant issues tokens using resource owner password credentials.
func (h *AuthHandlers) handlePasswordGrant(w http.ResponseWriter, r *http.Request) {
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
		dUser, err = h.userRepo.GetByEmail(r.Context(), username)
	} else {
		dUser, err = h.userRepo.GetByUsername(r.Context(), username)
		if err != nil {
			// Fallback to email if not found
			dUser, err = h.userRepo.GetByEmail(r.Context(), username)
		}
	}
	if err != nil || dUser == nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_grant", "Invalid credentials")
		return
	}
	hash, err := h.userRepo.GetPasswordHash(r.Context(), dUser.ID)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_grant", "Invalid credentials")
		return
	}

	// Issue tokens with default scopes for password grant
	defaultScope := "basic profile email"
	access := h.generateJWTWithRoleAndScope(dUser.ID, string(dUser.Role), defaultScope, 15*time.Minute)
	refresh := uuid.NewString()
	if refresh == "" {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to issue token")
		return
	}
	refreshExpires := time.Now().Add(7 * 24 * time.Hour)
	if h.authRepo != nil {
		rt := &usecase.RefreshToken{ID: uuid.NewString(), UserID: dUser.ID, Token: refresh, ExpiresAt: refreshExpires, CreatedAt: time.Now()}
		if err := h.authRepo.CreateRefreshToken(r.Context(), rt); err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to persist refresh token")
			return
		}
		if err := h.authRepo.CreateSession(r.Context(), refresh, dUser.ID, refreshExpires); err != nil {
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
	shared.WriteJSON(w, http.StatusOK, resp)
}

// handleRefreshTokenGrant rotates refresh and returns new access token.
func (h *AuthHandlers) handleRefreshTokenGrant(w http.ResponseWriter, r *http.Request) {
	rt := r.FormValue("refresh_token")
	if rt == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Missing refresh_token")
		return
	}
	if h.authRepo == nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Auth repository not configured")
		return
	}
	existing, err := h.authRepo.GetRefreshToken(r.Context(), rt)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_grant", "Invalid or expired refresh_token")
		return
	}
	_ = h.authRepo.RevokeRefreshToken(r.Context(), rt)

	newRefresh := uuid.NewString()
	refreshExpires := time.Now().Add(7 * 24 * time.Hour)
	if err := h.authRepo.CreateRefreshToken(r.Context(), &usecase.RefreshToken{ID: uuid.NewString(), UserID: existing.UserID, Token: newRefresh, ExpiresAt: refreshExpires, CreatedAt: time.Now()}); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to issue refresh token")
		return
	}
	_ = h.authRepo.DeleteSession(r.Context(), rt)
	if err := h.authRepo.CreateSession(r.Context(), newRefresh, existing.UserID, refreshExpires); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to create session")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	// Fetch user role for token
	var role string
	if h.userRepo != nil {
		if u, err := h.userRepo.GetByID(r.Context(), existing.UserID); err == nil {
			role = string(u.Role)
		}
	}
	resp := map[string]interface{}{
		"access_token":  h.generateJWTWithRole(existing.UserID, role, 15*time.Minute),
		"token_type":    "bearer",
		"expires_in":    15 * 60,
		"refresh_token": newRefresh,
	}
	shared.WriteJSON(w, http.StatusOK, resp)
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

// GetUserFromContext retrieves user from context (set by auth middleware)
func GetUserFromContext(ctx context.Context) *domain.User {
	userID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		return nil
	}
	return &domain.User{ID: userID.String()}
}

// OAuthAuthorize handles GET/POST /oauth/authorize for Authorization Code flow
func (h *AuthHandlers) OAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.showAuthorizationForm(w, r)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get user from session
	user := GetUserFromContext(r.Context())
	if user == nil {
		// Redirect to login with return URL
		loginURL := "/auth/login?redirect=" + url.QueryEscape(r.URL.String())
		http.Redirect(w, r, loginURL, http.StatusFound)
		return
	}

	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Invalid form data")
		return
	}

	// Parse OAuth parameters
	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")
	responseType := r.FormValue("response_type")
	scope := r.FormValue("scope")
	state := r.FormValue("state")
	codeChallenge := r.FormValue("code_challenge")
	codeChallengeMethod := r.FormValue("code_challenge_method")

	if clientID == "" || redirectURI == "" || responseType != "code" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Missing required parameters")
		return
	}

	// Validate client
	oauthRepo := h.oauthRepo
	if oauthRepo == nil {
		writeOAuthError(w, http.StatusNotImplemented, "server_error", "OAuth not configured")
		return
	}

	client, err := oauthRepo.GetClientByClientID(r.Context(), clientID)
	if err != nil {
		redirectError(w, r, redirectURI, "invalid_client", "Unknown client", state)
		return
	}

	// Validate redirect URI
	if !isValidRedirectURI(redirectURI, client.RedirectURIs) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Invalid redirect_uri")
		return
	}

	// Validate grant type
	if !containsGrantType(client.GrantTypes, "authorization_code") {
		redirectError(w, r, redirectURI, "unauthorized_client", "Client not authorized for this grant", state)
		return
	}

	// Validate scopes
	requestedScopes := parseScopes(scope)
	if !validateScopes(requestedScopes, client.AllowedScopes) {
		redirectError(w, r, redirectURI, "invalid_scope", "Invalid scope requested", state)
		return
	}

	// Check consent (simplified - always approve for now, you can add a consent UI)
	if r.FormValue("approve") != "true" {
		redirectError(w, r, redirectURI, "access_denied", "User denied authorization", state)
		return
	}

	// Generate authorization code
	code := generateSecureToken(32)
	codeRecord := &usecase.OAuthAuthorizationCode{
		ID:                  uuid.NewString(),
		Code:                code,
		ClientID:            clientID,
		UserID:              user.ID,
		RedirectURI:         redirectURI,
		Scope:               scope,
		State:               state,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		ExpiresAt:           time.Now().Add(10 * time.Minute),
		CreatedAt:           time.Now(),
	}

	if err := oauthRepo.CreateAuthorizationCode(r.Context(), codeRecord); err != nil {
		redirectError(w, r, redirectURI, "server_error", "Failed to create code", state)
		return
	}

	// Redirect with code
	u, _ := url.Parse(redirectURI)
	q := u.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// showAuthorizationForm displays a simple authorization form
func (h *AuthHandlers) showAuthorizationForm(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		loginURL := "/auth/login?redirect=" + url.QueryEscape(r.URL.String())
		http.Redirect(w, r, loginURL, http.StatusFound)
		return
	}

	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		http.Error(w, "Missing client_id", http.StatusBadRequest)
		return
	}

	client, err := h.oauthRepo.GetClientByClientID(r.Context(), clientID)
	if err != nil {
		http.Error(w, "Invalid client", http.StatusBadRequest)
		return
	}

	// Simple HTML form
	respHTML := fmt.Sprintf(`
		<!DOCTYPE html>
		<html>
		<head><title>Authorize %s</title></head>
		<body>
			<h1>Authorize %s</h1>
			<p>%s is requesting access to your account.</p>
			<p>Scopes: %s</p>
			<form method="POST">
				<input type="hidden" name="client_id" value="%s">
				<input type="hidden" name="redirect_uri" value="%s">
				<input type="hidden" name="response_type" value="%s">
				<input type="hidden" name="scope" value="%s">
				<input type="hidden" name="state" value="%s">
				<input type="hidden" name="code_challenge" value="%s">
				<input type="hidden" name="code_challenge_method" value="%s">
				<button type="submit" name="approve" value="true">Approve</button>
				<button type="submit" name="approve" value="false">Deny</button>
			</form>
		</body>
		</html>
	`,
		html.EscapeString(client.Name), html.EscapeString(client.Name), html.EscapeString(client.Name),
		html.EscapeString(r.URL.Query().Get("scope")),
		html.EscapeString(r.URL.Query().Get("client_id")),
		html.EscapeString(r.URL.Query().Get("redirect_uri")),
		html.EscapeString(r.URL.Query().Get("response_type")),
		html.EscapeString(r.URL.Query().Get("scope")),
		html.EscapeString(r.URL.Query().Get("state")),
		html.EscapeString(r.URL.Query().Get("code_challenge")),
		html.EscapeString(r.URL.Query().Get("code_challenge_method")),
	)

	w.Header().Set("Content-Type", "text/html")
	_, _ = w.Write([]byte(respHTML))
}

// handleAuthorizationCodeGrant exchanges auth code for tokens
func (h *AuthHandlers) handleAuthorizationCodeGrant(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	redirectURI := r.FormValue("redirect_uri")
	codeVerifier := r.FormValue("code_verifier")

	if code == "" || redirectURI == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Missing code or redirect_uri")
		return
	}

	oauthRepo := h.oauthRepo
	if oauthRepo == nil {
		writeOAuthError(w, http.StatusNotImplemented, "server_error", "OAuth not configured")
		return
	}

	// Get auth code
	codeRecord, err := oauthRepo.GetAuthorizationCode(r.Context(), code)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Invalid or expired code")
		return
	}

	// Check if already used
	if codeRecord.UsedAt != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Code already used")
		return
	}

	// Check expiration
	if time.Now().After(codeRecord.ExpiresAt) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Code expired")
		return
	}

	// Validate client (already authenticated in OAuthToken handler)
	clientID := r.FormValue("client_id")
	if clientID == "" {
		clientID, _, _ = parseClientAuth(r)
	}

	if codeRecord.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Code was issued to different client")
		return
	}

	// Validate redirect URI
	if codeRecord.RedirectURI != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Redirect URI mismatch")
		return
	}

	// Validate PKCE if used
	if codeRecord.CodeChallenge != "" {
		if !verifyPKCE(codeVerifier, codeRecord.CodeChallenge, codeRecord.CodeChallengeMethod) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Invalid code_verifier")
			return
		}
	}

	// Mark code as used
	if err := oauthRepo.MarkCodeAsUsed(r.Context(), code); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to invalidate code")
		return
	}

	// Get user role
	var role string
	if h.userRepo != nil {
		if u, err := h.userRepo.GetByID(r.Context(), codeRecord.UserID); err == nil {
			role = string(u.Role)
		}
	}

	// Issue tokens
	access := h.generateJWTWithRoleAndScope(codeRecord.UserID, role, codeRecord.Scope, 15*time.Minute)
	refresh := uuid.NewString()

	// Store refresh token
	refreshExpires := time.Now().Add(7 * 24 * time.Hour)
	if h.authRepo != nil {
		rt := &usecase.RefreshToken{
			ID:        uuid.NewString(),
			UserID:    codeRecord.UserID,
			Token:     refresh,
			ExpiresAt: refreshExpires,
			CreatedAt: time.Now(),
		}
		if err := h.authRepo.CreateRefreshToken(r.Context(), rt); err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to persist refresh token")
			return
		}
	}

	// Store access token for revocation
	tokenHash := hashToken(access)
	accessToken := &usecase.OAuthAccessToken{
		ID:        uuid.NewString(),
		TokenHash: tokenHash,
		ClientID:  codeRecord.ClientID,
		UserID:    codeRecord.UserID,
		Scope:     codeRecord.Scope,
		ExpiresAt: time.Now().Add(15 * time.Minute),
		CreatedAt: time.Now(),
	}
	if err := oauthRepo.CreateAccessToken(r.Context(), accessToken); err != nil {
		// Log but don't fail
		_ = err
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	resp := map[string]interface{}{
		"access_token":  access,
		"token_type":    "bearer",
		"expires_in":    15 * 60,
		"refresh_token": refresh,
		"scope":         codeRecord.Scope,
	}
	shared.WriteJSON(w, http.StatusOK, resp)
}

// OAuthRevoke handles POST /oauth/revoke for token revocation
func (h *AuthHandlers) OAuthRevoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Invalid form data")
		return
	}

	token := r.FormValue("token")
	tokenTypeHint := r.FormValue("token_type_hint")

	if token == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Missing token")
		return
	}

	// Authenticate client
	clientID, _, ok := parseClientAuth(r)
	if !ok {
		unauthorizedOAuth(w, "invalid_client", "Client authentication required")
		return
	}

	oauthRepo := h.oauthRepo
	if oauthRepo == nil {
		writeOAuthError(w, http.StatusNotImplemented, "server_error", "OAuth not configured")
		return
	}

	// Verify client exists
	_, err := oauthRepo.GetClientByClientID(r.Context(), clientID)
	if err != nil {
		unauthorizedOAuth(w, "invalid_client", "Invalid client")
		return
	}

	// Try to revoke as access token
	if tokenTypeHint == "" || tokenTypeHint == "access_token" {
		tokenHash := hashToken(token)
		if err := oauthRepo.RevokeAccessToken(r.Context(), tokenHash); err == nil {
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	// Try to revoke as refresh token
	if tokenTypeHint == "" || tokenTypeHint == "refresh_token" {
		if h.authRepo != nil {
			if err := h.authRepo.RevokeRefreshToken(r.Context(), token); err == nil {
				w.WriteHeader(http.StatusOK)
				return
			}
		}
	}

	// RFC 7009: Always respond with 200 OK even if token not found
	w.WriteHeader(http.StatusOK)
}

// OAuthIntrospect handles POST /oauth/introspect for token introspection
func (h *AuthHandlers) OAuthIntrospect(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Invalid form data")
		return
	}

	token := r.FormValue("token")
	tokenTypeHint := r.FormValue("token_type_hint")

	if token == "" {
		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"active": false})
		return
	}

	// Authenticate client
	clientID, _, ok := parseClientAuth(r)
	if !ok {
		unauthorizedOAuth(w, "invalid_client", "Client authentication required")
		return
	}

	oauthRepo := h.oauthRepo
	if oauthRepo == nil {
		writeOAuthError(w, http.StatusNotImplemented, "server_error", "OAuth not configured")
		return
	}

	// Try as access token
	if tokenTypeHint == "" || tokenTypeHint == "access_token" {
		tokenHash := hashToken(token)
		at, err := oauthRepo.GetAccessToken(r.Context(), tokenHash)
		if err == nil && at.RevokedAt == nil && time.Now().Before(at.ExpiresAt) {
			// Only return info if token belongs to requesting client
			if at.ClientID == clientID {
				resp := map[string]interface{}{
					"active":     true,
					"scope":      at.Scope,
					"client_id":  at.ClientID,
					"username":   at.UserID,
					"token_type": "Bearer",
					"exp":        at.ExpiresAt.Unix(),
					"iat":        at.CreatedAt.Unix(),
				}
				shared.WriteJSON(w, http.StatusOK, resp)
				return
			}
		}
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"active": false})
}

// Helper functions

func generateSecureToken(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b)
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func verifyPKCE(verifier, challenge, method string) bool {
	if method == "" || method == "plain" {
		return verifier == challenge
	}
	if method == "S256" {
		h := sha256.Sum256([]byte(verifier))
		computed := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(h[:])
		return computed == challenge
	}
	return false
}

func isValidRedirectURI(uri string, allowed []string) bool {
	for _, a := range allowed {
		if uri == a {
			return true
		}
	}
	return false
}

func containsGrantType(grants []string, grant string) bool {
	for _, g := range grants {
		if g == grant {
			return true
		}
	}
	return false
}

func parseScopes(scope string) []string {
	if scope == "" {
		return []string{"basic"}
	}
	return strings.Split(scope, " ")
}

func validateScopes(requested, allowed []string) bool {
	for _, r := range requested {
		found := false
		for _, a := range allowed {
			if r == a {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func redirectError(w http.ResponseWriter, r *http.Request, redirectURI, code, desc, state string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Invalid redirect_uri")
		return
	}
	q := u.Query()
	q.Set("error", code)
	q.Set("error_description", desc)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}
