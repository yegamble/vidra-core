package repository

import (
	"context"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/testutil"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMessageRepository_MultiUser_ComplexScenarios tests comprehensive multi-user messaging scenarios
func TestMessageRepository_MultiUser_ComplexScenarios(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	ur := NewUserRepository(td.DB)
	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	// Create 4 test users
	alice := createTestUser(t, ur, ctx, "alice", "alice@example.com")
	bob := createTestUser(t, ur, ctx, "bob", "bob@example.com")
	charlie := createTestUser(t, ur, ctx, "charlie", "charlie@example.com")
	diana := createTestUser(t, ur, ctx, "diana", "diana@example.com")

	t.Run("Cross_User_Message_Exchange", func(t *testing.T) {
		// Reset tables for this subtest
		td.TruncateTables(t, "messages", "conversations")

		baseTime := time.Now().Add(-10 * time.Minute)

		// Alice -> Bob (message 1)
		m1 := &domain.Message{
			ID:          uuid.NewString(),
			SenderID:    alice.ID,
			RecipientID: bob.ID,
			Content:     strPtr("Hi Bob, how are you?"),
			MessageType: domain.MessageTypeText,
			CreatedAt:   baseTime,
			UpdatedAt:   baseTime,
		}
		require.NoError(t, mr.CreateMessage(ctx, m1))

		// Bob -> Alice (message 2)
		m2 := &domain.Message{
			ID:          uuid.NewString(),
			SenderID:    bob.ID,
			RecipientID: alice.ID,
			Content:     strPtr("Hi Alice! I'm doing well, thanks!"),
			MessageType: domain.MessageTypeText,
			CreatedAt:   baseTime.Add(1 * time.Minute),
			UpdatedAt:   baseTime.Add(1 * time.Minute),
		}
		require.NoError(t, mr.CreateMessage(ctx, m2))

		// Charlie -> Alice (message 3)
		m3 := &domain.Message{
			ID:          uuid.NewString(),
			SenderID:    charlie.ID,
			RecipientID: alice.ID,
			Content:     strPtr("Alice, can we meet tomorrow?"),
			MessageType: domain.MessageTypeText,
			CreatedAt:   baseTime.Add(2 * time.Minute),
			UpdatedAt:   baseTime.Add(2 * time.Minute),
		}
		require.NoError(t, mr.CreateMessage(ctx, m3))

		// Alice -> Charlie (message 4)
		m4 := &domain.Message{
			ID:          uuid.NewString(),
			SenderID:    alice.ID,
			RecipientID: charlie.ID,
			Content:     strPtr("Sure Charlie, what time?"),
			MessageType: domain.MessageTypeText,
			CreatedAt:   baseTime.Add(3 * time.Minute),
			UpdatedAt:   baseTime.Add(3 * time.Minute),
		}
		require.NoError(t, mr.CreateMessage(ctx, m4))

		// Diana -> Bob (message 5)
		m5 := &domain.Message{
			ID:          uuid.NewString(),
			SenderID:    diana.ID,
			RecipientID: bob.ID,
			Content:     strPtr("Bob, urgent project update needed!"),
			MessageType: domain.MessageTypeText,
			CreatedAt:   baseTime.Add(4 * time.Minute),
			UpdatedAt:   baseTime.Add(4 * time.Minute),
		}
		require.NoError(t, mr.CreateMessage(ctx, m5))

		// Test: Alice's conversations should show 2 conversations (Bob and Charlie)
		aliceConvs, err := mr.GetConversations(ctx, alice.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, aliceConvs, 2)

		// Test: Bob's conversations should show 2 conversations (Alice and Diana)
		bobConvs, err := mr.GetConversations(ctx, bob.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, bobConvs, 2)

		// Test: Messages between Alice and Bob
		aliceBobMsgs, err := mr.GetMessages(ctx, alice.ID, bob.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, aliceBobMsgs, 2)
		// Should be ordered by created_at DESC (most recent first)
		assert.Equal(t, m2.ID, aliceBobMsgs[0].ID) // Bob's reply is more recent
		assert.Equal(t, m1.ID, aliceBobMsgs[1].ID) // Alice's original message

		// Test: Messages between Alice and Charlie
		aliceCharlieMsgs, err := mr.GetMessages(ctx, alice.ID, charlie.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, aliceCharlieMsgs, 2)
		assert.Equal(t, m4.ID, aliceCharlieMsgs[0].ID) // Alice's reply is more recent
		assert.Equal(t, m3.ID, aliceCharlieMsgs[1].ID) // Charlie's original message

		// Test: Unread counts
		// Alice should have 2 unread messages: m2 from Bob and m3 from Charlie
		aliceUnread, err := mr.GetUnreadCount(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, 2, aliceUnread)

		// Bob should have 2 unread messages: m1 from Alice and m5 from Diana
		bobUnread, err := mr.GetUnreadCount(ctx, bob.ID)
		require.NoError(t, err)
		assert.Equal(t, 2, bobUnread)

		// Charlie should have 1 unread (m4 from Alice)
		charlieUnread, err := mr.GetUnreadCount(ctx, charlie.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, charlieUnread)

		// Diana should have 0 unread
		dianaUnread, err := mr.GetUnreadCount(ctx, diana.ID)
		require.NoError(t, err)
		assert.Equal(t, 0, dianaUnread)
	})
}

func TestMessageRepository_MultiUser_DeletionScenarios(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	ur := NewUserRepository(td.DB)
	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	// Create 3 test users
	alice := createTestUser(t, ur, ctx, "alice_del", "alice_del@example.com")
	bob := createTestUser(t, ur, ctx, "bob_del", "bob_del@example.com")
	charlie := createTestUser(t, ur, ctx, "charlie_del", "charlie_del@example.com")

	t.Run("Selective_Message_Deletion", func(t *testing.T) {
		// Reset tables for this subtest
		td.TruncateTables(t, "messages", "conversations")

		baseTime := time.Now().Add(-5 * time.Minute)

		// Create conversation with multiple messages
		messages := []*domain.Message{
			{
				ID:          uuid.NewString(),
				SenderID:    alice.ID,
				RecipientID: bob.ID,
				Content:     strPtr("Message 1 from Alice"),
				MessageType: domain.MessageTypeText,
				CreatedAt:   baseTime,
				UpdatedAt:   baseTime,
			},
			{
				ID:          uuid.NewString(),
				SenderID:    bob.ID,
				RecipientID: alice.ID,
				Content:     strPtr("Message 2 from Bob"),
				MessageType: domain.MessageTypeText,
				CreatedAt:   baseTime.Add(1 * time.Minute),
				UpdatedAt:   baseTime.Add(1 * time.Minute),
			},
			{
				ID:          uuid.NewString(),
				SenderID:    alice.ID,
				RecipientID: bob.ID,
				Content:     strPtr("Message 3 from Alice"),
				MessageType: domain.MessageTypeText,
				CreatedAt:   baseTime.Add(2 * time.Minute),
				UpdatedAt:   baseTime.Add(2 * time.Minute),
			},
			{
				ID:          uuid.NewString(),
				SenderID:    bob.ID,
				RecipientID: alice.ID,
				Content:     strPtr("Message 4 from Bob"),
				MessageType: domain.MessageTypeText,
				CreatedAt:   baseTime.Add(3 * time.Minute),
				UpdatedAt:   baseTime.Add(3 * time.Minute),
			},
		}

		for _, msg := range messages {
			require.NoError(t, mr.CreateMessage(ctx, msg))
		}

		// Verify initial state: both users see all 4 messages
		aliceMsgs, err := mr.GetMessages(ctx, alice.ID, bob.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, aliceMsgs, 4)

		bobMsgs, err := mr.GetMessages(ctx, bob.ID, alice.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, bobMsgs, 4)

		// Alice deletes her first message (messages[0])
		require.NoError(t, mr.DeleteMessage(ctx, messages[0].ID, alice.ID))

		// Alice should now see 3 messages, Bob should still see 4
		aliceMsgsAfterDel, err := mr.GetMessages(ctx, alice.ID, bob.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, aliceMsgsAfterDel, 3)

		bobMsgsAfterAliceDel, err := mr.GetMessages(ctx, bob.ID, alice.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, bobMsgsAfterAliceDel, 4)

		// Bob also deletes the same message (messages[0])
		require.NoError(t, mr.DeleteMessage(ctx, messages[0].ID, bob.ID))

		// Now both should see 3 messages
		aliceMsgsFinal, err := mr.GetMessages(ctx, alice.ID, bob.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, aliceMsgsFinal, 3)

		bobMsgsFinal, err := mr.GetMessages(ctx, bob.ID, alice.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, bobMsgsFinal, 3)

		// Verify the deleted message IDs don't appear in results
		for _, msg := range aliceMsgsFinal {
			assert.NotEqual(t, messages[0].ID, msg.ID)
		}
		for _, msg := range bobMsgsFinal {
			assert.NotEqual(t, messages[0].ID, msg.ID)
		}

		// Test: Try to delete non-existent message
		fakeID := uuid.NewString()
		err = mr.DeleteMessage(ctx, fakeID, alice.ID)
		assert.ErrorIs(t, err, domain.ErrMessageNotFound)

		// Test: Try to delete message as unauthorized user
		err = mr.DeleteMessage(ctx, messages[1].ID, charlie.ID)
		assert.ErrorIs(t, err, domain.ErrMessageNotFound)
	})

	t.Run("Conversation_Persistence_After_Deletion", func(t *testing.T) {
		// Reset tables for this subtest
		td.TruncateTables(t, "messages", "conversations")

		baseTime := time.Now()

		// Create single message conversation
		msg := &domain.Message{
			ID:          uuid.NewString(),
			SenderID:    alice.ID,
			RecipientID: bob.ID,
			Content:     strPtr("Only message in conversation"),
			MessageType: domain.MessageTypeText,
			CreatedAt:   baseTime,
			UpdatedAt:   baseTime,
		}
		require.NoError(t, mr.CreateMessage(ctx, msg))

		// Verify conversation exists
		aliceConvs, err := mr.GetConversations(ctx, alice.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, aliceConvs, 1)

		bobConvs, err := mr.GetConversations(ctx, bob.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, bobConvs, 1)

		// Alice deletes the message
		require.NoError(t, mr.DeleteMessage(ctx, msg.ID, alice.ID))

		// Alice should see no messages, but conversation should still exist
		aliceMsgs, err := mr.GetMessages(ctx, alice.ID, bob.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, aliceMsgs, 0)

		// Bob should still see the message
		bobMsgs, err := mr.GetMessages(ctx, bob.ID, alice.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, bobMsgs, 1)

		// Both should still have the conversation record
		aliceConvsAfterDel, err := mr.GetConversations(ctx, alice.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, aliceConvsAfterDel, 1)

		bobConvsAfterDel, err := mr.GetConversations(ctx, bob.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, bobConvsAfterDel, 1)
	})
}

func TestMessageRepository_MultiUser_ReadStatusManagement(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	ur := NewUserRepository(td.DB)
	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	// Create 3 test users
	alice := createTestUser(t, ur, ctx, "alice_read", "alice_read@example.com")
	bob := createTestUser(t, ur, ctx, "bob_read", "bob_read@example.com")
	charlie := createTestUser(t, ur, ctx, "charlie_read", "charlie_read@example.com")

	t.Run("Complex_Read_Status_Scenarios", func(t *testing.T) {
		// Reset tables for this subtest
		td.TruncateTables(t, "messages", "conversations")

		baseTime := time.Now().Add(-3 * time.Minute)

		// Create messages from multiple senders to Alice
		m1 := &domain.Message{
			ID:          uuid.NewString(),
			SenderID:    bob.ID,
			RecipientID: alice.ID,
			Content:     strPtr("Message from Bob to Alice"),
			MessageType: domain.MessageTypeText,
			CreatedAt:   baseTime,
			UpdatedAt:   baseTime,
		}

		m2 := &domain.Message{
			ID:          uuid.NewString(),
			SenderID:    charlie.ID,
			RecipientID: alice.ID,
			Content:     strPtr("Message from Charlie to Alice"),
			MessageType: domain.MessageTypeText,
			CreatedAt:   baseTime.Add(1 * time.Minute),
			UpdatedAt:   baseTime.Add(1 * time.Minute),
		}

		m3 := &domain.Message{
			ID:          uuid.NewString(),
			SenderID:    bob.ID,
			RecipientID: alice.ID,
			Content:     strPtr("Another message from Bob to Alice"),
			MessageType: domain.MessageTypeText,
			CreatedAt:   baseTime.Add(2 * time.Minute),
			UpdatedAt:   baseTime.Add(2 * time.Minute),
		}

		require.NoError(t, mr.CreateMessage(ctx, m1))
		require.NoError(t, mr.CreateMessage(ctx, m2))
		require.NoError(t, mr.CreateMessage(ctx, m3))

		// Alice should have 3 unread messages
		aliceUnread, err := mr.GetUnreadCount(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, 3, aliceUnread)

		// Alice marks first message from Bob as read
		require.NoError(t, mr.MarkMessageAsRead(ctx, m1.ID, alice.ID))

		// Alice should now have 2 unread messages
		aliceUnreadAfterOne, err := mr.GetUnreadCount(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, 2, aliceUnreadAfterOne)

		// Alice marks message from Charlie as read
		require.NoError(t, mr.MarkMessageAsRead(ctx, m2.ID, alice.ID))

		// Alice should now have 1 unread message
		aliceUnreadAfterTwo, err := mr.GetUnreadCount(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, aliceUnreadAfterTwo)

		// Test: Try to mark already read message again
		err = mr.MarkMessageAsRead(ctx, m1.ID, alice.ID)
		assert.ErrorIs(t, err, domain.ErrMessageNotFound)

		// Test: Try to mark message as read by wrong user (Bob tries to mark his own sent message)
		err = mr.MarkMessageAsRead(ctx, m1.ID, bob.ID)
		assert.ErrorIs(t, err, domain.ErrMessageNotFound)

		// Test: Try to mark message as read by unauthorized user
		err = mr.MarkMessageAsRead(ctx, m3.ID, charlie.ID)
		assert.ErrorIs(t, err, domain.ErrMessageNotFound)

		// Alice marks the last message as read
		require.NoError(t, mr.MarkMessageAsRead(ctx, m3.ID, alice.ID))

		// Alice should now have 0 unread messages
		aliceUnreadFinal, err := mr.GetUnreadCount(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, 0, aliceUnreadFinal)

		// Verify conversation unread counts are updated
		aliceConvs, err := mr.GetConversations(ctx, alice.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, aliceConvs, 2) // Conversations with Bob and Charlie

		totalUnreadInConvs := 0
		for _, conv := range aliceConvs {
			totalUnreadInConvs += conv.UnreadCount
		}
		assert.Equal(t, 0, totalUnreadInConvs)
	})
}

func TestMessageRepository_MultiUser_ConversationOrdering(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	ur := NewUserRepository(td.DB)
	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	// Create test users
	alice := createTestUser(t, ur, ctx, "alice_order", "alice_order@example.com")
	bob := createTestUser(t, ur, ctx, "bob_order", "bob_order@example.com")
	charlie := createTestUser(t, ur, ctx, "charlie_order", "charlie_order@example.com")
	diana := createTestUser(t, ur, ctx, "diana_order", "diana_order@example.com")

	t.Run("Conversation_Last_Message_Ordering", func(t *testing.T) {
		// Reset tables for this subtest
		td.TruncateTables(t, "messages", "conversations")

		baseTime := time.Now().Add(-10 * time.Minute)

		// Create messages in specific time order to test conversation ordering
		messages := []*domain.Message{
			// Oldest conversation: Alice <-> Bob
			{
				ID:          uuid.NewString(),
				SenderID:    alice.ID,
				RecipientID: bob.ID,
				Content:     strPtr("Alice to Bob - oldest"),
				MessageType: domain.MessageTypeText,
				CreatedAt:   baseTime,
				UpdatedAt:   baseTime,
			},
			// Middle conversation: Alice <-> Charlie
			{
				ID:          uuid.NewString(),
				SenderID:    alice.ID,
				RecipientID: charlie.ID,
				Content:     strPtr("Alice to Charlie - middle"),
				MessageType: domain.MessageTypeText,
				CreatedAt:   baseTime.Add(5 * time.Minute),
				UpdatedAt:   baseTime.Add(5 * time.Minute),
			},
			// Most recent conversation: Alice <-> Diana
			{
				ID:          uuid.NewString(),
				SenderID:    diana.ID,
				RecipientID: alice.ID,
				Content:     strPtr("Diana to Alice - newest"),
				MessageType: domain.MessageTypeText,
				CreatedAt:   baseTime.Add(8 * time.Minute),
				UpdatedAt:   baseTime.Add(8 * time.Minute),
			},
			// Update Alice <-> Bob conversation to be second most recent
			{
				ID:          uuid.NewString(),
				SenderID:    bob.ID,
				RecipientID: alice.ID,
				Content:     strPtr("Bob to Alice - second newest"),
				MessageType: domain.MessageTypeText,
				CreatedAt:   baseTime.Add(7 * time.Minute),
				UpdatedAt:   baseTime.Add(7 * time.Minute),
			},
		}

		for _, msg := range messages {
			require.NoError(t, mr.CreateMessage(ctx, msg))
		}

		// Get Alice's conversations - should be ordered by last_message_at DESC
		aliceConvs, err := mr.GetConversations(ctx, alice.ID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, aliceConvs, 3)

		// Verify ordering: Diana (newest), Bob (second), Charlie (oldest with no updates)
		// First conversation should be with Diana (most recent message)
		assert.Equal(t, messages[2].ID, aliceConvs[0].LastMessage.ID)
		assert.True(t,
			(aliceConvs[0].ParticipantOneID == alice.ID && aliceConvs[0].ParticipantTwoID == diana.ID) ||
				(aliceConvs[0].ParticipantOneID == diana.ID && aliceConvs[0].ParticipantTwoID == alice.ID))

		// Second conversation should be with Bob
		assert.Equal(t, messages[3].ID, aliceConvs[1].LastMessage.ID)
		assert.True(t,
			(aliceConvs[1].ParticipantOneID == alice.ID && aliceConvs[1].ParticipantTwoID == bob.ID) ||
				(aliceConvs[1].ParticipantOneID == bob.ID && aliceConvs[1].ParticipantTwoID == alice.ID))

		// Third conversation should be with Charlie (oldest, no updates)
		assert.Equal(t, messages[1].ID, aliceConvs[2].LastMessage.ID)
		assert.True(t,
			(aliceConvs[2].ParticipantOneID == alice.ID && aliceConvs[2].ParticipantTwoID == charlie.ID) ||
				(aliceConvs[2].ParticipantOneID == charlie.ID && aliceConvs[2].ParticipantTwoID == alice.ID))

		// Verify conversations are ordered by last_message_at DESC
		assert.True(t, aliceConvs[0].LastMessageAt.After(aliceConvs[1].LastMessageAt))
		assert.True(t, aliceConvs[1].LastMessageAt.After(aliceConvs[2].LastMessageAt))

		// Test pagination
		page1, err := mr.GetConversations(ctx, alice.ID, 2, 0)
		require.NoError(t, err)
		assert.Len(t, page1, 2)

		page2, err := mr.GetConversations(ctx, alice.ID, 2, 2)
		require.NoError(t, err)
		assert.Len(t, page2, 1)

		// Verify no overlaps between pages
		assert.NotEqual(t, page1[0].ID, page2[0].ID)
		assert.NotEqual(t, page1[1].ID, page2[0].ID)
	})
}
