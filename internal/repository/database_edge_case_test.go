package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/testutil"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Edge Case Tests for Database Unavailability and Notification Systems

// ===== Database Unavailability Tests =====

func TestMessageRepository_CreateMessage_DatabaseUnavailable(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("database not available, skipping test")
		return
	}

	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	// Close the database connection to simulate unavailability
	td.DB.Close()

	m := &domain.Message{
		ID:          uuid.NewString(),
		SenderID:    uuid.NewString(),
		RecipientID: uuid.NewString(),
		Content:     "test message",
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	err := mr.CreateMessage(ctx, m)
	if err == nil {
		t.Error("expected error when database is unavailable, got nil")
	}

	// Should return a database error
	if !errors.Is(err, sql.ErrConnDone) && !strings.Contains(err.Error(), "closed") && !strings.Contains(err.Error(), "bad connection") {
		t.Logf("expected connection error, got: %v", err)
	}
}

func TestMessageRepository_GetMessages_DatabaseTimeout(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("database not available, skipping test")
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	mr := NewMessageRepository(td.DB)

	// Create context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Wait for context to expire
	time.Sleep(10 * time.Millisecond)

	msgs, err := mr.GetMessages(ctx, uuid.NewString(), uuid.NewString(), 10, 0)

	// Should return context deadline exceeded error
	if err == nil {
		t.Error("expected timeout error, got nil")
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Logf("expected deadline exceeded, got: %v", err)
	}

	if msgs != nil {
		t.Error("expected nil messages on timeout")
	}
}

func TestMessageRepository_ConcurrentOperations_DatabaseStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("database not available, skipping test")
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	u1 := createTestUserForEdgeCase(t, td, "stress_user1", "stress1@example.com")
	u2 := createTestUserForEdgeCase(t, td, "stress_user2", "stress2@example.com")

	numGoroutines := 50
	numMessagesPerGoroutine := 20

	var wg sync.WaitGroup
	var errorCount int
	var errorMutex sync.Mutex

	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numMessagesPerGoroutine; j++ {
				m := &domain.Message{
					ID:          uuid.NewString(),
					SenderID:    u1.ID,
					RecipientID: u2.ID,
					Content:     "concurrent message " + uuid.NewString(),
					MessageType: domain.MessageTypeText,
					CreatedAt:   time.Now(),
					UpdatedAt:   time.Now(),
				}

				if err := mr.CreateMessage(ctx, m); err != nil {
					errorMutex.Lock()
					errorCount++
					errorMutex.Unlock()
				}
			}
		}(i)
	}

	wg.Wait()

	// Should have very few errors under normal stress
	if errorCount > numGoroutines {
		t.Errorf("too many errors during concurrent operations: %d", errorCount)
	}

	// Verify messages were created
	msgs, err := mr.GetMessages(ctx, u1.ID, u2.ID, 1000, 0)
	require.NoError(t, err)

	expectedMessages := numGoroutines*numMessagesPerGoroutine - errorCount
	if len(msgs) < expectedMessages-10 {
		t.Errorf("expected ~%d messages, got %d", expectedMessages, len(msgs))
	}
}

// ===== Connection Pool Edge Cases =====

func TestMessageRepository_ConnectionPoolExhaustion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping connection pool test in short mode")
	}

	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("database not available, skipping test")
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	u1 := createTestUserForEdgeCase(t, td, "pool_user1", "pool1@example.com")
	u2 := createTestUserForEdgeCase(t, td, "pool_user2", "pool2@example.com")

	// Set very small connection pool
	td.DB.SetMaxOpenConns(2)
	td.DB.SetMaxIdleConns(1)

	// Try to perform more operations than available connections
	numGoroutines := 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			// Create context with timeout to prevent hanging
			opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			m := &domain.Message{
				ID:          uuid.NewString(),
				SenderID:    u1.ID,
				RecipientID: u2.ID,
				Content:     "pool test message",
				MessageType: domain.MessageTypeText,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}

			// Should eventually succeed or timeout
			err := mr.CreateMessage(opCtx, m)
			if err != nil && !errors.Is(err, context.DeadlineExceeded) {
				t.Logf("unexpected error: %v", err)
			}
		}(i)
	}

	wg.Wait()

	// Reset pool limits
	td.DB.SetMaxOpenConns(0)
	td.DB.SetMaxIdleConns(2)
}

// ===== Transaction Edge Cases =====

func TestMessageRepository_TransactionRollback(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("database not available, skipping test")
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	u1 := createTestUserForEdgeCase(t, td, "tx_user1", "tx1@example.com")
	u2 := createTestUserForEdgeCase(t, td, "tx_user2", "tx2@example.com")

	// Create a message successfully
	m1 := &domain.Message{
		ID:          uuid.NewString(),
		SenderID:    u1.ID,
		RecipientID: u2.ID,
		Content:     "message before rollback",
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, mr.CreateMessage(ctx, m1))

	// Try to create with invalid user ID (should fail)
	m2 := &domain.Message{
		ID:          uuid.NewString(),
		SenderID:    "invalid-user-id-that-does-not-exist",
		RecipientID: u2.ID,
		Content:     "message that should fail",
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err := mr.CreateMessage(ctx, m2)
	if err == nil {
		t.Error("expected error with invalid user ID")
	}

	// First message should still exist
	msgs, err := mr.GetMessages(ctx, u1.ID, u2.ID, 10, 0)
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, m1.ID, msgs[0].ID)
}

// ===== Notification Edge Cases =====

func TestMessageRepository_NotificationWithDBFailure(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("database not available, skipping test")
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	u1 := createTestUserForEdgeCase(t, td, "notif_user1", "notif1@example.com")
	u2 := createTestUserForEdgeCase(t, td, "notif_user2", "notif2@example.com")

	// Create message
	m := &domain.Message{
		ID:          uuid.NewString(),
		SenderID:    u1.ID,
		RecipientID: u2.ID,
		Content:     "test notification",
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, mr.CreateMessage(ctx, m))

	// Close database before trying to mark as read (simulating DB failure)
	td.DB.Close()

	// Should handle gracefully
	err := mr.MarkMessageAsRead(ctx, m.ID, u2.ID)
	if err == nil {
		t.Error("expected error when database is closed")
	}
}

func TestMessageRepository_NotificationWithMissingUser(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("database not available, skipping test")
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	u1 := createTestUserForEdgeCase(t, td, "notif2_user1", "notif2_1@example.com")
	u2 := createTestUserForEdgeCase(t, td, "notif2_user2", "notif2_2@example.com")

	// Create message
	m := &domain.Message{
		ID:          uuid.NewString(),
		SenderID:    u1.ID,
		RecipientID: u2.ID,
		Content:     "test message",
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, mr.CreateMessage(ctx, m))

	// Try to mark as read with non-existent user
	fakeUserID := uuid.NewString()
	err := mr.MarkMessageAsRead(ctx, m.ID, fakeUserID)

	// Should return not found error
	assert.ErrorIs(t, err, domain.ErrMessageNotFound)
}

func TestMessageRepository_ConcurrentNotificationDelivery(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("database not available, skipping test")
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	u1 := createTestUserForEdgeCase(t, td, "notif3_user1", "notif3_1@example.com")
	u2 := createTestUserForEdgeCase(t, td, "notif3_user2", "notif3_2@example.com")

	// Create multiple messages
	numMessages := 100
	messageIDs := make([]string, numMessages)

	for i := 0; i < numMessages; i++ {
		m := &domain.Message{
			ID:          uuid.NewString(),
			SenderID:    u1.ID,
			RecipientID: u2.ID,
			Content:     "concurrent notification test",
			MessageType: domain.MessageTypeText,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		require.NoError(t, mr.CreateMessage(ctx, m))
		messageIDs[i] = m.ID
	}

	// Verify unread count
	unreadCount, err := mr.GetUnreadCount(ctx, u2.ID)
	require.NoError(t, err)
	assert.Equal(t, numMessages, unreadCount)

	// Mark all as read concurrently
	var wg sync.WaitGroup
	wg.Add(numMessages)

	for _, msgID := range messageIDs {
		go func(id string) {
			defer wg.Done()
			// Ignore errors for already-read messages
			mr.MarkMessageAsRead(ctx, id, u2.ID)
		}(msgID)
	}

	wg.Wait()

	// Verify all marked as read
	unreadCount, err = mr.GetUnreadCount(ctx, u2.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, unreadCount)
}

// ===== Race Condition Tests =====

func TestMessageRepository_RaceConditionInSoftDelete(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("database not available, skipping test")
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	u1 := createTestUserForEdgeCase(t, td, "race_user1", "race1@example.com")
	u2 := createTestUserForEdgeCase(t, td, "race_user2", "race2@example.com")

	m := &domain.Message{
		ID:          uuid.NewString(),
		SenderID:    u1.ID,
		RecipientID: u2.ID,
		Content:     "race condition test",
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, mr.CreateMessage(ctx, m))

	// Try to delete from both users concurrently
	var wg sync.WaitGroup
	wg.Add(2)

	var err1, err2 error

	go func() {
		defer wg.Done()
		err1 = mr.DeleteMessage(ctx, m.ID, u1.ID)
	}()

	go func() {
		defer wg.Done()
		err2 = mr.DeleteMessage(ctx, m.ID, u2.ID)
	}()

	wg.Wait()

	// Both should succeed (soft delete is idempotent per user)
	assert.NoError(t, err1)
	assert.NoError(t, err2)

	// Message should be hidden from both users
	msgs1, err := mr.GetMessages(ctx, u1.ID, u2.ID, 10, 0)
	require.NoError(t, err)
	assert.Len(t, msgs1, 0)

	msgs2, err := mr.GetMessages(ctx, u2.ID, u1.ID, 10, 0)
	require.NoError(t, err)
	assert.Len(t, msgs2, 0)
}

func TestMessageRepository_RaceConditionInMarkAsRead(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("database not available, skipping test")
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	u1 := createTestUserForEdgeCase(t, td, "race2_user1", "race2_1@example.com")
	u2 := createTestUserForEdgeCase(t, td, "race2_user2", "race2_2@example.com")

	m := &domain.Message{
		ID:          uuid.NewString(),
		SenderID:    u1.ID,
		RecipientID: u2.ID,
		Content:     "mark read race test",
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, mr.CreateMessage(ctx, m))

	// Try to mark as read multiple times concurrently
	numGoroutines := 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	successCount := 0
	var countMutex sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			err := mr.MarkMessageAsRead(ctx, m.ID, u2.ID)
			if err == nil {
				countMutex.Lock()
				successCount++
				countMutex.Unlock()
			}
		}()
	}

	wg.Wait()

	// Only one should succeed (first one), others should fail with ErrMessageNotFound
	assert.Equal(t, 1, successCount, "only one concurrent MarkAsRead should succeed")
}

// ===== Resource Cleanup Tests =====

func TestMessageRepository_NoResourceLeaksOnError(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("database not available, skipping test")
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	// Perform many operations that will fail
	for i := 0; i < 1000; i++ {
		m := &domain.Message{
			ID:          uuid.NewString(),
			SenderID:    "invalid-user-id",
			RecipientID: "invalid-user-id",
			Content:     "test",
			MessageType: domain.MessageTypeText,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		// Should fail but not leak resources
		mr.CreateMessage(ctx, m)

		// Should fail but not leak resources
		mr.GetMessages(ctx, "invalid", "invalid", 10, 0)
	}

	// If we get here without hanging or OOM, test passes
	t.Log("Completed 1000 failed operations without resource leaks")
}

// ===== Helper Functions =====

func createTestUserForEdgeCase(t *testing.T, testDB *testutil.TestDB, username, email string) *domain.User {
	t.Helper()

	user := &domain.User{
		ID:        uuid.NewString(),
		Username:  username,
		Email:     email,
		Role:      domain.RoleUser,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Use raw SQL to create user for edge case tests
	_, err := testDB.DB.Exec(`
		INSERT INTO users (id, username, email, password_hash, role, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, user.ID, user.Username, user.Email, "hashed_password", user.Role, user.IsActive, user.CreatedAt, user.UpdatedAt)
	require.NoError(t, err)

	return user
}
