package repository

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/testutil"

	"github.com/google/uuid"
)

// FuzzMessageRepository_RandomOps performs randomized create/read/delete/mark-read
// sequences and asserts key invariants hold (ordering, unread counts, visibility).
// This fuzz test requires a test DB; run with: go test -fuzz=Fuzz -run ^$ ./internal/repository
func FuzzMessageRepository_RandomOps(f *testing.F) {
	// Seed: (seed1, seed2, ops)
	f.Add(int64(1), int64(2), int64(5))
	f.Add(int64(42), int64(99), int64(10))

	f.Fuzz(func(t *testing.T, seed1, seed2, ops int64) {
		td := testutil.SetupTestDB(t)
		if td == nil {
			return
		}
		td.TruncateTables(t, "messages", "conversations", "users")

		ur := NewUserRepository(td.DB)
		mr := NewMessageRepository(td.DB)
		ctx := context.Background()

		u1 := createTestUser(t, ur, ctx, "fuzz_u1", "fuzz_u1@ex.com")
		u2 := createTestUser(t, ur, ctx, "fuzz_u2", "fuzz_u2@ex.com")

		rng := rand.New(rand.NewSource(seed1 ^ (seed2 << 1)))
		n := int(ops % 20)
		if n < 1 {
			n = 1
		}

		type stat struct {
			id        string
			sender    string
			recipient string
			isRead    bool
			delByS    bool
			delByR    bool
			createdAt time.Time
		}
		var stats []stat

		base := time.Now().Add(-time.Duration(n+5) * time.Minute)
		// Create n alternating messages
		for i := 0; i < n; i++ {
			s := u1
			r := u2
			if i%2 == 1 {
				s, r = u2, u1
			}
			m := &domain.Message{
				ID:          uuid.NewString(),
				SenderID:    s.ID,
				RecipientID: r.ID,
				Content:     "fuzz-" + uuid.NewString(),
				MessageType: domain.MessageTypeText,
				CreatedAt:   base.Add(time.Duration(i) * time.Minute),
				UpdatedAt:   base.Add(time.Duration(i) * time.Minute),
			}
			if err := mr.CreateMessage(ctx, m); err != nil {
				t.Fatalf("create: %v", err)
			}
			stats = append(stats, stat{id: m.ID, sender: s.ID, recipient: r.ID, createdAt: m.CreatedAt})
		}

		// Random ops: mark read or delete
		for i := 0; i < n; i++ {
			j := rng.Intn(len(stats))
			st := &stats[j]
			switch rng.Intn(3) {
			case 0: // mark as read by recipient
				_ = mr.MarkMessageAsRead(ctx, st.id, st.recipient)
				// update expectation if it succeeded at least once
				st.isRead = true
			case 1: // delete by sender
				_ = mr.DeleteMessage(ctx, st.id, st.sender)
				st.delByS = true
			case 2: // delete by recipient
				_ = mr.DeleteMessage(ctx, st.id, st.recipient)
				st.delByR = true
			}
		}

		// Invariant: messages for a viewer exclude those deleted by that viewer
		msgsU1, err := mr.GetMessages(ctx, u1.ID, u2.ID, n+10, 0)
		if err != nil {
			t.Fatalf("get messages u1: %v", err)
		}
		expU1 := 0
		// verify descending order by created_at
		for i := 1; i < len(msgsU1); i++ {
			if msgsU1[i-1].CreatedAt.Before(msgsU1[i].CreatedAt) {
				t.Fatalf("order not desc: %v < %v", msgsU1[i-1].CreatedAt, msgsU1[i].CreatedAt)
			}
		}
		for _, st := range stats {
			if st.sender == u1.ID && st.delByS {
				continue
			}
			if st.recipient == u1.ID && st.delByR {
				continue
			}
			// Visible to u1
			expU1++
		}
		if len(msgsU1) != expU1 {
			t.Fatalf("u1 visibility mismatch: got %d exp %d", len(msgsU1), expU1)
		}

		// Unread count should equal messages sent to u1 not read and not deleted by recipient
		expUnreadU1 := 0
		for _, st := range stats {
			if st.recipient == u1.ID && !st.isRead && !st.delByR {
				expUnreadU1++
			}
		}
		cntU1, err := mr.GetUnreadCount(ctx, u1.ID)
		if err != nil {
			t.Fatalf("unread u1: %v", err)
		}
		if cntU1 != expUnreadU1 {
			t.Fatalf("unread mismatch u1: got %d exp %d", cntU1, expUnreadU1)
		}

		// Conversations invariants: limit respected; last_message ordering desc
		convs, err := mr.GetConversations(ctx, u1.ID, 10, 0)
		if err != nil {
			t.Fatalf("get convs: %v", err)
		}
		for i := 1; i < len(convs); i++ {
			if convs[i-1].LastMessageAt.Before(convs[i].LastMessageAt) {
				t.Fatalf("conversations order not desc: %v < %v", convs[i-1].LastMessageAt, convs[i].LastMessageAt)
			}
		}
		if len(convs) > 10 {
			t.Fatalf("limit not respected: got %d", len(convs))
		}
	})
}
