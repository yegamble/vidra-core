package messaging

import (
	"vidra-core/internal/httpapi/shared"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	chi "github.com/go-chi/chi/v5"
	validator "github.com/go-playground/validator/v10"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/usecase"

	"github.com/google/uuid"
)

var validate = validator.New()

func getUserID(ctx context.Context) string {
	if raw := ctx.Value(middleware.UserIDKey); raw != nil {
		switch v := raw.(type) {
		case string:
			if v != "" {
				return v
			}
		case uuid.UUID:
			if v != uuid.Nil {
				return v.String()
			}
		}
	}

	if v, ok := ctx.Value("userID").(string); ok && v != "" {
		return v
	}
	return ""
}

func SendMessageHandler(messageService MessageServiceInterface) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := getUserID(r.Context())
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		var req domain.SendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
			return
		}

		if err := validate.Struct(&req); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("VALIDATION_ERROR", err.Error()))
			return
		}

		message, err := messageService.SendMessage(r.Context(), userID, &req)
		if err != nil {
			status := shared.MapDomainErrorToHTTP(err)
			shared.WriteError(w, status, domain.NewDomainError("SEND_MESSAGE_FAILED", err.Error()))
			return
		}

		shared.WriteJSON(w, http.StatusCreated, domain.MessageResponse{Message: *message})
	}
}

func GetMessagesHandler(messageService MessageServiceInterface) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := getUserID(r.Context())
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		conversationWith := r.URL.Query().Get("conversation_with")
		if conversationWith == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_PARAMETER", "conversation_with parameter is required"))
			return
		}

		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		before := r.URL.Query().Get("before")

		req := &domain.GetMessagesRequest{
			ConversationWith: conversationWith,
			Limit:            limit,
			Offset:           offset,
			Before:           before,
		}

		if err := validate.Struct(req); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("VALIDATION_ERROR", err.Error()))
			return
		}

		response, err := messageService.GetMessages(r.Context(), userID, req)
		if err != nil {
			status := shared.MapDomainErrorToHTTP(err)
			shared.WriteError(w, status, domain.NewDomainError("GET_MESSAGES_FAILED", err.Error()))
			return
		}

		shared.WriteJSON(w, http.StatusOK, response)
	}
}

func messageActionHandler(w http.ResponseWriter, r *http.Request, action func(ctx context.Context, userID, messageID string) error, errorCode string) {
	userID := getUserID(r.Context())
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
		return
	}

	messageID := chi.URLParam(r, "messageId")
	if messageID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_PARAMETER", "messageId parameter is required"))
		return
	}

	err := action(r.Context(), userID, messageID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, domain.NewDomainError(errorCode, err.Error()))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

func MarkMessageReadHandler(messageService *usecase.MessageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		messageActionHandler(w, r, func(ctx context.Context, userID, messageID string) error {
			req := &domain.MarkMessageReadRequest{MessageID: messageID}
			if err := validate.Struct(req); err != nil {
				return fmt.Errorf("%w: %s", domain.ErrValidation, err.Error())
			}
			return messageService.MarkMessageAsRead(ctx, userID, req)
		}, "MARK_READ_FAILED")
	}
}

func DeleteMessageHandler(messageService *usecase.MessageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		messageActionHandler(w, r, func(ctx context.Context, userID, messageID string) error {
			req := &domain.DeleteMessageRequest{MessageID: messageID}
			if err := validate.Struct(req); err != nil {
				return fmt.Errorf("%w: %s", domain.ErrValidation, err.Error())
			}
			return messageService.DeleteMessage(ctx, userID, req)
		}, "DELETE_MESSAGE_FAILED")
	}
}

func GetConversationsHandler(messageService *usecase.MessageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := getUserID(r.Context())
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

		req := &domain.GetConversationsRequest{
			Limit:  limit,
			Offset: offset,
		}

		if err := validate.Struct(req); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("VALIDATION_ERROR", err.Error()))
			return
		}

		response, err := messageService.GetConversations(r.Context(), userID, req)
		if err != nil {
			status := shared.MapDomainErrorToHTTP(err)
			shared.WriteError(w, status, domain.NewDomainError("GET_CONVERSATIONS_FAILED", err.Error()))
			return
		}

		shared.WriteJSON(w, http.StatusOK, response)
	}
}

func GetUnreadCountHandler(messageService *usecase.MessageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := getUserID(r.Context())
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		count, err := messageService.GetUnreadCount(r.Context(), userID)
		if err != nil {
			status := shared.MapDomainErrorToHTTP(err)
			shared.WriteError(w, status, domain.NewDomainError("GET_UNREAD_COUNT_FAILED", err.Error()))
			return
		}

		shared.WriteJSON(w, http.StatusOK, map[string]int{"unread_count": count})
	}
}
