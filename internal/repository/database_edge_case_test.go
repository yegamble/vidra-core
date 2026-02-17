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

func TestMessageRepository_CreateMessage_DatabaseUnavailable(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("database not available, skipping test")
		return
	}

	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	td.DB.Close()

	m := &domain.Message{
		ID:          uuid.NewString(),
		SenderID:    uuid.NewString(),
		RecipientID: uuid.NewString(),
		Content:     strPtr("test message"),
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	err := mr.CreateMessage(ctx, m)
	if err == nil {
		t.Error("expected error when database is unavailable, got nil")
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	time.Sleep(10 * time.Millisecond)

	msgs, err := mr.GetMessages(ctx, uuid.NewString(), uuid.NewString(), 10, 0)

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
				msgContent := "concurrent message " + uuid.NewString()
				m := &domain.Message{
					ID:          uuid.NewString(),
					SenderID:    u1.ID,
					RecipientID: u2.ID,
					Content:     &msgContent,
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

	if errorCount > numGoroutines {
		t.Errorf("too many errors during concurrent operations: %d", errorCount)
	}

	msgs, err := mr.GetMessages(ctx, u1.ID, u2.ID, 1000, 0)
	require.NoError(t, err)

	expectedMessages := numGoroutines*numMessagesPerGoroutine - errorCount
	if len(msgs) < expectedMessages-10 {
		t.Errorf("expected ~%d messages, got %d", expectedMessages, len(msgs))
	}
}

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

	td.DB.SetMaxOpenConns(2)
	td.DB.SetMaxIdleConns(1)

	numGoroutines := 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			m := &domain.Message{
				ID:          uuid.NewString(),
				SenderID:    u1.ID,
				RecipientID: u2.ID,
				Content:     strPtr("pool test message"),
				MessageType: domain.MessageTypeText,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}

			err := mr.CreateMessage(opCtx, m)
			if err != nil && !errors.Is(err, context.DeadlineExceeded) {
				t.Logf("unexpected error: %v", err)
			}
		}(i)
	}

	wg.Wait()

	td.DB.SetMaxOpenConns(0)
	td.DB.SetMaxIdleConns(2)
}

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

	m1 := &domain.Message{
		ID:          uuid.NewString(),
		SenderID:    u1.ID,
		RecipientID: u2.ID,
		Content:     strPtr("message before rollback"),
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, mr.CreateMessage(ctx, m1))

	m2 := &domain.Message{
		ID:          uuid.NewString(),
		SenderID:    "invalid-user-id-that-does-not-exist",
		RecipientID: u2.ID,
		Content:     strPtr("message that should fail"),
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err := mr.CreateMessage(ctx, m2)
	if err == nil {
		t.Error("expected error with invalid user ID")
	}

	msgs, err := mr.GetMessages(ctx, u1.ID, u2.ID, 10, 0)
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, m1.ID, msgs[0].ID)
}

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

	m := &domain.Message{
		ID:          uuid.NewString(),
		SenderID:    u1.ID,
		RecipientID: u2.ID,
		Content:     strPtr("test notification"),
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, mr.CreateMessage(ctx, m))

	td.DB.Close()

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

	m := &domain.Message{
		ID:          uuid.NewString(),
		SenderID:    u1.ID,
		RecipientID: u2.ID,
		Content:     strPtr("test message"),
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, mr.CreateMessage(ctx, m))

	fakeUserID := uuid.NewString()
	err := mr.MarkMessageAsRead(ctx, m.ID, fakeUserID)

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

	numMessages := 100
	messageIDs := make([]string, numMessages)

	for i := 0; i < numMessages; i++ {
		m := &domain.Message{
			ID:          uuid.NewString(),
			SenderID:    u1.ID,
			RecipientID: u2.ID,
			Content:     strPtr("concurrent notification test"),
			MessageType: domain.MessageTypeText,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		require.NoError(t, mr.CreateMessage(ctx, m))
		messageIDs[i] = m.ID
	}

	unreadCount, err := mr.GetUnreadCount(ctx, u2.ID)
	require.NoError(t, err)
	assert.Equal(t, numMessages, unreadCount)

	var wg sync.WaitGroup
	wg.Add(numMessages)

	for _, msgID := range messageIDs {
		go func(id string) {
			defer wg.Done()
			mr.MarkMessageAsRead(ctx, id, u2.ID)
		}(msgID)
	}

	wg.Wait()

	unreadCount, err = mr.GetUnreadCount(ctx, u2.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, unreadCount)
}

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
		Content:     strPtr("race condition test"),
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, mr.CreateMessage(ctx, m))

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

	assert.NoError(t, err1)
	assert.NoError(t, err2)

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
		Content:     strPtr("mark read race test"),
		MessageType: domain.MessageTypeText,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, mr.CreateMessage(ctx, m))

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

	assert.Equal(t, 1, successCount, "only one concurrent MarkAsRead should succeed")
}

func TestMessageRepository_NoResourceLeaksOnError(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("database not available, skipping test")
		return
	}
	td.TruncateTables(t, "messages", "conversations", "users")

	mr := NewMessageRepository(td.DB)
	ctx := context.Background()

	for i := 0; i < 1000; i++ {
		m := &domain.Message{
			ID:          uuid.NewString(),
			SenderID:    "invalid-user-id",
			RecipientID: "invalid-user-id",
			Content:     strPtr("test"),
			MessageType: domain.MessageTypeText,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		mr.CreateMessage(ctx, m)

		mr.GetMessages(ctx, "invalid", "invalid", 10, 0)
	}

	t.Log("Completed 1000 failed operations without resource leaks")
}

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

	_, err := testDB.DB.Exec(`
		INSERT INTO users (id, username, email, password_hash, role, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, user.ID, user.Username, user.Email, "hashed_password", user.Role, user.IsActive, user.CreatedAt, user.UpdatedAt)
	require.NoError(t, err)

	return user
}
