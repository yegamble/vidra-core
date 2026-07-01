package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/messaging"
)

const maxMessageLen = 5000

// startConversationRequest is the POST /conversations body.
type startConversationRequest struct {
	RecipientID string `json:"recipient_id"`
}

func (r startConversationRequest) Validate() []FieldError {
	if strings.TrimSpace(r.RecipientID) == "" {
		return []FieldError{{Field: "recipient_id", Message: "is required"}}
	}
	return nil
}

// conversationView is the projection of a conversation.
type conversationView struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// conversationSummaryView is a conversation in the inbox list: the other
// participant + last message preview.
type conversationSummaryView struct {
	ID               string    `json:"id"`
	UpdatedAt        time.Time `json:"updated_at"`
	OtherUserID      string    `json:"other_user_id"`
	OtherUsername    string    `json:"other_username"`
	OtherDisplayName string    `json:"other_display_name"`
	LastMessageBody  string    `json:"last_message_body"`
	LastMessageAt    time.Time `json:"last_message_at"`
}

type conversationListResponse struct {
	Conversations []conversationSummaryView `json:"conversations"`
	Limit         int                       `json:"limit"`
	Offset        int                       `json:"offset"`
}

// sendMessageRequest is the POST /conversations/{id}/messages body.
type sendMessageRequest struct {
	Body string `json:"body"`
}

func (r sendMessageRequest) Validate() []FieldError {
	body := strings.TrimSpace(r.Body)
	switch {
	case body == "":
		return []FieldError{{Field: "body", Message: "is required"}}
	case len(body) > maxMessageLen:
		return []FieldError{{Field: "body", Message: "must be at most 5000 characters"}}
	}
	return nil
}

// messageView is the projection of a message.
type messageView struct {
	ID                string    `json:"id"`
	ConversationID    string    `json:"conversation_id"`
	SenderID          string    `json:"sender_id"`
	SenderUsername    string    `json:"sender_username,omitempty"`
	SenderDisplayName string    `json:"sender_display_name,omitempty"`
	Body              string    `json:"body"`
	CreatedAt         time.Time `json:"created_at"`
}

func newMessageView(m messaging.Message) messageView {
	return messageView{
		ID:                m.ID.String(),
		ConversationID:    m.ConversationID.String(),
		SenderID:          m.SenderID.String(),
		SenderUsername:    m.SenderUsername,
		SenderDisplayName: m.SenderDisplayName,
		Body:              m.Body,
		CreatedAt:         m.CreatedAt,
	}
}

type messageListResponse struct {
	Messages []messageView `json:"messages"`
	Limit    int           `json:"limit"`
	Offset   int           `json:"offset"`
}

// handleStartConversation returns (creating if needed) the 1:1 conversation with
// the recipient. Behind requireAuth. Messaging yourself → 422; unknown recipient
// → 404. Idempotent (returns the existing conversation).
func (s *Server) handleStartConversation(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in startConversationRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	recipientID, err := uuid.Parse(strings.TrimSpace(in.RecipientID))
	if err != nil {
		return &ValidationError{Fields: []FieldError{{Field: "recipient_id", Message: "must be a valid id"}}}
	}
	conv, err := s.messagingsvc.StartConversation(c.Request().Context(), userID, recipientID)
	if err != nil {
		switch {
		case errors.Is(err, messaging.ErrCannotMessageSelf):
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "cannot message yourself")
		case errors.Is(err, messaging.ErrRecipientNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "recipient not found")
		}
		return err
	}
	return c.JSON(http.StatusCreated, conversationView{
		ID: conv.ID.String(), CreatedAt: conv.CreatedAt, UpdatedAt: conv.UpdatedAt,
	})
}

// handleListConversations returns the caller's inbox, most-recently-active first.
// Behind requireAuth. Pagination via ?limit (1–100, default 20)/?offset.
func (s *Server) handleListConversations(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.messagingsvc.ListConversations(c.Request().Context(), userID, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]conversationSummaryView, 0, len(items))
	for _, it := range items {
		views = append(views, conversationSummaryView{
			ID: it.ID.String(), UpdatedAt: it.UpdatedAt, OtherUserID: it.OtherUserID.String(),
			OtherUsername: it.OtherUsername, OtherDisplayName: it.OtherDisplayName,
			LastMessageBody: it.LastMessageBody, LastMessageAt: it.LastMessageAt,
		})
	}
	return c.JSON(http.StatusOK, conversationListResponse{Conversations: views, Limit: limit, Offset: offset})
}

// handleListMessages returns a conversation's messages, newest first. Behind
// requireAuth. A non-participant (or unknown conversation) is 404 so a
// conversation's existence is not leaked. Pagination via ?limit/?offset.
func (s *Server) handleListMessages(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "conversation not found")
	}
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	msgs, err := s.messagingsvc.ListMessages(c.Request().Context(), userID, convID, int32(limit), int32(offset))
	if err != nil {
		if errors.Is(err, messaging.ErrNotParticipant) {
			return echo.NewHTTPError(http.StatusNotFound, "conversation not found")
		}
		return err
	}
	views := make([]messageView, 0, len(msgs))
	for _, m := range msgs {
		views = append(views, newMessageView(m))
	}
	return c.JSON(http.StatusOK, messageListResponse{Messages: views, Limit: limit, Offset: offset})
}

// handleSendMessage posts a message to a conversation. Behind requireAuth. A
// non-participant (or unknown conversation) is 404. The response carries the
// sender's identity (the authenticated user).
func (s *Server) handleSendMessage(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "conversation not found")
	}
	var in sendMessageRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	msg, err := s.messagingsvc.SendMessage(ctx, userID, convID, strings.TrimSpace(in.Body))
	if err != nil {
		if errors.Is(err, messaging.ErrNotParticipant) {
			return echo.NewHTTPError(http.StatusNotFound, "conversation not found")
		}
		return err
	}
	view := newMessageView(msg)
	if u, uerr := s.authsvc.UserByID(ctx, userID); uerr == nil {
		view.SenderUsername = u.Username
		view.SenderDisplayName = u.DisplayName
	}
	return c.JSON(http.StatusCreated, view)
}
