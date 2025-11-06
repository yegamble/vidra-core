package auth

import (
	"encoding/json"
	"net/http"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/usecase"
)

// TwoFAHandlers handles two-factor authentication HTTP endpoints
type TwoFAHandlers struct {
	twoFAService *usecase.TwoFAService
}

// NewTwoFAHandlers creates a new TwoFAHandlers instance
func NewTwoFAHandlers(twoFAService *usecase.TwoFAService) *TwoFAHandlers {
	return &TwoFAHandlers{
		twoFAService: twoFAService,
	}
}

// SetupTwoFA initiates 2FA setup for the current user
// POST /api/v1/auth/2fa/setup
func (h *TwoFAHandlers) SetupTwoFA(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
		return
	}

	// Generate secret and backup codes
	setup, err := h.twoFAService.GenerateSecret(r.Context(), userID)
	if err != nil {
		if err == domain.ErrTwoFAAlreadyEnabled {
			shared.WriteError(w, http.StatusConflict, domain.NewDomainError("TWOFA_ALREADY_ENABLED", "Two-factor authentication is already enabled"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to setup two-factor authentication"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, setup)
}

// VerifyTwoFASetup verifies the TOTP code and enables 2FA
// POST /api/v1/auth/2fa/verify-setup
func (h *TwoFAHandlers) VerifyTwoFASetup(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
		return
	}

	var req domain.TwoFAVerifySetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.Code == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CODE", "Two-factor authentication code is required"))
		return
	}

	// Verify and enable 2FA
	err := h.twoFAService.VerifySetup(r.Context(), userID, req.Code)
	if err != nil {
		if err == domain.ErrTwoFAInvalidCode {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_CODE", "Invalid two-factor authentication code"))
			return
		}
		if err == domain.ErrTwoFAAlreadyEnabled {
			shared.WriteError(w, http.StatusConflict, domain.NewDomainError("TWOFA_ALREADY_ENABLED", "Two-factor authentication is already enabled"))
			return
		}
		if err == domain.ErrTwoFASetupIncomplete {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("TWOFA_SETUP_INCOMPLETE", "Two-factor authentication setup is incomplete. Please restart the setup process."))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to verify two-factor authentication"))
		return
	}

	response := domain.TwoFAVerifySetupResponse{
		Enabled: true,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// DisableTwoFA disables 2FA for the current user
// POST /api/v1/auth/2fa/disable
func (h *TwoFAHandlers) DisableTwoFA(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
		return
	}

	var req domain.TwoFADisableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.Password == "" || req.Code == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FIELDS", "Password and two-factor code are required"))
		return
	}

	// Disable 2FA
	err := h.twoFAService.Disable(r.Context(), userID, req.Password, req.Code)
	if err != nil {
		if err == domain.ErrInvalidCredentials {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("INVALID_PASSWORD", "Invalid password"))
			return
		}
		if err == domain.ErrTwoFAInvalidCode {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_CODE", "Invalid two-factor authentication code"))
			return
		}
		if err == domain.ErrTwoFANotEnabled {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("TWOFA_NOT_ENABLED", "Two-factor authentication is not enabled"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to disable two-factor authentication"))
		return
	}

	response := domain.TwoFADisableResponse{
		Disabled: true,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// RegenerateBackupCodes regenerates backup codes for the current user
// POST /api/v1/auth/2fa/regenerate-backup-codes
func (h *TwoFAHandlers) RegenerateBackupCodes(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
		return
	}

	var req domain.TwoFARegenerateBackupCodesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.Code == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CODE", "Two-factor authentication code is required"))
		return
	}

	// Regenerate backup codes
	backupCodes, err := h.twoFAService.RegenerateBackupCodes(r.Context(), userID, req.Code)
	if err != nil {
		if err == domain.ErrTwoFAInvalidCode {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_CODE", "Invalid two-factor authentication code"))
			return
		}
		if err == domain.ErrTwoFANotEnabled {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("TWOFA_NOT_ENABLED", "Two-factor authentication is not enabled"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to regenerate backup codes"))
		return
	}

	response := domain.TwoFARegenerateBackupCodesResponse{
		BackupCodes: backupCodes,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// GetTwoFAStatus returns the 2FA status for the current user
// GET /api/v1/auth/2fa/status
func (h *TwoFAHandlers) GetTwoFAStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
		return
	}

	// This would need to be implemented in the service or we can query the user directly
	// For now, we'll just return a simple response structure
	response := map[string]interface{}{
		"enabled": false, // This should be fetched from the user
	}

	shared.WriteJSON(w, http.StatusOK, response)
}
