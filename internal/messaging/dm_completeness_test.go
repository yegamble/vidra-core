package messaging

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// fakeScanner is a messaging.Scanner with a fixed verdict.
type fakeScanner struct {
	clean bool
	err   error
}

func (s fakeScanner) Scan(context.Context, string) (bool, error) { return s.clean, s.err }

// fakePreviews is a messaging.PreviewFetcher returning fixed metadata.
type fakePreviews struct {
	title, desc, image string
	err                error
}

func (p fakePreviews) Fetch(context.Context, string) (string, string, string, error) {
	return p.title, p.desc, p.image, p.err
}

// startConv wires a conversation between two users on the given repo.
func startConv(t *testing.T, svc *Service, ada, bob uuid.UUID) uuid.UUID {
	t.Helper()
	conv, err := svc.StartConversation(context.Background(), ada, bob)
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	return conv.ID
}

func newAttachService(t *testing.T, repo *fakeRepo, scanner Scanner, previews PreviewFetcher) *Service {
	t.Helper()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	opts := []Option{WithAttachments(blobs, scanner, 0)}
	if previews != nil {
		opts = append(opts, WithPreviews(previews))
	}
	return NewService(repo, opts...)
}

func TestUploadAttachmentAndSend(t *testing.T) {
	ada, bob := uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob)
	svc := newAttachService(t, repo, fakeScanner{clean: true}, nil)
	ctx := context.Background()
	conv := startConv(t, svc, ada, bob)

	att, err := svc.UploadAttachment(ctx, ada, conv, AttachmentInput{
		Filename: "pic.png", ContentType: "image/png", Size: 3, Reader: strings.NewReader("png"),
	})
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	if att.Kind != "image" {
		t.Errorf("kind = %q, want image", att.Kind)
	}
	// Send referencing the attachment.
	msg, err := svc.SendMessage(ctx, ada, conv, "look", []uuid.UUID{att.ID})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].ID != att.ID {
		t.Fatalf("send attachments = %+v", msg.Attachments)
	}
	// It surfaces on the thread for the peer.
	msgs, err := svc.ListMessages(ctx, bob, conv, 20, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 || len(msgs[0].Attachments) != 1 {
		t.Fatalf("list = %+v", msgs)
	}
	// The peer can resolve the attachment for download; a non-participant cannot.
	if _, err := svc.AttachmentForDownload(ctx, bob, att.ID); err != nil {
		t.Errorf("peer download: %v", err)
	}
	if _, err := svc.AttachmentForDownload(ctx, uuid.New(), att.ID); !errors.Is(err, ErrAttachmentNotFound) {
		t.Errorf("stranger download err = %v, want ErrAttachmentNotFound", err)
	}
}

func TestUploadAttachmentRejects(t *testing.T) {
	ada, bob := uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob)
	ctx := context.Background()

	// Unsupported type.
	svc := newAttachService(t, repo, fakeScanner{clean: true}, nil)
	conv := startConv(t, svc, ada, bob)
	if _, err := svc.UploadAttachment(ctx, ada, conv, AttachmentInput{
		Filename: "a.exe", ContentType: "application/octet-stream", Size: 2, Reader: strings.NewReader("x"),
	}); !errors.Is(err, ErrUnsupportedAttachment) {
		t.Errorf("unsupported err = %v", err)
	}
	// Non-participant.
	if _, err := svc.UploadAttachment(ctx, uuid.New(), conv, AttachmentInput{
		Filename: "a.png", ContentType: "image/png", Size: 1, Reader: strings.NewReader("x"),
	}); !errors.Is(err, ErrNotParticipant) {
		t.Errorf("non-participant err = %v", err)
	}
	// Scanner fail-closed (infected).
	infected := newAttachService(t, repo, fakeScanner{clean: false}, nil)
	if _, err := infected.UploadAttachment(ctx, ada, conv, AttachmentInput{
		Filename: "a.pdf", ContentType: "application/pdf", Size: 3, Reader: strings.NewReader("pdf"),
	}); !errors.Is(err, ErrAttachmentRejected) {
		t.Errorf("infected err = %v, want ErrAttachmentRejected", err)
	}
	// Scanner error → also fail-closed.
	broken := newAttachService(t, repo, fakeScanner{clean: true, err: errors.New("clamd down")}, nil)
	if _, err := broken.UploadAttachment(ctx, ada, conv, AttachmentInput{
		Filename: "a.pdf", ContentType: "application/pdf", Size: 3, Reader: strings.NewReader("pdf"),
	}); !errors.Is(err, ErrAttachmentRejected) {
		t.Errorf("scan-error err = %v, want ErrAttachmentRejected", err)
	}
}

func TestSendRejectsForeignAttachment(t *testing.T) {
	ada, bob := uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob)
	svc := newAttachService(t, repo, fakeScanner{clean: true}, nil)
	ctx := context.Background()
	conv := startConv(t, svc, ada, bob)

	// Bob uploads; Ada cannot reference Bob's attachment.
	att, err := svc.UploadAttachment(ctx, bob, conv, AttachmentInput{
		Filename: "b.png", ContentType: "image/png", Size: 1, Reader: strings.NewReader("x"),
	})
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	if _, err := svc.SendMessage(ctx, ada, conv, "steal", []uuid.UUID{att.ID}); !errors.Is(err, ErrInvalidAttachments) {
		t.Errorf("foreign attach err = %v, want ErrInvalidAttachments", err)
	}
	// An already-linked attachment cannot be reused.
	if _, err := svc.SendMessage(ctx, bob, conv, "mine", []uuid.UUID{att.ID}); err != nil {
		t.Fatalf("first send: %v", err)
	}
	if _, err := svc.SendMessage(ctx, bob, conv, "again", []uuid.UUID{att.ID}); !errors.Is(err, ErrInvalidAttachments) {
		t.Errorf("reuse err = %v, want ErrInvalidAttachments", err)
	}
}

func TestReadReceiptsAndUnread(t *testing.T) {
	ada, bob := uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob)
	svc := NewService(repo)
	ctx := context.Background()
	conv := startConv(t, svc, ada, bob)

	// Bob sends two messages; Ada has 2 unread.
	if _, err := svc.SendMessage(ctx, bob, conv, "one", nil); err != nil {
		t.Fatal(err)
	}
	m2, err := svc.SendMessage(ctx, bob, conv, "two", nil)
	if err != nil {
		t.Fatal(err)
	}
	inbox, _ := svc.ListConversations(ctx, ada, 20, 0)
	if len(inbox) != 1 || inbox[0].UnreadCount != 2 {
		t.Fatalf("ada unread = %+v, want 2", inbox)
	}
	// Ada marks all read → unread 0.
	if err := svc.MarkRead(ctx, ada, conv, nil); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	inbox, _ = svc.ListConversations(ctx, ada, 20, 0)
	if inbox[0].UnreadCount != 0 {
		t.Errorf("after read unread = %d, want 0", inbox[0].UnreadCount)
	}
	// Bob sees Ada's watermark (Ada read up to m2).
	wm, ok, err := svc.PeerReadWatermark(ctx, bob, conv)
	if err != nil || !ok || wm != m2.ID {
		t.Fatalf("peer watermark = (%v,%v,%v), want (%v,true,nil)", wm, ok, err, m2.ID)
	}
	// Ada disables read receipts → Bob no longer sees her watermark.
	if err := svc.SetReadReceiptsEnabled(ctx, ada, false); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := svc.PeerReadWatermark(ctx, bob, conv); ok {
		t.Error("peer watermark visible after Ada disabled read receipts")
	}
}

func TestDeleteMessage(t *testing.T) {
	ada, bob := uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob)
	svc := newAttachService(t, repo, fakeScanner{clean: true}, nil)
	ctx := context.Background()
	conv := startConv(t, svc, ada, bob)

	att, _ := svc.UploadAttachment(ctx, ada, conv, AttachmentInput{
		Filename: "x.png", ContentType: "image/png", Size: 1, Reader: strings.NewReader("x"),
	})
	msg, _ := svc.SendMessage(ctx, ada, conv, "oops http://x.test", []uuid.UUID{att.ID})

	// Only the sender may delete: Bob cannot (404-shaped).
	if err := svc.DeleteMessage(ctx, bob, msg.ID); !errors.Is(err, ErrNotSender) {
		t.Errorf("bob delete err = %v, want ErrNotSender", err)
	}
	if err := svc.DeleteMessage(ctx, ada, msg.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Idempotent.
	if err := svc.DeleteMessage(ctx, ada, msg.ID); err != nil {
		t.Errorf("second delete: %v", err)
	}
	// Tombstoned in the thread; attachments gone.
	msgs, _ := svc.ListMessages(ctx, ada, conv, 20, 0)
	if len(msgs) != 1 || !msgs[0].Deleted || msgs[0].Body != "[deleted]" || len(msgs[0].Attachments) != 0 {
		t.Fatalf("after delete = %+v", msgs)
	}
	// Unknown message → not found.
	if err := svc.DeleteMessage(ctx, ada, uuid.New()); !errors.Is(err, ErrMessageNotFound) {
		t.Errorf("unknown delete err = %v", err)
	}
}

func TestReportableMessage(t *testing.T) {
	ada, bob, carol := uuid.New(), uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob, carol)
	svc := NewService(repo)
	ctx := context.Background()
	conv := startConv(t, svc, ada, bob)
	msg, _ := svc.SendMessage(ctx, ada, conv, "hello", nil)

	// A participant (bob) resolves it with the body snapshot.
	cid, body, err := svc.ReportableMessage(ctx, bob, msg.ID)
	if err != nil || cid != conv || body != "hello" {
		t.Fatalf("ReportableMessage = (%v,%q,%v)", cid, body, err)
	}
	// A non-participant is 404-shaped.
	if _, _, err := svc.ReportableMessage(ctx, carol, msg.ID); !errors.Is(err, ErrMessageNotFound) {
		t.Errorf("stranger report err = %v", err)
	}
}

func TestLinkPreviewAttached(t *testing.T) {
	ada, bob := uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob)
	svc := newAttachService(t, repo, nil, fakePreviews{title: "Example", desc: "d", image: "https://img.test/i.png"})
	ctx := context.Background()
	conv := startConv(t, svc, ada, bob)

	if _, err := svc.SendMessage(ctx, ada, conv, "see https://example.test/page", nil); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	svc.WaitForPreviews() // await the best-effort async fetch
	msgs, _ := svc.ListMessages(ctx, bob, conv, 20, 0)
	if len(msgs) != 1 || msgs[0].Preview == nil {
		t.Fatalf("no preview: %+v", msgs)
	}
	if msgs[0].Preview.Title != "Example" || msgs[0].Preview.URL != "https://example.test/page" {
		t.Errorf("preview = %+v", msgs[0].Preview)
	}
}

func TestLinkPreviewFailureIsSilent(t *testing.T) {
	ada, bob := uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob)
	svc := newAttachService(t, repo, nil, fakePreviews{err: io.ErrUnexpectedEOF})
	ctx := context.Background()
	conv := startConv(t, svc, ada, bob)

	// The send must succeed even though the preview fetch will fail.
	if _, err := svc.SendMessage(ctx, ada, conv, "bad https://nope.test", nil); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	svc.WaitForPreviews()
	msgs, _ := svc.ListMessages(ctx, bob, conv, 20, 0)
	if len(msgs) != 1 || msgs[0].Preview != nil {
		t.Fatalf("failed preview should be absent: %+v", msgs)
	}
}
