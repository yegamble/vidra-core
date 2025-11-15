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

func TestNotificationRepository_Create(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewNotificationRepository(testDB.DB)

	ctx := context.Background()
	userID := uuid.New()

	notification := &domain.Notification{
		UserID:  userID,
		Type:    domain.NotificationNewVideo,
		Title:   "New Video",
		Message: "A new video has been uploaded",
		Data: map[string]interface{}{
			"video_id": uuid.New().String(),
			"channel":  "test_channel",
		},
	}

	err := repo.Create(ctx, notification)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, notification.ID)
	assert.NotZero(t, notification.CreatedAt)

	// Verify it was created correctly
	retrieved, err := repo.GetByID(ctx, notification.ID)
	require.NoError(t, err)
	assert.Equal(t, notification.UserID, retrieved.UserID)
	assert.Equal(t, notification.Type, retrieved.Type)
	assert.Equal(t, notification.Title, retrieved.Title)
	assert.Equal(t, notification.Message, retrieved.Message)
	assert.False(t, retrieved.Read)
	assert.Nil(t, retrieved.ReadAt)
}

func TestNotificationRepository_CreateBatch(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewNotificationRepository(testDB.DB)

	ctx := context.Background()
	userID1 := uuid.New()
	userID2 := uuid.New()

	notifications := []domain.Notification{
		{
			UserID:  userID1,
			Type:    domain.NotificationNewVideo,
			Title:   "New Video 1",
			Message: "Video 1 uploaded",
			Data:    map[string]interface{}{"video_id": uuid.New().String()},
		},
		{
			UserID:  userID2,
			Type:    domain.NotificationNewVideo,
			Title:   "New Video 2",
			Message: "Video 2 uploaded",
			Data:    map[string]interface{}{"video_id": uuid.New().String()},
		},
		{
			UserID:  userID1,
			Type:    domain.NotificationComment,
			Title:   "New Comment",
			Message: "Someone commented",
			Data:    map[string]interface{}{"comment_id": uuid.New().String()},
		},
	}

	err := repo.CreateBatch(ctx, notifications)
	require.NoError(t, err)

	// Verify all were created with IDs
	for i := range notifications {
		assert.NotEqual(t, uuid.Nil, notifications[i].ID)
		assert.NotZero(t, notifications[i].CreatedAt)
	}

	// Verify they can be retrieved
	retrieved, err := repo.GetByID(ctx, notifications[0].ID)
	require.NoError(t, err)
	assert.Equal(t, notifications[0].Title, retrieved.Title)
}

func TestNotificationRepository_CreateBatch_Empty(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewNotificationRepository(testDB.DB)

	ctx := context.Background()
	err := repo.CreateBatch(ctx, []domain.Notification{})
	require.NoError(t, err)
}

func TestNotificationRepository_GetByID(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewNotificationRepository(testDB.DB)

	ctx := context.Background()
	userID := uuid.New()

	notification := createTestNotification(t, repo, ctx, userID, domain.NotificationNewVideo)

	tests := []struct {
		name    string
		id      uuid.UUID
		wantErr error
	}{
		{
			name:    "existing notification",
			id:      notification.ID,
			wantErr: nil,
		},
		{
			name:    "non-existent notification",
			id:      uuid.New(),
			wantErr: domain.ErrNotificationNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := repo.GetByID(ctx, tt.id)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, tt.id, result.ID)
			}
		})
	}
}

func TestNotificationRepository_ListByUser(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewNotificationRepository(testDB.DB)

	ctx := context.Background()
	userID1 := uuid.New()
	userID2 := uuid.New()

	// Create notifications for user1
	createTestNotification(t, repo, ctx, userID1, domain.NotificationNewVideo)
	createTestNotification(t, repo, ctx, userID1, domain.NotificationComment)
	createTestNotification(t, repo, ctx, userID1, domain.NotificationNewSubscriber)

	// Create notifications for user2
	createTestNotification(t, repo, ctx, userID2, domain.NotificationNewVideo)

	// Mark one as read
	notifications, err := repo.ListByUser(ctx, domain.NotificationFilter{UserID: userID1})
	require.NoError(t, err)
	require.Len(t, notifications, 3)
	err = repo.MarkAsRead(ctx, notifications[0].ID, userID1)
	require.NoError(t, err)

	tests := []struct {
		name      string
		filter    domain.NotificationFilter
		wantCount int
	}{
		{
			name: "all notifications for user1",
			filter: domain.NotificationFilter{
				UserID: userID1,
			},
			wantCount: 3,
		},
		{
			name: "all notifications for user2",
			filter: domain.NotificationFilter{
				UserID: userID2,
			},
			wantCount: 1,
		},
		{
			name: "only unread for user1",
			filter: domain.NotificationFilter{
				UserID: userID1,
				Unread: boolPtr(true),
			},
			wantCount: 2,
		},
		{
			name: "only read for user1",
			filter: domain.NotificationFilter{
				UserID: userID1,
				Unread: boolPtr(false),
			},
			wantCount: 1,
		},
		{
			name: "filter by type",
			filter: domain.NotificationFilter{
				UserID: userID1,
				Types:  []domain.NotificationType{domain.NotificationNewVideo},
			},
			wantCount: 1,
		},
		{
			name: "limit results",
			filter: domain.NotificationFilter{
				UserID: userID1,
				Limit:  2,
			},
			wantCount: 2,
		},
		{
			name: "offset results",
			filter: domain.NotificationFilter{
				UserID: userID1,
				Offset: 2,
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := repo.ListByUser(ctx, tt.filter)
			require.NoError(t, err)
			assert.Len(t, results, tt.wantCount)

			// Verify ordering (newest first)
			if len(results) > 1 {
				for i := 1; i < len(results); i++ {
					assert.True(t, results[i-1].CreatedAt.After(results[i].CreatedAt) ||
						results[i-1].CreatedAt.Equal(results[i].CreatedAt))
				}
			}
		})
	}
}

func TestNotificationRepository_ListByUser_DateFilters(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewNotificationRepository(testDB.DB)

	ctx := context.Background()
	userID := uuid.New()

	// Create notifications
	createTestNotification(t, repo, ctx, userID, domain.NotificationNewVideo)
	time.Sleep(10 * time.Millisecond)
	createTestNotification(t, repo, ctx, userID, domain.NotificationComment)
	time.Sleep(10 * time.Millisecond)
	createTestNotification(t, repo, ctx, userID, domain.NotificationNewSubscriber)

	allNotifications, err := repo.ListByUser(ctx, domain.NotificationFilter{UserID: userID})
	require.NoError(t, err)
	require.Len(t, allNotifications, 3)

	middleTime := allNotifications[1].CreatedAt

	tests := []struct {
		name      string
		startDate *time.Time
		endDate   *time.Time
		wantMin   int // minimum expected
		wantMax   int // maximum expected
	}{
		{
			name:      "start date filter",
			startDate: &middleTime,
			wantMin:   1,
			wantMax:   2,
		},
		{
			name:    "end date filter",
			endDate: &middleTime,
			wantMin: 1,
			wantMax: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := domain.NotificationFilter{
				UserID:    userID,
				StartDate: tt.startDate,
				EndDate:   tt.endDate,
			}
			results, err := repo.ListByUser(ctx, filter)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(results), tt.wantMin)
			assert.LessOrEqual(t, len(results), tt.wantMax)
		})
	}
}

func TestNotificationRepository_MarkAsRead(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewNotificationRepository(testDB.DB)

	ctx := context.Background()
	userID := uuid.New()
	otherUserID := uuid.New()

	notification := createTestNotification(t, repo, ctx, userID, domain.NotificationNewVideo)

	tests := []struct {
		name    string
		id      uuid.UUID
		userID  uuid.UUID
		wantErr error
	}{
		{
			name:    "mark own notification as read",
			id:      notification.ID,
			userID:  userID,
			wantErr: nil,
		},
		{
			name:    "mark already read notification",
			id:      notification.ID,
			userID:  userID,
			wantErr: domain.ErrNotificationNotFound, // Already read, won't update
		},
		{
			name:    "mark other user's notification",
			id:      notification.ID,
			userID:  otherUserID,
			wantErr: domain.ErrNotificationNotFound,
		},
		{
			name:    "non-existent notification",
			id:      uuid.New(),
			userID:  userID,
			wantErr: domain.ErrNotificationNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.MarkAsRead(ctx, tt.id, tt.userID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)

				// Verify it was marked as read
				retrieved, err := repo.GetByID(ctx, tt.id)
				require.NoError(t, err)
				assert.True(t, retrieved.Read)
				assert.NotNil(t, retrieved.ReadAt)
			}
		})
	}
}

func TestNotificationRepository_MarkAllAsRead(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewNotificationRepository(testDB.DB)

	ctx := context.Background()
	userID := uuid.New()

	// Create multiple unread notifications
	createTestNotification(t, repo, ctx, userID, domain.NotificationNewVideo)
	createTestNotification(t, repo, ctx, userID, domain.NotificationComment)
	createTestNotification(t, repo, ctx, userID, domain.NotificationNewSubscriber)

	// Verify all are unread
	count, err := repo.GetUnreadCount(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	// Mark all as read
	err = repo.MarkAllAsRead(ctx, userID)
	require.NoError(t, err)

	// Verify all are now read
	count, err = repo.GetUnreadCount(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Verify individually
	notifications, err := repo.ListByUser(ctx, domain.NotificationFilter{UserID: userID})
	require.NoError(t, err)
	for _, notif := range notifications {
		assert.True(t, notif.Read)
		assert.NotNil(t, notif.ReadAt)
	}
}

func TestNotificationRepository_Delete(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewNotificationRepository(testDB.DB)

	ctx := context.Background()
	userID := uuid.New()
	otherUserID := uuid.New()

	notification := createTestNotification(t, repo, ctx, userID, domain.NotificationNewVideo)

	tests := []struct {
		name    string
		id      uuid.UUID
		userID  uuid.UUID
		wantErr error
	}{
		{
			name:    "delete other user's notification",
			id:      notification.ID,
			userID:  otherUserID,
			wantErr: domain.ErrNotificationNotFound,
		},
		{
			name:    "delete non-existent notification",
			id:      uuid.New(),
			userID:  userID,
			wantErr: domain.ErrNotificationNotFound,
		},
		{
			name:    "delete own notification",
			id:      notification.ID,
			userID:  userID,
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.Delete(ctx, tt.id, tt.userID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)

				// Verify it was deleted
				_, err := repo.GetByID(ctx, tt.id)
				assert.ErrorIs(t, err, domain.ErrNotificationNotFound)
			}
		})
	}
}

func TestNotificationRepository_DeleteOldRead(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewNotificationRepository(testDB.DB)

	ctx := context.Background()
	userID := uuid.New()

	// Create and mark as read
	notif1 := createTestNotification(t, repo, ctx, userID, domain.NotificationNewVideo)
	err := repo.MarkAsRead(ctx, notif1.ID, userID)
	require.NoError(t, err)

	// Create another unread
	createTestNotification(t, repo, ctx, userID, domain.NotificationComment)

	// Try to delete old read (should delete the read one if it's old enough)
	// Since we just created it, use 0 duration to delete anything read
	deleted, err := repo.DeleteOldRead(ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	// Verify only unread remains
	notifications, err := repo.ListByUser(ctx, domain.NotificationFilter{UserID: userID})
	require.NoError(t, err)
	assert.Len(t, notifications, 1)
	assert.False(t, notifications[0].Read)
}

func TestNotificationRepository_GetUnreadCount(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewNotificationRepository(testDB.DB)

	ctx := context.Background()
	userID := uuid.New()

	// Initially zero
	count, err := repo.GetUnreadCount(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Create unread notifications
	notif1 := createTestNotification(t, repo, ctx, userID, domain.NotificationNewVideo)
	createTestNotification(t, repo, ctx, userID, domain.NotificationComment)

	count, err = repo.GetUnreadCount(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Mark one as read
	err = repo.MarkAsRead(ctx, notif1.ID, userID)
	require.NoError(t, err)

	count, err = repo.GetUnreadCount(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestNotificationRepository_GetStats(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewNotificationRepository(testDB.DB)

	ctx := context.Background()
	userID := uuid.New()

	// Create various notifications
	notif1 := createTestNotification(t, repo, ctx, userID, domain.NotificationNewVideo)
	createTestNotification(t, repo, ctx, userID, domain.NotificationNewVideo)
	createTestNotification(t, repo, ctx, userID, domain.NotificationComment)
	createTestNotification(t, repo, ctx, userID, domain.NotificationNewSubscriber)

	// Mark one as read
	err := repo.MarkAsRead(ctx, notif1.ID, userID)
	require.NoError(t, err)

	stats, err := repo.GetStats(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 4, stats.TotalCount)
	assert.Equal(t, 3, stats.UnreadCount)
	assert.Equal(t, 2, stats.ByType[domain.NotificationNewVideo])
	assert.Equal(t, 1, stats.ByType[domain.NotificationComment])
	assert.Equal(t, 1, stats.ByType[domain.NotificationNewSubscriber])
}

func TestNotificationRepository_GetStats_Empty(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewNotificationRepository(testDB.DB)

	ctx := context.Background()
	userID := uuid.New()

	stats, err := repo.GetStats(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.TotalCount)
	assert.Equal(t, 0, stats.UnreadCount)
	assert.Empty(t, stats.ByType)
}

// Helper functions

func createTestNotification(t *testing.T, repo *NotificationRepository, ctx context.Context, userID uuid.UUID, notifType domain.NotificationType) *domain.Notification {
	t.Helper()

	notification := &domain.Notification{
		UserID:  userID,
		Type:    notifType,
		Title:   "Test Notification",
		Message: "Test message",
		Data: map[string]interface{}{
			"test_key": "test_value",
		},
	}

	err := repo.Create(ctx, notification)
	require.NoError(t, err)

	return notification
}

func boolPtr(b bool) *bool {
	return &b
}
