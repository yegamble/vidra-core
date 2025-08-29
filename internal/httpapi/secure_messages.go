package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	"athena/internal/domain"
	"athena/internal/usecase"
)

// SecureMessagesHandler handles E2EE messaging endpoints
type SecureMessagesHandler struct {
	e2eeService *usecase.E2EEService
	validator   *validator.Validate
}

// NewSecureMessagesHandler creates a new secure messages handler
func NewSecureMessagesHandler(e2eeService *usecase.E2EEService, validator *validator.Validate) *SecureMessagesHandler {
	return &SecureMessagesHandler{
		e2eeService: e2eeService,
		validator:   validator,
	}
}

// SetupE2EE sets up E2EE for a user
func (h *SecureMessagesHandler) SetupE2EE(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := GetUserIDFromContext(ctx)

	var req domain.SetupE2EERequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteErrorResponse(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		WriteValidationErrorResponse(w, err)
		return
	}

	// Get client info for audit logging
	clientIP := GetClientIP(r)
	userAgent := r.UserAgent()

	err := h.e2eeService.SetupE2EE(ctx, userID, req.Password, clientIP, userAgent)
	if err != nil {
		if err.Error() == "user already has E2EE setup" {
			WriteErrorResponse(w, http.StatusConflict, "already_setup", "E2EE already setup for this user")
			return
		}
		WriteErrorResponse(w, http.StatusInternalServerError, "setup_failed", "Failed to setup E2EE")
		return
	}

	WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message": "E2EE setup completed successfully",
	})
}

// UnlockE2EE unlocks a user's E2EE session
func (h *SecureMessagesHandler) UnlockE2EE(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := GetUserIDFromContext(ctx)

	var req domain.UnlockE2EERequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteErrorResponse(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		WriteValidationErrorResponse(w, err)
		return
	}

	// Get client info for audit logging
	clientIP := GetClientIP(r)
	userAgent := r.UserAgent()

	err := h.e2eeService.UnlockE2EE(ctx, userID, req.Password, clientIP, userAgent)
	if err != nil {
		if err.Error() == "invalid password" {
			WriteErrorResponse(w, http.StatusUnauthorized, "invalid_password", "Invalid password")
			return
		}
		if err.Error() == "user has no E2EE setup" {
			WriteErrorResponse(w, http.StatusNotFound, "no_setup", "User has no E2EE setup")
			return
		}
		WriteErrorResponse(w, http.StatusInternalServerError, "unlock_failed", "Failed to unlock E2EE")
		return
	}

	WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message": "E2EE session unlocked successfully",
	})
}

// LockE2EE locks a user's E2EE session
func (h *SecureMessagesHandler) LockE2EE(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := GetUserIDFromContext(ctx)

	h.e2eeService.LockE2EE(ctx, userID)

	WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message": "E2EE session locked successfully",
	})
}

// GetE2EEStatus returns the E2EE status for a user
func (h *SecureMessagesHandler) GetE2EEStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := GetUserIDFromContext(ctx)

	status, err := h.e2eeService.GetE2EEStatus(ctx, userID)
	if err != nil {
		WriteErrorResponse(w, http.StatusInternalServerError, "status_failed", "Failed to get E2EE status")
		return
	}

	WriteJSONResponse(w, http.StatusOK, status)
}

// InitiateKeyExchange initiates E2EE key exchange for a conversation
func (h *SecureMessagesHandler) InitiateKeyExchange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := GetUserIDFromContext(ctx)

	var req domain.InitiateKeyExchangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteErrorResponse(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		WriteValidationErrorResponse(w, err)
		return
	}

	// Prevent self-messaging
	if req.RecipientID == userID {
		WriteErrorResponse(w, http.StatusBadRequest, "invalid_recipient", "Cannot initiate key exchange with yourself")
		return
	}

	// Get client info for audit logging
	clientIP := GetClientIP(r)
	userAgent := r.UserAgent()

	keyExchange, err := h.e2eeService.InitiateKeyExchange(ctx, userID, req.RecipientID, clientIP, userAgent)
	if err != nil {
		switch err.Error() {
		case "sender E2EE session not unlocked":
			WriteErrorResponse(w, http.StatusUnauthorized, "session_locked", "E2EE session not unlocked")
			return
		case "conversation already has E2EE enabled":
			WriteErrorResponse(w, http.StatusConflict, "already_encrypted", "Conversation already has E2EE enabled")
			return
		default:
			WriteErrorResponse(w, http.StatusInternalServerError, "key_exchange_failed", "Failed to initiate key exchange")
			return
		}
	}

	response := &domain.KeyExchangeResponse{
		KeyExchange: *keyExchange,
		Status:      "initiated",
	}

	WriteJSONResponse(w, http.StatusCreated, response)
}

// AcceptKeyExchange accepts an E2EE key exchange
func (h *SecureMessagesHandler) AcceptKeyExchange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := GetUserIDFromContext(ctx)

	var req domain.AcceptKeyExchangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteErrorResponse(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		WriteValidationErrorResponse(w, err)
		return
	}

	// Get client info for audit logging
	clientIP := GetClientIP(r)
	userAgent := r.UserAgent()

	err := h.e2eeService.AcceptKeyExchange(ctx, req.KeyExchangeID, userID, clientIP, userAgent)
	if err != nil {
		switch err.Error() {
		case "user E2EE session not unlocked":
			WriteErrorResponse(w, http.StatusUnauthorized, "session_locked", "E2EE session not unlocked")
			return
		case "key exchange not found or expired":
			WriteErrorResponse(w, http.StatusNotFound, "key_exchange_not_found", "Key exchange not found or expired")
			return
		case "unauthorized to accept this key exchange":
			WriteErrorResponse(w, http.StatusForbidden, "unauthorized", "Unauthorized to accept this key exchange")
			return
		case "invalid key exchange type for acceptance":
			WriteErrorResponse(w, http.StatusBadRequest, "invalid_exchange_type", "Invalid key exchange type for acceptance")
			return
		case "invalid key exchange signature":
			WriteErrorResponse(w, http.StatusBadRequest, "invalid_signature", "Invalid key exchange signature")
			return
		default:
			WriteErrorResponse(w, http.StatusInternalServerError, "accept_failed", "Failed to accept key exchange")
			return
		}
	}

	WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message": "Key exchange accepted successfully",
		"status":  "completed",
	})
}

// GetPendingKeyExchanges returns pending key exchanges for a user
func (h *SecureMessagesHandler) GetPendingKeyExchanges(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := GetUserIDFromContext(ctx)

	keyExchanges, err := h.e2eeService.GetPendingKeyExchanges(ctx, userID)
	if err != nil {
		WriteErrorResponse(w, http.StatusInternalServerError, "fetch_failed", "Failed to get pending key exchanges")
		return
	}

	WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"key_exchanges": keyExchanges,
		"total":         len(keyExchanges),
	})
}

// SendSecureMessage sends an encrypted message
func (h *SecureMessagesHandler) SendSecureMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := GetUserIDFromContext(ctx)

	var req domain.SendSecureMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteErrorResponse(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		WriteValidationErrorResponse(w, err)
		return
	}

	// Prevent self-messaging
	if req.RecipientID == userID {
		WriteErrorResponse(w, http.StatusBadRequest, "invalid_recipient", "Cannot send message to yourself")
		return
	}

	// Get client info for audit logging
	clientIP := GetClientIP(r)
	userAgent := r.UserAgent()

	// For secure messages, we need to decrypt the client-side encrypted content
	// and re-encrypt it with our shared secret
	// The client sends the message encrypted with the recipient's public key
	// We decrypt it and then encrypt it with the conversation shared secret

	// Note: This is a simplified flow. In a full implementation, you might want
	// to have the client directly encrypt with the shared secret they computed
	// from their local key exchange

	message, err := h.e2eeService.EncryptMessage(ctx, userID, req.RecipientID, req.EncryptedContent, clientIP, userAgent)
	if err != nil {
		switch err.Error() {
		case "sender E2EE session not unlocked":
			WriteErrorResponse(w, http.StatusUnauthorized, "session_locked", "E2EE session not unlocked")
			return
		case "conversation not ready for E2EE":
			WriteErrorResponse(w, http.StatusPreconditionFailed, "not_ready", "Conversation not ready for E2EE")
			return
		case "no shared secret available":
			WriteErrorResponse(w, http.StatusPreconditionFailed, "no_shared_secret", "No shared secret available")
			return
		default:
			WriteErrorResponse(w, http.StatusInternalServerError, "encryption_failed", "Failed to encrypt message")
			return
		}
	}

	// Save encrypted message to database
	err = h.e2eeService.SaveSecureMessage(ctx, message)
	if err != nil {
		WriteErrorResponse(w, http.StatusInternalServerError, "save_failed", "Failed to save secure message")
		return
	}

	response := &domain.MessageResponse{
		Message: *message,
	}

	WriteJSONResponse(w, http.StatusCreated, response)
}

// DecryptMessage decrypts a secure message for the authenticated user
func (h *SecureMessagesHandler) DecryptMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := GetUserIDFromContext(ctx)
	messageID := chi.URLParam(r, "messageId")

	if messageID == "" {
		WriteErrorResponse(w, http.StatusBadRequest, "missing_message_id", "Message ID is required")
		return
	}

	// Get message from repository
	message, err := h.e2eeService.GetMessage(ctx, messageID)
	if err != nil {
		WriteErrorResponse(w, http.StatusNotFound, "message_not_found", "Message not found")
		return
	}

	// Verify user can decrypt this message
	if message.SenderID != userID && message.RecipientID != userID {
		WriteErrorResponse(w, http.StatusForbidden, "unauthorized", "Unauthorized to decrypt this message")
		return
	}

	if !message.IsEncrypted {
		WriteErrorResponse(w, http.StatusBadRequest, "not_encrypted", "Message is not encrypted")
		return
	}

	// Get client info for audit logging
	clientIP := GetClientIP(r)
	userAgent := r.UserAgent()

	// Decrypt message
	plaintext, err := h.e2eeService.DecryptMessage(ctx, message, userID, clientIP, userAgent)
	if err != nil {
		switch err.Error() {
		case "user E2EE session not unlocked":
			WriteErrorResponse(w, http.StatusUnauthorized, "session_locked", "E2EE session not unlocked")
			return
		case "unauthorized to decrypt message":
			WriteErrorResponse(w, http.StatusForbidden, "unauthorized", "Unauthorized to decrypt message")
			return
		case "message is not encrypted":
			WriteErrorResponse(w, http.StatusBadRequest, "not_encrypted", "Message is not encrypted")
			return
		case "invalid message signature":
			WriteErrorResponse(w, http.StatusBadRequest, "invalid_signature", "Invalid message signature")
			return
		default:
			WriteErrorResponse(w, http.StatusInternalServerError, "decryption_failed", "Failed to decrypt message")
			return
		}
	}

	WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message_id": messageID,
		"content":    plaintext,
		"sender_id":  message.SenderID,
		"created_at": message.CreatedAt,
	})
}

// Helper functions (these would typically be in a separate utilities file)

// GetUserIDFromContext extracts user ID from request context
func GetUserIDFromContext(ctx context.Context) string {
	// This should be implemented based on your authentication middleware
	// For now, returning a placeholder
	return "user-id-placeholder"
}

// GetClientIP extracts client IP from request
func GetClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first
	if xForwardedFor := r.Header.Get("X-Forwarded-For"); xForwardedFor != "" {
		// X-Forwarded-For can contain multiple IPs, take the first one
		if commaIndex := strings.Index(xForwardedFor, ","); commaIndex != -1 {
			return strings.TrimSpace(xForwardedFor[:commaIndex])
		}
		return strings.TrimSpace(xForwardedFor)
	}

	// Check X-Real-IP header
	if xRealIP := r.Header.Get("X-Real-IP"); xRealIP != "" {
		return strings.TrimSpace(xRealIP)
	}

	// Fall back to RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}

	return r.RemoteAddr
}

// WriteJSONResponse writes a JSON response
func WriteJSONResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// WriteErrorResponse writes an error response
func WriteErrorResponse(w http.ResponseWriter, statusCode int, errorCode, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    errorCode,
			"message": message,
		},
	})
}

// WriteValidationErrorResponse writes a validation error response
func WriteValidationErrorResponse(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)

	var validationErrors []map[string]interface{}

	if validationErr, ok := err.(validator.ValidationErrors); ok {
		for _, fieldError := range validationErr {
			validationErrors = append(validationErrors, map[string]interface{}{
				"field":   fieldError.Field(),
				"tag":     fieldError.Tag(),
				"message": getValidationErrorMessage(fieldError),
			})
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    "validation_failed",
			"message": "Validation failed",
			"details": validationErrors,
		},
	})
}

// getValidationErrorMessage returns user-friendly validation error messages
func getValidationErrorMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "This field is required"
	case "email":
		return "Invalid email format"
	case "min":
		return fmt.Sprintf("Minimum length is %s", fe.Param())
	case "max":
		return fmt.Sprintf("Maximum length is %s", fe.Param())
	case "uuid":
		return "Invalid UUID format"
	default:
		return "Invalid value"
	}
}
