package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	chi "github.com/go-chi/chi/v5"
	validator "github.com/go-playground/validator/v10"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"
)

var validate = validator.New()

// SendMessageHandler handles sending a new message
func SendMessageHandler(messageService *usecase.MessageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		var req domain.SendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
			return
		}

		if err := validate.Struct(&req); err != nil {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("VALIDATION_ERROR", err.Error()))
			return
		}

		message, err := messageService.SendMessage(r.Context(), userID, &req)
		if err != nil {
			status := MapDomainErrorToHTTP(err)
			WriteError(w, status, domain.NewDomainError("SEND_MESSAGE_FAILED", err.Error()))
			return
		}

		WriteJSON(w, http.StatusCreated, domain.MessageResponse{Message: *message})
	}
}

// GetMessagesHandler handles retrieving messages in a conversation
func GetMessagesHandler(messageService *usecase.MessageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		conversationWith := r.URL.Query().Get("conversation_with")
		if conversationWith == "" {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_PARAMETER", "conversation_with parameter is required"))
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
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("VALIDATION_ERROR", err.Error()))
			return
		}

		response, err := messageService.GetMessages(r.Context(), userID, req)
		if err != nil {
			status := MapDomainErrorToHTTP(err)
			WriteError(w, status, domain.NewDomainError("GET_MESSAGES_FAILED", err.Error()))
			return
		}

		WriteJSON(w, http.StatusOK, response)
	}
}

// messageActionHandler is a helper to reduce duplication in message handlers
func messageActionHandler(w http.ResponseWriter, r *http.Request, action func(ctx context.Context, userID, messageID string) error, errorCode string) {
	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
		return
	}

	messageID := chi.URLParam(r, "messageId")
	if messageID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_PARAMETER", "messageId parameter is required"))
		return
	}

	err := action(r.Context(), userID, messageID)
	if err != nil {
		status := MapDomainErrorToHTTP(err)
		WriteError(w, status, domain.NewDomainError(errorCode, err.Error()))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// MarkMessageReadHandler handles marking a message as read
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

// DeleteMessageHandler handles deleting a message
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

// GetConversationsHandler handles retrieving user's conversations
func GetConversationsHandler(messageService *usecase.MessageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

		req := &domain.GetConversationsRequest{
			Limit:  limit,
			Offset: offset,
		}

		if err := validate.Struct(req); err != nil {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("VALIDATION_ERROR", err.Error()))
			return
		}

		response, err := messageService.GetConversations(r.Context(), userID, req)
		if err != nil {
			status := MapDomainErrorToHTTP(err)
			WriteError(w, status, domain.NewDomainError("GET_CONVERSATIONS_FAILED", err.Error()))
			return
		}

		WriteJSON(w, http.StatusOK, response)
	}
}

// GetUnreadCountHandler handles retrieving the user's unread message count
func GetUnreadCountHandler(messageService *usecase.MessageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		count, err := messageService.GetUnreadCount(r.Context(), userID)
		if err != nil {
			status := MapDomainErrorToHTTP(err)
			WriteError(w, status, domain.NewDomainError("GET_UNREAD_COUNT_FAILED", err.Error()))
			return
		}

		WriteJSON(w, http.StatusOK, map[string]int{"unread_count": count})
	}
}
