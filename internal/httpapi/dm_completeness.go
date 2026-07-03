package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/messaging"
	"github.com/vidra/vidra-core/internal/moderation"
)

// parseUUIDList parses a slice of id strings, erroring on the first malformed one.
func parseUUIDList(raw []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// conversationEncrypted reports whether a conversation is encrypted, when the
// e2ee service is wired. Used to reject plaintext-only operations (attachments,
// tombstone, report) on encrypted conversations with a documented 422.
func (s *Server) conversationEncrypted(c echo.Context, userID, convID uuid.UUID) (bool, error) {
	if s.e2eesvc == nil {
		return false, nil
	}
	return s.e2eesvc.ConversationEncrypted(c.Request().Context(), userID, convID)
}

// uploadAttachmentResponse carries the id a client references on a subsequent send.
type uploadAttachmentResponse struct {
	AttachmentID string `json:"attachment_id"`
	Kind         string `json:"kind"`
	ContentType  string `json:"content_type"`
	Filename     string `json:"filename"`
	SizeBytes    int64  `json:"size_bytes"`
}

// handleUploadAttachment stores a DM attachment (multipart "file") for a
// conversation the caller participates in, returning an id to reference on a
// send. Behind requireAuth. Non-participant/unknown conversation → 404; an
// encrypted conversation → 422 (attachments are ciphertext-external); an
// unsupported type → 415; oversize → 413; a failed malware scan → 422.
func (s *Server) handleUploadAttachment(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if !s.messagingsvc.AttachmentsEnabled() {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "attachments are not available")
	}
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "conversation not found")
	}
	// Attachments are plaintext-only.
	if enc, eerr := s.conversationEncrypted(c, userID, convID); eerr != nil {
		if herr := e2eeError(eerr); herr != nil {
			return herr
		}
		return eerr
	} else if enc {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "attachments are not supported on encrypted conversations")
	}
	fh, err := c.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, `multipart form field "file" is required`)
	}
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	att, err := s.messagingsvc.UploadAttachment(c.Request().Context(), userID, convID, messaging.AttachmentInput{
		Filename:    fh.Filename,
		ContentType: fh.Header.Get("Content-Type"),
		Size:        fh.Size,
		Reader:      f,
	})
	if err != nil {
		switch {
		case errors.Is(err, messaging.ErrNotParticipant):
			return echo.NewHTTPError(http.StatusNotFound, "conversation not found")
		case errors.Is(err, messaging.ErrUnsupportedAttachment):
			return echo.NewHTTPError(http.StatusUnsupportedMediaType, "unsupported attachment type (allowed: image, video, audio, pdf)")
		case errors.Is(err, messaging.ErrAttachmentTooLarge):
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "attachment exceeds the 25 MiB limit")
		case errors.Is(err, messaging.ErrAttachmentRejected):
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "attachment failed the malware scan")
		case errors.Is(err, messaging.ErrAttachmentsUnavailable):
			return echo.NewHTTPError(http.StatusServiceUnavailable, "attachments are not available")
		}
		return err
	}
	return c.JSON(http.StatusCreated, uploadAttachmentResponse{
		AttachmentID: att.ID.String(), Kind: att.Kind, ContentType: att.ContentType,
		Filename: att.Filename, SizeBytes: att.SizeBytes,
	})
}

// handleDownloadAttachment streams a DM attachment's bytes, participant-gated.
// Behind requireAuth. Unknown attachment or a caller who is not a participant of
// its conversation → 404.
func (s *Server) handleDownloadAttachment(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if !s.messagingsvc.AttachmentsEnabled() {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "attachments are not available")
	}
	attID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "attachment not found")
	}
	att, err := s.messagingsvc.AttachmentForDownload(c.Request().Context(), userID, attID)
	if err != nil {
		if errors.Is(err, messaging.ErrAttachmentNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "attachment not found")
		}
		return err
	}
	return s.serveStoredObjectNamed(c, att.StorageKey, att.ContentType, "attachment not found")
}

// markConversationReadRequest optionally pins the watermark to a specific
// message; when absent the newest message is used.
type markConversationReadRequest struct {
	MessageID string `json:"message_id"`
}

// markConversationReadResponse echoes the resulting watermark.
type markConversationReadResponse struct {
	LastReadMessageID string `json:"last_read_message_id,omitempty"`
}

// handleMarkConversationRead advances the caller's read watermark in a
// conversation (idempotent, advance-only). Behind requireAuth. Non-participant/
// unknown conversation → 404; a message_id not in the conversation → 404.
func (s *Server) handleMarkConversationRead(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "conversation not found")
	}
	var in markConversationReadRequest
	// The body is optional; ignore a bind error on an empty body.
	_ = c.Bind(&in)
	var msgPtr *uuid.UUID
	if strings.TrimSpace(in.MessageID) != "" {
		mid, perr := uuid.Parse(strings.TrimSpace(in.MessageID))
		if perr != nil {
			return &ValidationError{Fields: []FieldError{{Field: "message_id", Message: "must be a valid id"}}}
		}
		msgPtr = &mid
	}
	if err := s.messagingsvc.MarkRead(c.Request().Context(), userID, convID, msgPtr); err != nil {
		switch {
		case errors.Is(err, messaging.ErrNotParticipant), errors.Is(err, messaging.ErrMessageNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "conversation not found")
		}
		return err
	}
	// Report the peer-independent caller watermark by re-reading the latest read
	// target is unnecessary; the client knows what it marked. Return 204-style OK.
	return c.NoContent(http.StatusNoContent)
}

// handleDeleteMessage tombstones the caller's own message. Behind requireAuth.
// A non-sender or unknown message → 404; already-deleted is idempotent 204.
func (s *Server) handleDeleteMessage(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	msgID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "message not found")
	}
	if err := s.messagingsvc.DeleteMessage(c.Request().Context(), userID, msgID); err != nil {
		switch {
		case errors.Is(err, messaging.ErrMessageNotFound), errors.Is(err, messaging.ErrNotSender):
			return echo.NewHTTPError(http.StatusNotFound, "message not found")
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleReportMessage files a report against a DM message. Behind requireAuth.
// Either participant may report; a non-participant or unknown message → 404.
// Idempotent per (reporter, message).
func (s *Server) handleReportMessage(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if s.moderationsvc == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "moderation is not available")
	}
	msgID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "message not found")
	}
	var in createReportRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	_, snapshot, err := s.messagingsvc.ReportableMessage(ctx, userID, msgID)
	if err != nil {
		if errors.Is(err, messaging.ErrMessageNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "message not found")
		}
		return err
	}
	if err := s.moderationsvc.ReportMessage(ctx, userID, msgID, snapshot, strings.TrimSpace(in.Reason)); err != nil {
		if errors.Is(err, moderation.ErrInvalidTarget) {
			return echo.NewHTTPError(http.StatusNotFound, "message not found")
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// messagingPrefsView is the caller's DM privacy preferences.
type messagingPrefsView struct {
	ReadReceipts bool `json:"read_receipts"`
}

// updateMessagingPrefsRequest patches DM privacy preferences (only present
// fields change).
type updateMessagingPrefsRequest struct {
	ReadReceipts *bool `json:"read_receipts"`
}

// handleGetMessagingPrefs returns the caller's DM privacy toggles. Behind
// requireAuth. Currently just the read-receipts toggle (default enabled).
func (s *Server) handleGetMessagingPrefs(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	enabled, err := s.messagingsvc.ReadReceiptsEnabled(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, messagingPrefsView{ReadReceipts: enabled})
}

// handleUpdateMessagingPrefs updates the caller's DM privacy toggles. Behind
// requireAuth.
func (s *Server) handleUpdateMessagingPrefs(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in updateMessagingPrefsRequest
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	if in.ReadReceipts != nil {
		if err := s.messagingsvc.SetReadReceiptsEnabled(ctx, userID, *in.ReadReceipts); err != nil {
			return err
		}
	}
	enabled, err := s.messagingsvc.ReadReceiptsEnabled(ctx, userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, messagingPrefsView{ReadReceipts: enabled})
}
