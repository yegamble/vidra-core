// Package messaging implements normal (non-E2EE) direct messaging for vidra-core:
// 1:1 conversations and plaintext messages. Encrypted messaging (ciphertext-only)
// is a separate later slice (fix_plan P11.2). It is HTTP-agnostic and testable
// without a server.
package messaging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"mime"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/linkpreview"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrCannotMessageSelf means a user tried to start a conversation with themselves.
	ErrCannotMessageSelf = errors.New("messaging: cannot message yourself")
	// ErrRecipientNotFound means the target account does not exist.
	ErrRecipientNotFound = errors.New("messaging: recipient not found")
	// ErrNotParticipant means the caller is not a member of the conversation.
	ErrNotParticipant = errors.New("messaging: not a participant")
	// ErrBlocked means a block exists between the two users (in either
	// direction), so they cannot message each other.
	ErrBlocked = errors.New("messaging: blocked")
	// ErrMessageNotFound means no message matches the id.
	ErrMessageNotFound = errors.New("messaging: message not found")
	// ErrNotSender means the caller is not the sender of the message (delete is
	// sender-only). The HTTP layer maps it to 404 so authorship is not leaked.
	ErrNotSender = errors.New("messaging: not the sender")
	// ErrAttachmentsUnavailable means attachment storage is not configured.
	ErrAttachmentsUnavailable = errors.New("messaging: attachments unavailable")
	// ErrUnsupportedAttachment means the file type is not on the allowlist.
	ErrUnsupportedAttachment = errors.New("messaging: unsupported attachment type")
	// ErrAttachmentTooLarge means the upload exceeded the size cap.
	ErrAttachmentTooLarge = errors.New("messaging: attachment too large")
	// ErrAttachmentRejected means the malware scan failed or found an infection
	// (fail-closed).
	ErrAttachmentRejected = errors.New("messaging: attachment failed malware scan")
	// ErrInvalidAttachments means a send referenced attachments that are not the
	// caller's own, not in this conversation, already attached, too many, or
	// unknown.
	ErrInvalidAttachments = errors.New("messaging: invalid attachment reference")
	// ErrAttachmentNotFound means no attachment matches the id for this caller.
	ErrAttachmentNotFound = errors.New("messaging: attachment not found")
)

// MaxAttachmentBytes is the default per-attachment size cap (25 MiB, per
// product-decisions.md §14).
const MaxAttachmentBytes int64 = 25 << 20

// MaxAttachmentsPerMessage bounds how many attachments one send may reference.
const MaxAttachmentsPerMessage = 4

// readReceiptsPrefType is the notification_prefs pseudo-type that stores a user's
// read-receipts privacy toggle (product-decisions.md §14 — reuse over a
// dedicated column). Absence of a row means the default: enabled.
const readReceiptsPrefType = "read_receipts"

// attachmentKinds maps allowed MIME type prefixes/values to a coarse kind. Only
// images, video, audio, and PDF are accepted.
func attachmentKind(contentType, filename string) (string, bool) {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch {
	case ct == "application/pdf":
		return "pdf", true
	case strings.HasPrefix(ct, "image/"):
		return "image", true
	case strings.HasPrefix(ct, "video/"):
		return "video", true
	case strings.HasPrefix(ct, "audio/"):
		return "audio", true
	}
	// Fall back to the filename extension when the client sent a generic type.
	switch strings.ToLower(strings.TrimPrefix(path.Ext(filename), ".")) {
	case "pdf":
		return "pdf", true
	case "jpg", "jpeg", "png", "gif", "webp", "avif":
		return "image", true
	case "mp4", "webm", "mov", "m4v":
		return "video", true
	case "mp3", "m4a", "aac", "ogg", "oga", "wav", "flac":
		return "audio", true
	}
	return "", false
}

// attachmentExt derives a safe storage-key extension from the content type
// (preferred) or filename, defaulting to "bin".
func attachmentExt(contentType, filename string) string {
	if exts, _ := mime.ExtensionsByType(contentType); len(exts) > 0 {
		return strings.TrimPrefix(exts[0], ".")
	}
	if e := strings.ToLower(strings.TrimPrefix(path.Ext(filename), ".")); e != "" && isSafeExt(e) {
		return e
	}
	return "bin"
}

func isSafeExt(e string) bool {
	for _, r := range e {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return len(e) > 0 && len(e) <= 8
}

// Blocker reports whether a block exists between two users (in either
// direction). *block.Service satisfies it. When wired, messaging refuses to
// start a conversation with, or send a message to, a user on either side of a
// block. Optional: nil means no block enforcement.
type Blocker interface {
	IsBlockedBetween(ctx context.Context, a, b uuid.UUID) (bool, error)
}

// Repository is the data access the messaging service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateConversation(ctx context.Context, dmKey *string) (sqlcgen.CreateConversationRow, error)
	GetConversationByDMKey(ctx context.Context, dmKey *string) (sqlcgen.GetConversationByDMKeyRow, error)
	AddConversationParticipant(ctx context.Context, arg sqlcgen.AddConversationParticipantParams) error
	IsConversationParticipant(ctx context.Context, arg sqlcgen.IsConversationParticipantParams) (bool, error)
	CreateMessage(ctx context.Context, arg sqlcgen.CreateMessageParams) (sqlcgen.CreateMessageRow, error)
	GetOtherParticipant(ctx context.Context, arg sqlcgen.GetOtherParticipantParams) (uuid.UUID, error)
	TouchConversation(ctx context.Context, id uuid.UUID) error
	ListMessages(ctx context.Context, arg sqlcgen.ListMessagesParams) ([]sqlcgen.ListMessagesRow, error)
	ListConversations(ctx context.Context, arg sqlcgen.ListConversationsParams) ([]sqlcgen.ListConversationsRow, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error)
	// DM completeness (§14).
	GetMessage(ctx context.Context, id uuid.UUID) (sqlcgen.GetMessageRow, error)
	GetLatestMessageID(ctx context.Context, conversationID uuid.UUID) (uuid.UUID, error)
	SetLastReadMessage(ctx context.Context, arg sqlcgen.SetLastReadMessageParams) error
	GetPeerReadWatermark(ctx context.Context, arg sqlcgen.GetPeerReadWatermarkParams) (pgtype.UUID, error)
	TombstoneMessage(ctx context.Context, arg sqlcgen.TombstoneMessageParams) (int64, error)
	CreateMessageAttachment(ctx context.Context, arg sqlcgen.CreateMessageAttachmentParams) (sqlcgen.MessageAttachment, error)
	GetMessageAttachment(ctx context.Context, id uuid.UUID) (sqlcgen.MessageAttachment, error)
	ListOwnedUnlinkedAttachments(ctx context.Context, arg sqlcgen.ListOwnedUnlinkedAttachmentsParams) ([]sqlcgen.ListOwnedUnlinkedAttachmentsRow, error)
	LinkAttachmentsToMessage(ctx context.Context, arg sqlcgen.LinkAttachmentsToMessageParams) error
	ListAttachmentsForMessages(ctx context.Context, messageIds []uuid.UUID) ([]sqlcgen.ListAttachmentsForMessagesRow, error)
	ListAttachmentKeysByMessage(ctx context.Context, messageID pgtype.UUID) ([]sqlcgen.ListAttachmentKeysByMessageRow, error)
	DeleteAttachmentsByMessage(ctx context.Context, messageID pgtype.UUID) error
	UpsertPendingLinkPreview(ctx context.Context, arg sqlcgen.UpsertPendingLinkPreviewParams) error
	SetMessageLinkPreview(ctx context.Context, arg sqlcgen.SetMessageLinkPreviewParams) error
	GetLinkPreview(ctx context.Context, urlHash string) (sqlcgen.LinkPreview, error)
	ResolveLinkPreview(ctx context.Context, arg sqlcgen.ResolveLinkPreviewParams) error
	IsNotificationTypeEnabled(ctx context.Context, arg sqlcgen.IsNotificationTypeEnabledParams) (bool, error)
	UpsertNotificationPref(ctx context.Context, arg sqlcgen.UpsertNotificationPrefParams) error
}

// Scanner checks a stored attachment for malware before it is accepted. It has
// the same shape as video.Scanner (e.g. internal/media.ClamAV). When wired,
// attachment uploads are scanned fail-closed.
type Scanner interface {
	Scan(ctx context.Context, storageKey string) (clean bool, err error)
}

// PreviewFetcher fetches OpenGraph link-preview metadata for a URL through an
// SSRF-guarded client. *linkpreview.Fetcher satisfies it. Optional; nil disables
// link previews.
type PreviewFetcher interface {
	Fetch(ctx context.Context, url string) (title, description, image string, err error)
}

// Service holds the messaging application logic.
type Service struct {
	repo    Repository
	blocker Blocker // optional; nil disables block enforcement
	logger  *slog.Logger

	// Attachments (optional; blobs==nil disables the attachment endpoints).
	blobs     storage.Backend
	scanner   Scanner // optional; when set, uploads are scanned fail-closed
	maxAttach int64

	// Link previews (optional; previews==nil disables preview fetching).
	previews  PreviewFetcher
	previewWG sync.WaitGroup // lets tests await in-flight best-effort fetches
}

// Option configures the messaging service.
type Option func(*Service)

// WithBlocker enables block enforcement: a conversation cannot be started with,
// or a message sent to, a user on either side of a block.
func WithBlocker(b Blocker) Option {
	return func(s *Service) { s.blocker = b }
}

// WithLogger sets the structured logger (defaults to slog.Default()).
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithAttachments enables DM attachments backed by blobs, scanned fail-closed by
// scanner when non-nil. maxBytes<=0 uses MaxAttachmentBytes.
func WithAttachments(blobs storage.Backend, scanner Scanner, maxBytes int64) Option {
	return func(s *Service) {
		s.blobs = blobs
		s.scanner = scanner
		if maxBytes > 0 {
			s.maxAttach = maxBytes
		}
	}
}

// WithPreviews enables best-effort, async link-preview fetching on plaintext
// sends via an SSRF-guarded fetcher.
func WithPreviews(f PreviewFetcher) Option {
	return func(s *Service) { s.previews = f }
}

// NewService builds the messaging service.
func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{repo: repo, logger: slog.Default(), maxAttach: MaxAttachmentBytes}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// AttachmentsEnabled reports whether attachment storage is configured.
func (s *Service) AttachmentsEnabled() bool { return s.blobs != nil }

// WaitForPreviews blocks until every in-flight best-effort preview fetch has
// finished. Test-only helper; production never needs to wait.
func (s *Service) WaitForPreviews() { s.previewWG.Wait() }

// Conversation is a 1:1 thread.
type Conversation struct {
	ID        uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Summary is a conversation as shown in the caller's inbox: the other
// participant and the last message preview. Encrypted conversations appear
// too (Encrypted true) with an empty preview — envelopes are opaque.
type Summary struct {
	ID               uuid.UUID
	UpdatedAt        time.Time
	Encrypted        bool
	OtherUserID      uuid.UUID
	OtherUsername    string
	OtherDisplayName string
	LastMessageBody  string // "" when there are no messages yet
	LastMessageAt    time.Time
	UnreadCount      int64 // caller's unread messages (peer messages past their watermark)
}

// Attachment is a DM attachment as shown on a message: coarse kind + metadata.
// The bytes are served separately (participant-gated) at
// GET /api/v1/attachments/{id}.
type Attachment struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	Kind           string // image | video | audio | pdf
	ContentType    string
	Filename       string
	SizeBytes      int64
	StorageKey     string // internal; not exposed in views
}

// LinkPreview is the OpenGraph preview joined onto a message, present only once
// the async fetch has resolved to ready.
type LinkPreview struct {
	URL         string
	Title       string
	Description string
	Image       string
}

// Message is a single message. SenderUsername/SenderDisplayName are populated
// when listing; on send the caller supplies the sender's identity. Deleted is
// true for a tombstoned message (body is "[deleted]"). Attachments and Preview
// are populated on list views.
type Message struct {
	ID                uuid.UUID
	ConversationID    uuid.UUID
	SenderID          uuid.UUID
	SenderUsername    string
	SenderDisplayName string
	Body              string
	CreatedAt         time.Time
	Deleted           bool
	Attachments       []Attachment
	Preview           *LinkPreview
}

// isBlocked reports whether a block exists between the two users. Always false
// when no Blocker is wired.
func (s *Service) isBlocked(ctx context.Context, a, b uuid.UUID) (bool, error) {
	if s.blocker == nil {
		return false, nil
	}
	return s.blocker.IsBlockedBetween(ctx, a, b)
}

// dmKey is the canonical, order-independent key for a 1:1 conversation.
func dmKey(a, b uuid.UUID) string {
	x, y := a.String(), b.String()
	if x > y {
		x, y = y, x
	}
	return x + ":" + y
}

// StartConversation returns the 1:1 conversation between the caller and the
// recipient, creating it (with both participants) if it does not exist.
// Messaging yourself → ErrCannotMessageSelf; an unknown recipient →
// ErrRecipientNotFound.
func (s *Service) StartConversation(ctx context.Context, meID, recipientID uuid.UUID) (Conversation, error) {
	if meID == recipientID {
		return Conversation{}, ErrCannotMessageSelf
	}
	if _, err := s.repo.GetUserByID(ctx, recipientID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Conversation{}, ErrRecipientNotFound
		}
		return Conversation{}, err
	}
	if blocked, err := s.isBlocked(ctx, meID, recipientID); err != nil {
		return Conversation{}, err
	} else if blocked {
		return Conversation{}, ErrBlocked
	}

	key := dmKey(meID, recipientID)
	var conv Conversation
	row, err := s.repo.CreateConversation(ctx, &key)
	switch {
	case errors.Is(err, pgx.ErrNoRows): // already exists
		existing, gerr := s.repo.GetConversationByDMKey(ctx, &key)
		if gerr != nil {
			return Conversation{}, gerr
		}
		conv = Conversation{ID: existing.ID, CreatedAt: existing.CreatedAt, UpdatedAt: existing.UpdatedAt}
	case err != nil:
		return Conversation{}, err
	default:
		conv = Conversation{ID: row.ID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	}

	for _, uid := range []uuid.UUID{meID, recipientID} {
		if err := s.repo.AddConversationParticipant(ctx, sqlcgen.AddConversationParticipantParams{
			ConversationID: conv.ID,
			UserID:         uid,
		}); err != nil {
			return Conversation{}, err
		}
	}
	return conv, nil
}

// ListConversations returns the caller's conversations, most-recently-active
// first. The caller clamps limit/offset.
func (s *Service) ListConversations(ctx context.Context, meID uuid.UUID, limit, offset int32) ([]Summary, error) {
	rows, err := s.repo.ListConversations(ctx, sqlcgen.ListConversationsParams{
		UserID:       meID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(rows))
	for _, r := range rows {
		out = append(out, Summary{
			ID: r.ID, UpdatedAt: r.UpdatedAt, Encrypted: r.Encrypted, OtherUserID: r.OtherUserID,
			OtherUsername: r.OtherUsername, OtherDisplayName: r.OtherDisplayName,
			LastMessageBody: r.LastMessageBody, LastMessageAt: r.LastMessageAt,
			UnreadCount: r.UnreadCount,
		})
	}
	return out, nil
}

// SendMessage posts a message to a conversation. The caller must be a
// participant, else ErrNotParticipant. attachmentIDs (<=4) must be the caller's
// own still-unlinked uploads in this conversation, else ErrInvalidAttachments —
// validated BEFORE the message is created so a bad reference never leaves a
// half-sent message. On success the first URL in the body kicks off a
// best-effort async link-preview fetch.
func (s *Service) SendMessage(ctx context.Context, meID, conversationID uuid.UUID, body string, attachmentIDs []uuid.UUID) (Message, error) {
	member, err := s.repo.IsConversationParticipant(ctx, sqlcgen.IsConversationParticipantParams{
		ConversationID: conversationID,
		UserID:         meID,
	})
	if err != nil {
		return Message{}, err
	}
	if !member {
		return Message{}, ErrNotParticipant
	}
	// Refuse to send if a block exists between the sender and the other
	// participant (in either direction).
	if s.blocker != nil {
		if other, oerr := s.repo.GetOtherParticipant(ctx, sqlcgen.GetOtherParticipantParams{
			ConversationID: conversationID,
			UserID:         meID,
		}); oerr == nil {
			if blocked, berr := s.blocker.IsBlockedBetween(ctx, meID, other); berr != nil {
				return Message{}, berr
			} else if blocked {
				return Message{}, ErrBlocked
			}
		}
	}
	// Validate attachment references up front (own-uploaded, same conversation,
	// unlinked, <=4). A mismatch means at least one id was invalid.
	if len(attachmentIDs) > MaxAttachmentsPerMessage {
		return Message{}, ErrInvalidAttachments
	}
	if len(attachmentIDs) > 0 {
		owned, oerr := s.repo.ListOwnedUnlinkedAttachments(ctx, sqlcgen.ListOwnedUnlinkedAttachmentsParams{
			Ids: attachmentIDs, UploaderID: meID, ConversationID: conversationID,
		})
		if oerr != nil {
			return Message{}, oerr
		}
		if len(owned) != len(attachmentIDs) {
			return Message{}, ErrInvalidAttachments
		}
	}
	m, err := s.repo.CreateMessage(ctx, sqlcgen.CreateMessageParams{
		ConversationID: conversationID,
		SenderID:       meID,
		Body:           body,
	})
	if err != nil {
		return Message{}, err
	}
	msg := Message{
		ID: m.ID, ConversationID: m.ConversationID, SenderID: m.SenderID,
		Body: m.Body, CreatedAt: m.CreatedAt,
	}
	if len(attachmentIDs) > 0 {
		if lerr := s.repo.LinkAttachmentsToMessage(ctx, sqlcgen.LinkAttachmentsToMessageParams{
			MessageID:      pgUUID(m.ID),
			Ids:            attachmentIDs,
			UploaderID:     meID,
			ConversationID: conversationID,
		}); lerr != nil {
			s.logger.WarnContext(ctx, "link attachments failed", "error", lerr, "message_id", m.ID)
		}
		msg.Attachments = s.attachmentsForMessages(ctx, []uuid.UUID{m.ID})[m.ID]
	}
	// Bump the conversation so it sorts to the top of both inboxes (best-effort).
	_ = s.repo.TouchConversation(ctx, conversationID)
	// Best-effort, async link preview of the first URL in the body.
	s.launchPreview(m.ID, body)
	return msg, nil
}

// OtherParticipant returns the other member of a 1:1 conversation (whoever isn't
// meID). Used to address a new-message notification to the recipient. Returns
// pgx.ErrNoRows if there is no other participant.
func (s *Service) OtherParticipant(ctx context.Context, conversationID, meID uuid.UUID) (uuid.UUID, error) {
	return s.repo.GetOtherParticipant(ctx, sqlcgen.GetOtherParticipantParams{
		ConversationID: conversationID,
		UserID:         meID,
	})
}

// ListMessages returns a conversation's messages, newest first. The caller must
// be a participant, else ErrNotParticipant.
func (s *Service) ListMessages(ctx context.Context, meID, conversationID uuid.UUID, limit, offset int32) ([]Message, error) {
	member, err := s.repo.IsConversationParticipant(ctx, sqlcgen.IsConversationParticipantParams{
		ConversationID: conversationID,
		UserID:         meID,
	})
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrNotParticipant
	}
	rows, err := s.repo.ListMessages(ctx, sqlcgen.ListMessagesParams{
		ConversationID: conversationID,
		ResultLimit:    limit,
		ResultOffset:   offset,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	attByMsg := s.attachmentsForMessages(ctx, ids)
	out := make([]Message, 0, len(rows))
	for _, r := range rows {
		m := Message{
			ID: r.ID, ConversationID: r.ConversationID, SenderID: r.SenderID,
			SenderUsername: r.SenderUsername, SenderDisplayName: r.SenderDisplayName,
			Body: r.Body, CreatedAt: r.CreatedAt, Deleted: r.Deleted,
			Attachments: attByMsg[r.ID],
		}
		// A tombstoned message never surfaces stale attachments/preview.
		if !r.Deleted {
			if r.PreviewUrl != nil {
				m.Preview = &LinkPreview{
					URL: deref(r.PreviewUrl), Title: deref(r.PreviewTitle),
					Description: deref(r.PreviewDescription), Image: deref(r.PreviewImage),
				}
			}
		} else {
			m.Attachments = nil
		}
		out = append(out, m)
	}
	return out, nil
}

// attachmentsForMessages groups attachments by message id for the given page of
// message ids (best-effort: a lookup error yields no attachments).
func (s *Service) attachmentsForMessages(ctx context.Context, ids []uuid.UUID) map[uuid.UUID][]Attachment {
	out := map[uuid.UUID][]Attachment{}
	if len(ids) == 0 {
		return out
	}
	rows, err := s.repo.ListAttachmentsForMessages(ctx, ids)
	if err != nil {
		s.logger.WarnContext(ctx, "list attachments failed", "error", err)
		return out
	}
	for _, r := range rows {
		mid := uuidFromPG(r.MessageID)
		out[mid] = append(out[mid], Attachment{
			ID: r.ID, Kind: r.Kind, ContentType: r.ContentType,
			Filename: r.Filename, SizeBytes: r.SizeBytes,
		})
	}
	return out
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

func uuidFromPG(p pgtype.UUID) uuid.UUID {
	if !p.Valid {
		return uuid.Nil
	}
	return p.Bytes
}

// AttachmentInput is one file being uploaded as a DM attachment.
type AttachmentInput struct {
	Filename    string
	ContentType string
	Size        int64 // client-declared size (from the multipart header); may be 0
	Reader      io.Reader
}

// UploadAttachment stores a participant's attachment for a conversation,
// UNLINKED (referenced later on send). The caller must be a participant
// (ErrNotParticipant → 404). The file must be an allowed kind
// (ErrUnsupportedAttachment), within the size cap (ErrAttachmentTooLarge), and
// pass the malware scan when configured (ErrAttachmentRejected, fail-closed).
// The caller is responsible for rejecting encrypted conversations.
func (s *Service) UploadAttachment(ctx context.Context, meID, conversationID uuid.UUID, in AttachmentInput) (Attachment, error) {
	if s.blobs == nil {
		return Attachment{}, ErrAttachmentsUnavailable
	}
	member, err := s.repo.IsConversationParticipant(ctx, sqlcgen.IsConversationParticipantParams{
		ConversationID: conversationID, UserID: meID,
	})
	if err != nil {
		return Attachment{}, err
	}
	if !member {
		return Attachment{}, ErrNotParticipant
	}
	kind, ok := attachmentKind(in.ContentType, in.Filename)
	if !ok {
		return Attachment{}, ErrUnsupportedAttachment
	}
	if in.Size > s.maxAttach {
		return Attachment{}, ErrAttachmentTooLarge
	}
	attID := uuid.New()
	ext := attachmentExt(in.ContentType, in.Filename)
	key := path.Join("dm-attachments", conversationID.String(), attID.String()+"."+ext)

	// Bound the stored bytes even if the declared size lied: read one extra byte
	// past the cap to detect an oversize stream.
	written, err := s.blobs.Put(ctx, key, io.LimitReader(in.Reader, s.maxAttach+1))
	if err != nil {
		return Attachment{}, err
	}
	if written > s.maxAttach {
		_ = s.blobs.Delete(ctx, key)
		return Attachment{}, ErrAttachmentTooLarge
	}
	// Malware scan, fail-closed: any infection OR scan error rejects the upload
	// and removes the stored bytes.
	if s.scanner != nil {
		clean, serr := s.scanner.Scan(ctx, key)
		if serr != nil || !clean {
			_ = s.blobs.Delete(ctx, key)
			if serr != nil {
				s.logger.WarnContext(ctx, "attachment scan failed (fail-closed)", "error", serr, "conversation_id", conversationID)
			}
			return Attachment{}, ErrAttachmentRejected
		}
	}
	row, err := s.repo.CreateMessageAttachment(ctx, sqlcgen.CreateMessageAttachmentParams{
		ID: attID, ConversationID: conversationID, UploaderID: meID,
		Kind: kind, ContentType: in.ContentType, Filename: in.Filename,
		SizeBytes: written, StorageKey: key,
	})
	if err != nil {
		_ = s.blobs.Delete(ctx, key)
		return Attachment{}, err
	}
	return Attachment{
		ID: row.ID, ConversationID: row.ConversationID, Kind: row.Kind,
		ContentType: row.ContentType, Filename: row.Filename, SizeBytes: row.SizeBytes,
		StorageKey: row.StorageKey,
	}, nil
}

// AttachmentForDownload returns an attachment (including its storage key) for the
// caller to stream, gated on participation in the attachment's conversation. An
// unknown attachment, or one in a conversation the caller is not part of, is
// ErrAttachmentNotFound (→ 404, no existence leak).
func (s *Service) AttachmentForDownload(ctx context.Context, meID, attachmentID uuid.UUID) (Attachment, error) {
	if s.blobs == nil {
		return Attachment{}, ErrAttachmentsUnavailable
	}
	row, err := s.repo.GetMessageAttachment(ctx, attachmentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Attachment{}, ErrAttachmentNotFound
		}
		return Attachment{}, err
	}
	member, err := s.repo.IsConversationParticipant(ctx, sqlcgen.IsConversationParticipantParams{
		ConversationID: row.ConversationID, UserID: meID,
	})
	if err != nil {
		return Attachment{}, err
	}
	if !member {
		return Attachment{}, ErrAttachmentNotFound
	}
	return Attachment{
		ID: row.ID, ConversationID: row.ConversationID, Kind: row.Kind,
		ContentType: row.ContentType, Filename: row.Filename, SizeBytes: row.SizeBytes,
		StorageKey: row.StorageKey,
	}, nil
}

// OpenAttachment returns a reader for a stored attachment's bytes.
func (s *Service) OpenAttachment(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.blobs.Open(ctx, key)
}

// MarkRead advances the caller's read watermark in a conversation. When
// messageID is non-nil it must belong to the conversation; otherwise the newest
// message is used ("mark all read"). Idempotent, advance-only. The caller must
// be a participant (ErrNotParticipant → 404). An empty conversation is a no-op.
func (s *Service) MarkRead(ctx context.Context, meID, conversationID uuid.UUID, messageID *uuid.UUID) error {
	member, err := s.repo.IsConversationParticipant(ctx, sqlcgen.IsConversationParticipantParams{
		ConversationID: conversationID, UserID: meID,
	})
	if err != nil {
		return err
	}
	if !member {
		return ErrNotParticipant
	}
	var target uuid.UUID
	if messageID != nil {
		row, gerr := s.repo.GetMessage(ctx, *messageID)
		if gerr != nil {
			if errors.Is(gerr, pgx.ErrNoRows) {
				return ErrMessageNotFound
			}
			return gerr
		}
		if row.ConversationID != conversationID {
			return ErrMessageNotFound
		}
		target = row.ID
	} else {
		latest, lerr := s.repo.GetLatestMessageID(ctx, conversationID)
		if lerr != nil {
			if errors.Is(lerr, pgx.ErrNoRows) {
				return nil // nothing to read yet
			}
			return lerr
		}
		target = latest
	}
	return s.repo.SetLastReadMessage(ctx, sqlcgen.SetLastReadMessageParams{
		MessageID: pgUUID(target), ConversationID: conversationID, UserID: meID,
	})
}

// PeerReadWatermark returns the other participant's read watermark (the newest
// message id they have read), or (uuid.Nil, false) when the peer has read
// nothing OR has disabled read receipts. The caller must have already confirmed
// participation (this method does not re-check).
func (s *Service) PeerReadWatermark(ctx context.Context, meID, conversationID uuid.UUID) (uuid.UUID, bool, error) {
	peer, err := s.repo.GetOtherParticipant(ctx, sqlcgen.GetOtherParticipantParams{
		ConversationID: conversationID, UserID: meID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	// The peer's privacy toggle: if they disabled read receipts, hide their
	// watermark from everyone.
	enabled, err := s.ReadReceiptsEnabled(ctx, peer)
	if err != nil {
		return uuid.Nil, false, err
	}
	if !enabled {
		return uuid.Nil, false, nil
	}
	wm, err := s.repo.GetPeerReadWatermark(ctx, sqlcgen.GetPeerReadWatermarkParams{
		ConversationID: conversationID, UserID: meID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	if !wm.Valid {
		return uuid.Nil, false, nil
	}
	return wm.Bytes, true, nil
}

// ReadReceiptsEnabled reports a user's read-receipts privacy toggle (default
// enabled). Stored in notification_prefs under the 'read_receipts' pseudo-type.
func (s *Service) ReadReceiptsEnabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	return s.repo.IsNotificationTypeEnabled(ctx, sqlcgen.IsNotificationTypeEnabledParams{
		UserID: userID, Type: readReceiptsPrefType,
	})
}

// SetReadReceiptsEnabled sets a user's read-receipts privacy toggle.
func (s *Service) SetReadReceiptsEnabled(ctx context.Context, userID uuid.UUID, enabled bool) error {
	return s.repo.UpsertNotificationPref(ctx, sqlcgen.UpsertNotificationPrefParams{
		UserID: userID, Type: readReceiptsPrefType, Enabled: enabled,
	})
}

// DeleteMessage tombstones a message (body → "[deleted]") — sender-only. Its
// attachments (bytes + rows) are removed. Unknown message → ErrMessageNotFound;
// a non-sender → ErrNotSender (→ 404); an already-deleted message is an
// idempotent success.
func (s *Service) DeleteMessage(ctx context.Context, meID, messageID uuid.UUID) error {
	row, err := s.repo.GetMessage(ctx, messageID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMessageNotFound
		}
		return err
	}
	if row.SenderID != meID {
		return ErrNotSender
	}
	if row.DeletedAt.Valid {
		return nil // already tombstoned
	}
	// Remove attachment bytes then rows (best-effort on the blobs).
	if s.blobs != nil {
		if keys, kerr := s.repo.ListAttachmentKeysByMessage(ctx, pgUUID(messageID)); kerr == nil {
			for _, k := range keys {
				if derr := s.blobs.Delete(ctx, k.StorageKey); derr != nil {
					s.logger.WarnContext(ctx, "delete attachment blob failed", "error", derr, "attachment_id", k.ID)
				}
			}
		}
	}
	if derr := s.repo.DeleteAttachmentsByMessage(ctx, pgUUID(messageID)); derr != nil {
		s.logger.WarnContext(ctx, "delete attachment rows failed", "error", derr, "message_id", messageID)
	}
	if _, terr := s.repo.TombstoneMessage(ctx, sqlcgen.TombstoneMessageParams{ID: messageID, SenderID: meID}); terr != nil {
		return terr
	}
	return nil
}

// ReportableMessage resolves a message for reporting: it returns the message's
// conversation id and current body snapshot AFTER confirming the caller is a
// participant of the conversation (either participant may report). Unknown
// message or non-participant → ErrMessageNotFound (→ 404).
func (s *Service) ReportableMessage(ctx context.Context, meID, messageID uuid.UUID) (conversationID uuid.UUID, bodySnapshot string, err error) {
	row, err := s.repo.GetMessage(ctx, messageID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, "", ErrMessageNotFound
		}
		return uuid.Nil, "", err
	}
	member, merr := s.repo.IsConversationParticipant(ctx, sqlcgen.IsConversationParticipantParams{
		ConversationID: row.ConversationID, UserID: meID,
	})
	if merr != nil {
		return uuid.Nil, "", merr
	}
	if !member {
		return uuid.Nil, "", ErrMessageNotFound
	}
	return row.ConversationID, row.Body, nil
}

// launchPreview kicks off a best-effort, async link-preview fetch for the first
// URL in body. It never blocks or affects the send. No-op when previews are
// disabled, the body has no URL, or the shared cache row already resolved.
func (s *Service) launchPreview(messageID uuid.UUID, body string) {
	if s.previews == nil {
		return
	}
	rawURL := linkpreview.FirstURL(body)
	if rawURL == "" {
		return
	}
	sum := sha256.Sum256([]byte(rawURL))
	urlHash := hex.EncodeToString(sum[:])
	// Detached context so the fetch outlives the request; own timeout inside.
	bg := context.Background()
	if err := s.repo.UpsertPendingLinkPreview(bg, sqlcgen.UpsertPendingLinkPreviewParams{UrlHash: urlHash, Url: rawURL}); err != nil {
		s.logger.WarnContext(bg, "upsert link preview failed", "error", err)
		return
	}
	if err := s.repo.SetMessageLinkPreview(bg, sqlcgen.SetMessageLinkPreviewParams{ID: messageID, UrlHash: pgText(urlHash)}); err != nil {
		s.logger.WarnContext(bg, "set message link preview failed", "error", err, "message_id", messageID)
		return
	}
	// Skip the network fetch when the shared cache already has a result.
	if cur, err := s.repo.GetLinkPreview(bg, urlHash); err == nil && cur.State != "pending" {
		return
	}
	s.previewWG.Add(1)
	go func() {
		defer s.previewWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		title, desc, image, ferr := s.previews.Fetch(ctx, rawURL)
		state := "ready"
		if ferr != nil {
			state, title, desc, image = "failed", "", "", ""
		}
		if rerr := s.repo.ResolveLinkPreview(ctx, sqlcgen.ResolveLinkPreviewParams{
			UrlHash: urlHash, State: state, Title: title, Description: desc, ImageUrl: image,
		}); rerr != nil {
			s.logger.WarnContext(ctx, "resolve link preview failed", "error", rerr)
		}
	}()
}

func pgText(s string) *string { return &s }
