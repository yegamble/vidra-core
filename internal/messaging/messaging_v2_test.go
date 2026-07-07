package messaging

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// setMsgTime overwrites a message's stored timestamp (white-box) so keyset tests
// are deterministic (real time.Now() could collide for rapid sequential sends).
func setMsgTime(f *fakeRepo, id uuid.UUID, at time.Time) {
	for i := range f.messages {
		if f.messages[i].id == id {
			f.messages[i].createdAt = at
		}
	}
}

// TestKeysetPagination proves before_id keyset paging: newest-first pages that do
// not shift when a new message arrives (no duplicate/skip), an empty boundary
// page, and 422-shaped errors for unknown or foreign cursors.
func TestKeysetPagination(t *testing.T) {
	ada, bob, carol := uuid.New(), uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob, carol)
	svc := NewService(repo)
	ctx := context.Background()
	conv := startConv(t, svc, ada, bob)

	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]uuid.UUID, 5)
	for i := 0; i < 5; i++ {
		m, err := svc.SendMessage(ctx, ada, conv, fmt.Sprintf("m%d", i), nil)
		if err != nil {
			t.Fatalf("send m%d: %v", i, err)
		}
		ids[i] = m.ID
		setMsgTime(repo, m.ID, base.Add(time.Duration(i)*time.Minute)) // m0 oldest … m4 newest
	}

	// Page 1: newest two.
	p1, err := svc.ListMessages(ctx, ada, conv, 2, 0)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(p1) != 2 || p1[0].ID != ids[4] || p1[1].ID != ids[3] {
		t.Fatalf("page1 = %v, want [m4 m3]", msgIDs(p1))
	}

	// A new message arrives between pages (would shift an offset page).
	m5, err := svc.SendMessage(ctx, ada, conv, "m5", nil)
	if err != nil {
		t.Fatal(err)
	}
	setMsgTime(repo, m5.ID, base.Add(10*time.Minute)) // newest of all

	// Page 2 by keyset from the oldest of page 1 (m3): stable, no dup/skip.
	p2, err := svc.ListMessagesBefore(ctx, ada, conv, ids[3], 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(p2) != 2 || p2[0].ID != ids[2] || p2[1].ID != ids[1] {
		t.Fatalf("page2 = %v, want [m2 m1] (stable despite m5 insert)", msgIDs(p2))
	}

	// Page 3: the last remaining older message.
	p3, err := svc.ListMessagesBefore(ctx, ada, conv, ids[1], 2)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(p3) != 1 || p3[0].ID != ids[0] {
		t.Fatalf("page3 = %v, want [m0]", msgIDs(p3))
	}

	// Exact boundary: before the oldest message is an empty page.
	empty, err := svc.ListMessagesBefore(ctx, ada, conv, ids[0], 2)
	if err != nil {
		t.Fatalf("empty page: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("before oldest = %v, want empty", msgIDs(empty))
	}

	// Unknown cursor → invalid.
	if _, err := svc.ListMessagesBefore(ctx, ada, conv, uuid.New(), 2); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("unknown cursor err = %v, want ErrInvalidCursor", err)
	}

	// Foreign-conversation cursor → invalid (a message from another conversation).
	otherConv := startConv(t, svc, ada, carol)
	foreign, _ := svc.SendMessage(ctx, ada, otherConv, "elsewhere", nil)
	if _, err := svc.ListMessagesBefore(ctx, ada, conv, foreign.ID, 2); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("foreign cursor err = %v, want ErrInvalidCursor", err)
	}

	// Non-participant → 404-shaped, even with a cursor.
	if _, err := svc.ListMessagesBefore(ctx, carol, conv, ids[3], 2); !errors.Is(err, ErrNotParticipant) {
		t.Errorf("non-participant keyset err = %v, want ErrNotParticipant", err)
	}
}

func msgIDs(ms []Message) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Body
	}
	return out
}

// TestAvatarFlags proves the D3 avatar-presence flags flow through both listings.
func TestAvatarFlags(t *testing.T) {
	ada, bob := uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob)
	svc := NewService(repo)
	ctx := context.Background()
	conv := startConv(t, svc, ada, bob)
	if _, err := svc.SendMessage(ctx, ada, conv, "hi", nil); err != nil {
		t.Fatal(err)
	}

	// No avatars set → flags false.
	msgs, _ := svc.ListMessages(ctx, bob, conv, 20, 0)
	if len(msgs) != 1 || msgs[0].SenderHasAvatar {
		t.Fatalf("sender_has_avatar = %v, want false", msgs)
	}
	inbox, _ := svc.ListConversations(ctx, bob, 20, 0)
	if len(inbox) != 1 || inbox[0].OtherHasAvatar {
		t.Fatalf("other_has_avatar = %v, want false", inbox)
	}

	// Ada gets an avatar → both flags flip true from Bob's perspective.
	repo.avatars[ada] = true
	msgs, _ = svc.ListMessages(ctx, bob, conv, 20, 0)
	if !msgs[0].SenderHasAvatar {
		t.Error("sender_has_avatar = false after avatar set, want true")
	}
	inbox, _ = svc.ListConversations(ctx, bob, 20, 0)
	if !inbox[0].OtherHasAvatar {
		t.Error("other_has_avatar = false after avatar set, want true")
	}

	// The keyset listing carries the flag too.
	m2, _ := svc.SendMessage(ctx, ada, conv, "again", nil)
	before, _ := svc.ListMessagesBefore(ctx, bob, conv, m2.ID, 20)
	if len(before) == 0 || !before[0].SenderHasAvatar {
		t.Errorf("keyset sender_has_avatar = %v, want true", before)
	}
}
