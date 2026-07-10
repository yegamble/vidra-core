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

// startConversationRequest is the POST /conversations body. The recipient is
// named by EXACTLY ONE of recipient_id (a UUID) or recipient_username (resolved
// server-side, case-insensitive, active accounts only) — both or neither is a
// 422. encrypted chooses the conversation type AT CREATION, immutably: an
// encrypted conversation stores only opaque ciphertext envelopes (see
// internal/e2ee), a plaintext one only {body} messages. The two live in
// distinct dm_key namespaces, so a pair of users can have one of each.
type startConversationRequest struct {
	RecipientID       string `json:"recipient_id"`
	RecipientUsername string `json:"recipient_username"`
	Encrypted         bool   `json:"encrypted"`
}

func (r startConversationRequest) Validate() []FieldError {
	id := strings.TrimSpace(r.RecipientID)
	username := strings.TrimSpace(r.RecipientUsername)
	switch {
	case id == "" && username == "":
		return []FieldError{{Field: "recipient_id", Message: "provide recipient_id or recipient_username"}}
	case id != "" && username != "":
		return []FieldError{{Field: "recipient_username", Message: "provide only one of recipient_id or recipient_username"}}
	}
	return nil
}

// conversationView is the projection of a conversation.
type conversationView struct {
	ID          string    `json:"id"`
	OtherUserID string    `json:"other_user_id"`
	Encrypted   bool      `json:"encrypted"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// conversationSummaryView is a conversation in the inbox list: the other
// participant + last message preview (empty for encrypted conversations —
// envelopes are opaque).
type conversationSummaryView struct {
	ID               string    `json:"id"`
	UpdatedAt        time.Time `json:"updated_at"`
	Encrypted        bool      `json:"encrypted"`
	OtherUserID      string    `json:"other_user_id"`
	OtherUsername    string    `json:"other_username"`
	OtherDisplayName string    `json:"other_display_name"`
	OtherHasAvatar   bool      `json:"other_has_avatar,omitempty"`
	LastMessageBody  string    `json:"last_message_body"`
	LastMessageAt    time.Time `json:"last_message_at"`
	UnreadCount      int64     `json:"unread_count"`
}

type conversationListResponse struct {
	Conversations []conversationSummaryView `json:"conversations"`
	Limit         int                       `json:"limit"`
	Offset        int                       `json:"offset"`
}

// envelopeIn is one per-recipient-device ciphertext blob of an encrypted send.
type envelopeIn struct {
	RecipientDeviceID string `json:"recipient_device_id"`
	MessageType       int32  `json:"message_type"`
	Ciphertext        string `json:"ciphertext"`
}

// maxEnvelopesPerSend bounds the client fan-out of one encrypted send (each
// participant has at most 20 devices, so 40 covers every legitimate send).
const maxEnvelopesPerSend = 40

// sendMessageRequest is the POST /conversations/{id}/messages body. Exactly
// one shape is valid per conversation type: plaintext conversations take
// {body}; encrypted conversations take {sender_device_id, envelopes,
// expires_in_seconds?} (the shape check against the conversation's type
// happens in the handler; mixing shapes is rejected here).
type sendMessageRequest struct {
	Body             string       `json:"body"`
	AttachmentIDs    []string     `json:"attachment_ids"`
	SenderDeviceID   string       `json:"sender_device_id"`
	Envelopes        []envelopeIn `json:"envelopes"`
	ExpiresInSeconds *int64       `json:"expires_in_seconds"`
}

func (r sendMessageRequest) Validate() []FieldError {
	body := strings.TrimSpace(r.Body)
	if len(r.Envelopes) == 0 {
		// Plaintext shape. A message needs a body OR at least one attachment.
		switch {
		case body == "" && len(r.AttachmentIDs) == 0:
			return []FieldError{{Field: "body", Message: "is required"}}
		case len(body) > maxMessageLen:
			return []FieldError{{Field: "body", Message: "must be at most 5000 characters"}}
		case len(r.AttachmentIDs) > messaging.MaxAttachmentsPerMessage:
			return []FieldError{{Field: "attachment_ids", Message: "at most 30 attachments"}}
		case r.SenderDeviceID != "" || r.ExpiresInSeconds != nil:
			return []FieldError{{Field: "body", Message: "cannot be combined with encrypted-send fields"}}
		}
		return nil
	}
	// Encrypted shape.
	var errs []FieldError
	if body != "" {
		errs = append(errs, FieldError{Field: "body", Message: "cannot be combined with envelopes"})
	}
	if len(r.AttachmentIDs) > 0 {
		errs = append(errs, FieldError{Field: "attachment_ids", Message: "attachments are not supported on encrypted conversations"})
	}
	if strings.TrimSpace(r.SenderDeviceID) == "" {
		errs = append(errs, FieldError{Field: "sender_device_id", Message: "is required with envelopes"})
	}
	if len(r.Envelopes) > maxEnvelopesPerSend {
		errs = append(errs, FieldError{Field: "envelopes", Message: "too many envelopes"})
	}
	for _, env := range r.Envelopes {
		if strings.TrimSpace(env.RecipientDeviceID) == "" || env.Ciphertext == "" {
			errs = append(errs, FieldError{Field: "envelopes", Message: "each envelope needs recipient_device_id and ciphertext"})
			break
		}
	}
	return errs
}

// attachmentView is a DM attachment on a message (metadata only; the bytes are
// served participant-gated at GET /api/v1/attachments/{id}).
type attachmentView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
	Filename    string `json:"filename"`
	SizeBytes   int64  `json:"size_bytes"`
	// Width/Height are the probed intrinsic pixel dimensions, present only for
	// kind=image when the probe succeeded.
	Width  *int32 `json:"width,omitempty"`
	Height *int32 `json:"height,omitempty"`
}

// linkPreviewView is the OpenGraph preview joined onto a message when the async
// fetch has resolved to ready. Any field may be empty.
type linkPreviewView struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Image       string `json:"image,omitempty"`
}

// messageView is the projection of a message.
type messageView struct {
	ID                string           `json:"id"`
	ConversationID    string           `json:"conversation_id"`
	SenderID          string           `json:"sender_id"`
	SenderUsername    string           `json:"sender_username,omitempty"`
	SenderDisplayName string           `json:"sender_display_name,omitempty"`
	SenderHasAvatar   bool             `json:"sender_has_avatar,omitempty"`
	Body              string           `json:"body"`
	Deleted           bool             `json:"deleted,omitempty"`
	CreatedAt         time.Time        `json:"created_at"`
	Attachments       []attachmentView `json:"attachments,omitempty"`
	Preview           *linkPreviewView `json:"preview,omitempty"`
}

func newMessageView(m messaging.Message) messageView {
	v := messageView{
		ID:                m.ID.String(),
		ConversationID:    m.ConversationID.String(),
		SenderID:          m.SenderID.String(),
		SenderUsername:    m.SenderUsername,
		SenderDisplayName: m.SenderDisplayName,
		SenderHasAvatar:   m.SenderHasAvatar,
		Body:              m.Body,
		Deleted:           m.Deleted,
		CreatedAt:         m.CreatedAt,
	}
	for _, a := range m.Attachments {
		v.Attachments = append(v.Attachments, attachmentView{
			ID: a.ID.String(), Kind: a.Kind, ContentType: a.ContentType,
			Filename: a.Filename, SizeBytes: a.SizeBytes,
			Width: a.Width, Height: a.Height,
		})
	}
	if m.Preview != nil {
		v.Preview = &linkPreviewView{
			URL: m.Preview.URL, Title: m.Preview.Title,
			Description: m.Preview.Description, Image: m.Preview.Image,
		}
	}
	return v
}

type messageListResponse struct {
	Messages []messageView `json:"messages"`
	// PeerLastReadMessageID is the other participant's read watermark (the newest
	// message id they have read), for a "seen" indicator. Omitted when the peer
	// has read nothing or disabled read receipts.
	PeerLastReadMessageID string `json:"peer_last_read_message_id,omitempty"`
	Limit                 int    `json:"limit"`
	Offset                int    `json:"offset"`
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
	// Resolve the recipient to a user id. Validate() guarantees exactly one of
	// recipient_id / recipient_username is set. A username is resolved
	// server-side (case-insensitive, active-only); an unknown/inactive username
	// 404s exactly like an unknown recipient_id, so existence is not leaked
	// differently. Self, block, and encrypted-type handling below are unchanged
	// and operate on the resolved id (self → 422, block → 403).
	var recipientID uuid.UUID
	if uname := strings.TrimSpace(in.RecipientUsername); uname != "" {
		resolved, rerr := s.messagingsvc.ResolveRecipientUsername(c.Request().Context(), uname)
		if rerr != nil {
			if errors.Is(rerr, messaging.ErrRecipientNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "recipient not found")
			}
			return rerr
		}
		recipientID = resolved
	} else {
		parsed, perr := uuid.Parse(strings.TrimSpace(in.RecipientID))
		if perr != nil {
			return &ValidationError{Fields: []FieldError{{Field: "recipient_id", Message: "must be a valid id"}}}
		}
		recipientID = parsed
	}
	// Encrypted conversations live in the e2ee service (distinct dm_key
	// namespace, ciphertext-only storage); everything else is unchanged.
	if in.Encrypted {
		if s.e2eesvc == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "encrypted messaging is not available")
		}
		conv, err := s.e2eesvc.StartConversation(c.Request().Context(), userID, recipientID)
		if err != nil {
			if herr := e2eeError(err); herr != nil {
				return herr
			}
			return err
		}
		return c.JSON(http.StatusCreated, conversationView{
			ID: conv.ID.String(), OtherUserID: recipientID.String(), Encrypted: true,
			CreatedAt: conv.CreatedAt, UpdatedAt: conv.UpdatedAt,
		})
	}
	conv, err := s.messagingsvc.StartConversation(c.Request().Context(), userID, recipientID)
	if err != nil {
		switch {
		case errors.Is(err, messaging.ErrCannotMessageSelf):
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "cannot message yourself")
		case errors.Is(err, messaging.ErrRecipientNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "recipient not found")
		case errors.Is(err, messaging.ErrBlocked):
			return echo.NewHTTPError(http.StatusForbidden, "cannot message this user")
		}
		return err
	}
	return c.JSON(http.StatusCreated, conversationView{
		ID: conv.ID.String(), OtherUserID: recipientID.String(), Encrypted: false,
		CreatedAt: conv.CreatedAt, UpdatedAt: conv.UpdatedAt,
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
			ID: it.ID.String(), UpdatedAt: it.UpdatedAt, Encrypted: it.Encrypted, OtherUserID: it.OtherUserID.String(),
			OtherUsername: it.OtherUsername, OtherDisplayName: it.OtherDisplayName,
			OtherHasAvatar:  it.OtherHasAvatar,
			LastMessageBody: it.LastMessageBody, LastMessageAt: it.LastMessageAt,
			UnreadCount: it.UnreadCount,
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
	// Optional keyset cursor: return only messages strictly older than before_id
	// (stable upward history paging). Mutually exclusive with offset — both is a
	// 422; a malformed id is a 422. Unknown/foreign cursors 422 in the service.
	beforeRaw := strings.TrimSpace(c.QueryParam("before_id"))
	var beforeID uuid.UUID
	useKeyset := false
	if beforeRaw != "" {
		if strings.TrimSpace(c.QueryParam("offset")) != "" {
			return &ValidationError{Fields: []FieldError{{Field: "before_id", Message: "cannot be combined with offset"}}}
		}
		parsed, perr := uuid.Parse(beforeRaw)
		if perr != nil {
			return &ValidationError{Fields: []FieldError{{Field: "before_id", Message: "must be a valid id"}}}
		}
		beforeID, useKeyset = parsed, true
	}
	ctx := c.Request().Context()
	// Encrypted conversations return only the CALLER's devices' envelopes;
	// plaintext ones are exactly as before E2EE existed. The participant check
	// runs first either way, so existence is never leaked.
	if s.e2eesvc != nil {
		encrypted, err := s.e2eesvc.ConversationEncrypted(ctx, userID, convID)
		if err != nil {
			if herr := e2eeError(err); herr != nil {
				return herr
			}
			return err
		}
		if encrypted {
			if useKeyset {
				return s.listEncryptedMessagesBefore(c, userID, convID, beforeID, limit)
			}
			return s.listEncryptedMessages(c, userID, convID, limit, offset)
		}
	}
	var msgs []messaging.Message
	if useKeyset {
		msgs, err = s.messagingsvc.ListMessagesBefore(ctx, userID, convID, beforeID, int32(limit))
	} else {
		msgs, err = s.messagingsvc.ListMessages(ctx, userID, convID, int32(limit), int32(offset))
	}
	if err != nil {
		switch {
		case errors.Is(err, messaging.ErrNotParticipant):
			return echo.NewHTTPError(http.StatusNotFound, "conversation not found")
		case errors.Is(err, messaging.ErrInvalidCursor):
			return &ValidationError{Fields: []FieldError{{Field: "before_id", Message: "must reference a message in this conversation"}}}
		}
		return err
	}
	if useKeyset {
		offset = 0 // keyset paging has no offset
	}
	views := make([]messageView, 0, len(msgs))
	for _, m := range msgs {
		views = append(views, newMessageView(m))
	}
	resp := messageListResponse{Messages: views, Limit: limit, Offset: offset}
	// Expose the peer's read watermark for a "seen" indicator (hidden when they
	// have read nothing or disabled read receipts).
	if wm, ok, werr := s.messagingsvc.PeerReadWatermark(c.Request().Context(), userID, convID); werr == nil && ok {
		resp.PeerLastReadMessageID = wm.String()
	}
	return c.JSON(http.StatusOK, resp)
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
	// Route by the conversation's type (chosen at creation, immutable): an
	// encrypted conversation takes ONLY the envelopes shape, a plaintext one
	// ONLY {body} — the wrong shape for the type is 422 in both directions.
	// The participant check runs before the shape check so a non-participant
	// stays 404 (no existence leak).
	if s.e2eesvc != nil {
		encrypted, err := s.e2eesvc.ConversationEncrypted(ctx, userID, convID)
		if err != nil {
			if herr := e2eeError(err); herr != nil {
				return herr
			}
			return err
		}
		if encrypted {
			if len(in.Envelopes) == 0 {
				return &ValidationError{Fields: []FieldError{{Field: "envelopes", Message: "this conversation is encrypted; send envelopes, not body"}}}
			}
			return s.sendEncryptedMessage(c, userID, convID, in)
		}
		if len(in.Envelopes) > 0 {
			return &ValidationError{Fields: []FieldError{{Field: "body", Message: "this conversation is not encrypted; send body, not envelopes"}}}
		}
	} else if len(in.Envelopes) > 0 {
		return &ValidationError{Fields: []FieldError{{Field: "envelopes", Message: "encrypted messaging is not available"}}}
	}
	attachmentIDs, perr := parseUUIDList(in.AttachmentIDs)
	if perr != nil {
		return &ValidationError{Fields: []FieldError{{Field: "attachment_ids", Message: "must be valid ids"}}}
	}
	msg, err := s.messagingsvc.SendMessage(ctx, userID, convID, strings.TrimSpace(in.Body), attachmentIDs)
	if err != nil {
		switch {
		case errors.Is(err, messaging.ErrNotParticipant):
			return echo.NewHTTPError(http.StatusNotFound, "conversation not found")
		case errors.Is(err, messaging.ErrBlocked):
			return echo.NewHTTPError(http.StatusForbidden, "cannot message this user")
		case errors.Is(err, messaging.ErrInvalidAttachments):
			return &ValidationError{Fields: []FieldError{{Field: "attachment_ids", Message: "must reference your own uploads in this conversation, not yet sent"}}}
		}
		return err
	}
	view := newMessageView(msg)
	if u, uerr := s.authsvc.UserByID(ctx, userID); uerr == nil {
		view.SenderUsername = u.Username
		view.SenderDisplayName = u.DisplayName
	}
	// Best-effort: notify the recipient of the new message. A failure here must
	// never fail the send (the message is already persisted).
	if s.notifsvc != nil {
		if other, oerr := s.messagingsvc.OtherParticipant(ctx, convID, userID); oerr == nil {
			if nerr := s.notifsvc.NotifyMessage(ctx, other, userID, convID); nerr != nil {
				s.logger.WarnContext(ctx, "notify message failed", "error", nerr, "conversation_id", convID)
			}
		}
	}
	return c.JSON(http.StatusCreated, view)
}
