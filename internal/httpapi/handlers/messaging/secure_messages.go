package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	"athena/internal/domain"
	"athena/internal/middleware"
)

type SecureMessagesHandler struct {
	e2eeService E2EEServiceInterface
	validator   *validator.Validate
}

func NewSecureMessagesHandler(e2eeService E2EEServiceInterface, validator *validator.Validate) *SecureMessagesHandler {
	return &SecureMessagesHandler{
		e2eeService: e2eeService,
		validator:   validator,
	}
}

func (h *SecureMessagesHandler) RegisterIdentityKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := GetUserIDFromContext(ctx)

	var req domain.RegisterIdentityKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteErrorResponse(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		WriteValidationErrorResponse(w, err)
		return
	}

	clientIP := GetClientIP(r)
	userAgent := r.UserAgent()

	err := h.e2eeService.RegisterIdentityKey(ctx, userID, req.PublicIdentityKey, req.PublicSigningKey, clientIP, userAgent)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			WriteErrorResponse(w, http.StatusConflict, "key_conflict", "Identity key already registered for this user")
			return
		}
		WriteErrorResponse(w, http.StatusInternalServerError, "register_failed", "Failed to register identity key")
		return
	}

	WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message": "Identity key registered successfully",
	})
}

func (h *SecureMessagesHandler) GetPublicKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	targetUserID := chi.URLParam(r, "userId")

	if targetUserID == "" {
		WriteErrorResponse(w, http.StatusBadRequest, "missing_user_id", "User ID is required")
		return
	}

	bundle, err := h.e2eeService.GetPublicKeys(ctx, targetUserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			WriteErrorResponse(w, http.StatusNotFound, "keys_not_found", "No keys registered for this user")
			return
		}
		WriteErrorResponse(w, http.StatusInternalServerError, "fetch_failed", "Failed to get public keys")
		return
	}

	WriteJSONResponse(w, http.StatusOK, bundle)
}

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

	if req.RecipientID == userID {
		WriteErrorResponse(w, http.StatusBadRequest, "invalid_recipient", "Cannot initiate key exchange with yourself")
		return
	}

	clientIP := GetClientIP(r)
	userAgent := r.UserAgent()

	keyExchange, err := h.e2eeService.InitiateKeyExchange(ctx, userID, req.RecipientID, req.SenderPublicKey, clientIP, userAgent)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			WriteErrorResponse(w, http.StatusConflict, "already_encrypted", "Conversation already has E2EE enabled")
			return
		}
		WriteErrorResponse(w, http.StatusInternalServerError, "key_exchange_failed", "Failed to initiate key exchange")
		return
	}

	response := &domain.KeyExchangeResponse{
		KeyExchange: *keyExchange,
		Status:      "initiated",
	}

	WriteJSONResponse(w, http.StatusCreated, response)
}

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

	clientIP := GetClientIP(r)
	userAgent := r.UserAgent()

	err := h.e2eeService.AcceptKeyExchange(ctx, req.KeyExchangeID, userID, req.PublicKey, clientIP, userAgent)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			WriteErrorResponse(w, http.StatusNotFound, "key_exchange_not_found", "Key exchange not found or expired")
			return
		}
		if errors.Is(err, domain.ErrForbidden) {
			WriteErrorResponse(w, http.StatusForbidden, "unauthorized", "Unauthorized to accept this key exchange")
			return
		}
		if errors.Is(err, domain.ErrKeyExchangeExpired) {
			WriteErrorResponse(w, http.StatusGone, "key_exchange_expired", "Key exchange has expired")
			return
		}
		WriteErrorResponse(w, http.StatusInternalServerError, "accept_failed", "Failed to accept key exchange")
		return
	}

	WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"message": "Key exchange accepted successfully",
		"status":  "active",
	})
}

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

func (h *SecureMessagesHandler) StoreEncryptedMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := GetUserIDFromContext(ctx)

	var req domain.StoreEncryptedMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteErrorResponse(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	if err := h.validator.Struct(req); err != nil {
		WriteValidationErrorResponse(w, err)
		return
	}

	if req.RecipientID == userID {
		WriteErrorResponse(w, http.StatusBadRequest, "invalid_recipient", "Cannot send message to yourself")
		return
	}

	clientIP := GetClientIP(r)
	userAgent := r.UserAgent()

	message, err := h.e2eeService.StoreEncryptedMessage(ctx, userID, &req, clientIP, userAgent)
	if err != nil {
		if errors.Is(err, domain.ErrKeyExchangeNotComplete) {
			WriteErrorResponse(w, http.StatusPreconditionFailed, "key_exchange_required", "Key exchange not complete for this conversation")
			return
		}
		WriteErrorResponse(w, http.StatusInternalServerError, "store_failed", "Failed to store encrypted message")
		return
	}

	response := &domain.MessageResponse{
		Message: *message,
	}

	WriteJSONResponse(w, http.StatusCreated, response)
}

func (h *SecureMessagesHandler) GetEncryptedMessages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := GetUserIDFromContext(ctx)
	conversationID := chi.URLParam(r, "conversationId")

	if conversationID == "" {
		WriteErrorResponse(w, http.StatusBadRequest, "missing_conversation_id", "Conversation ID is required")
		return
	}

	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	messages, err := h.e2eeService.GetEncryptedMessages(ctx, conversationID, userID, limit, offset)
	if err != nil {
		if errors.Is(err, domain.ErrForbidden) {
			WriteErrorResponse(w, http.StatusForbidden, "unauthorized", "You are not a participant in this conversation")
			return
		}
		if errors.Is(err, domain.ErrNotFound) {
			WriteErrorResponse(w, http.StatusNotFound, "conversation_not_found", "Conversation not found")
			return
		}
		WriteErrorResponse(w, http.StatusInternalServerError, "fetch_failed", "Failed to get encrypted messages")
		return
	}

	WriteJSONResponse(w, http.StatusOK, map[string]interface{}{
		"messages": messages,
		"total":    len(messages),
		"limit":    limit,
		"offset":   offset,
	})
}

func GetUserIDFromContext(ctx context.Context) string {
	userID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		return ""
	}
	return userID.String()
}

func GetClientIP(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}

	if isPrivateIP(host) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first, _, ok := strings.Cut(xff, ","); ok {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(xff)
		}
		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			return strings.TrimSpace(realIP)
		}
	}

	return host
}

func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	privateRanges := []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "::1/128"}
	for _, cidr := range privateRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

func WriteJSONResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	err := json.NewEncoder(w).Encode(data)
	if err != nil {
		http.Error(w, `{"error": {"code": "internal_error", "message": "Failed to encode response"}}`, http.StatusInternalServerError)
	}
}

func WriteErrorResponse(w http.ResponseWriter, statusCode int, errorCode, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	err := json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    errorCode,
			"message": message,
		},
	})
	if err != nil {
		http.Error(w, `{"error": {"code": "internal_error", "message": "Failed to encode error response"}}`, http.StatusInternalServerError)
	}
}

func WriteValidationErrorResponse(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)

	var validationErrors []map[string]interface{}

	var validationErr validator.ValidationErrors
	if errors.As(err, &validationErr) {
		for _, fieldError := range validationErr {
			validationErrors = append(validationErrors, map[string]interface{}{
				"field":   fieldError.Field(),
				"tag":     fieldError.Tag(),
				"message": getValidationErrorMessage(fieldError),
			})
		}
	}

	err = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    "validation_failed",
			"message": "Validation failed",
			"details": validationErrors,
		},
	})
	if err != nil {
		http.Error(w, `{"error": {"code": "internal_error", "message": "Failed to encode validation error response"}}`, http.StatusInternalServerError)
	}
}

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
