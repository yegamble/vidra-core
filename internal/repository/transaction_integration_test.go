package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/testutil"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMultiRepositoryTransaction tests transactions across multiple repositories
func TestMultiRepositoryTransaction(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		return
	}
	db := td.DB
	tm := NewTransactionManager(db)

	// Initialize all repositories
	userRepo := NewUserRepository(db)
	videoRepo := NewVideoRepository(db)
	commentRepo := NewCommentRepository(db)
	ratingRepo := NewRatingRepository(db)
	subRepo := NewSubscriptionRepository(db)

	t.Run("complex multi-repository transaction", func(t *testing.T) {
		ctx := context.Background()

		// Data for our complex operation
		user1ID := uuid.New()
		user2ID := uuid.New()
		channel1ID := uuid.New()
		channel2ID := uuid.New()
		videoID := uuid.New()
		commentID := uuid.New()

		// Perform complex operation in a single transaction
		err := tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
			// Add transaction to context
			txCtx := WithTx(ctx, tx)

			// Step 1: Create two users
			user1 := &domain.User{
				ID:          user1ID.String(),
				Username:    "creator",
				Email:       "creator@example.com",
				DisplayName: "Content Creator",
				Role:        domain.RoleUser,
				IsActive:    true,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}
			if err := userRepo.Create(txCtx, user1, "password123"); err != nil {
				return fmt.Errorf("create user1: %w", err)
			}

			user2 := &domain.User{
				ID:          user2ID.String(),
				Username:    "viewer",
				Email:       "viewer@example.com",
				DisplayName: "Content Viewer",
				Role:        domain.RoleUser,
				IsActive:    true,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}
			if err := userRepo.Create(txCtx, user2, "password456"); err != nil {
				return fmt.Errorf("create user2: %w", err)
			}

			// Step 2: Create channels directly (since userRepo.Create might create default channels)
			_, err := tx.ExecContext(ctx, `
				INSERT INTO channels (id, account_id, handle, display_name, created_at, updated_at)
				VALUES
					($1, $2, 'creator_channel', 'Creator Channel', NOW(), NOW()),
					($3, $4, 'viewer_channel', 'Viewer Channel', NOW(), NOW())
			`, channel1ID, user1ID, channel2ID, user2ID)
			if err != nil {
				return fmt.Errorf("create channels: %w", err)
			}

			// Step 3: Create a video
			video := &domain.Video{
				ID:          videoID.String(),
				Title:       "Transaction Test Video",
				Description: "Testing multi-repository transactions",
				Duration:    300,
				Privacy:     domain.PrivacyPublic,
				Status:      domain.StatusCompleted,
				UploadDate:  time.Now(),
				UserID:      user1ID.String(),
				ChannelID:   channel1ID,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}
			if err := videoRepo.Create(txCtx, video); err != nil {
				return fmt.Errorf("create video: %w", err)
			}

			// Step 4: User2 subscribes to User1's channel
			if err := subRepo.SubscribeToChannel(txCtx, user2ID, channel1ID); err != nil {
				return fmt.Errorf("subscribe: %w", err)
			}

			// Step 5: User2 comments on the video
			comment := &domain.Comment{
				ID:      commentID,
				VideoID: videoID,
				UserID:  user2ID,
				Body:    "Great video!",
			}
			if err := commentRepo.Create(txCtx, comment); err != nil {
				return fmt.Errorf("create comment: %w", err)
			}
			commentID = comment.ID

			// Step 6: User2 likes the video
			if err := ratingRepo.SetRating(txCtx, user2ID, videoID, domain.RatingLike); err != nil {
				return fmt.Errorf("set rating: %w", err)
			}

			return nil
		})

		require.NoError(t, err)

		// Verify all operations were committed successfully

		// Check users exist
		user1, err := userRepo.GetByID(ctx, user1ID.String())
		require.NoError(t, err)
		assert.Equal(t, "creator", user1.Username)

		user2, err := userRepo.GetByID(ctx, user2ID.String())
		require.NoError(t, err)
		assert.Equal(t, "viewer", user2.Username)

		// Check video exists
		video, err := videoRepo.GetByID(ctx, videoID.String())
		require.NoError(t, err)
		assert.Equal(t, "Transaction Test Video", video.Title)

		// Check subscription exists
		isSubscribed, err := subRepo.IsSubscribed(ctx, user2ID, channel1ID)
		require.NoError(t, err)
		assert.True(t, isSubscribed)

		// Check comment exists
		comment, err := commentRepo.GetByID(ctx, commentID)
		require.NoError(t, err)
		assert.Equal(t, "Great video!", comment.Body)

		// Check rating exists
		rating, err := ratingRepo.GetRating(ctx, user2ID, videoID)
		require.NoError(t, err)
		assert.Equal(t, domain.RatingLike, rating)
	})

	t.Run("multi-repository transaction rollback", func(t *testing.T) {
		ctx := context.Background()

		// Data for our complex operation
		user3ID := uuid.New()
		video2ID := uuid.New()

		// Perform complex operation that will fail
		err := tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
			txCtx := WithTx(ctx, tx)

			// Create a user
			user := &domain.User{
				ID:          user3ID.String(),
				Username:    "failuser",
				Email:       "failuser@example.com",
				DisplayName: "Fail User",
				Role:        domain.RoleUser,
				IsActive:    true,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}
			if err := userRepo.Create(txCtx, user, "password789"); err != nil {
				return fmt.Errorf("create rollback user: %w", err)
			}

			// Create a video
			video := &domain.Video{
				ID:          video2ID.String(),
				Title:       "Rollback Test Video",
				Description: "This should be rolled back",
				Duration:    200,
				Privacy:     domain.PrivacyPublic,
				Status:      domain.StatusCompleted,
				UploadDate:  time.Now(),
				UserID:      user3ID.String(),
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}
			if err := videoRepo.Create(txCtx, video); err != nil {
				return fmt.Errorf("create rollback video: %w", err)
			}

			// Force an error to trigger rollback
			return errors.New("intentional failure for rollback test")
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "intentional failure")

		// Verify NOTHING was committed

		// User should not exist
		_, err = userRepo.GetByID(ctx, user3ID.String())
		assert.Equal(t, domain.ErrUserNotFound, err)

		// Video should not exist
		_, err = videoRepo.GetByID(ctx, video2ID.String())
		assert.Error(t, err)
	})
}

// TestTransactionDeadlockRetry tests retry logic for deadlocks
func TestTransactionDeadlockRetry(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		return
	}
	db := td.DB
	tm := NewTransactionManager(db)

	t.Run("retry on retryable error", func(t *testing.T) {
		ctx := context.Background()
		attempts := 0

		err := tm.WithRetry(ctx, 3, nil, func(tx *sqlx.Tx) error {
			attempts++

			// Simulate a retryable error on first two attempts
			if attempts < 3 {
				return RetryableError{Err: errors.New("simulated deadlock")}
			}

			// Succeed on third attempt
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 3, attempts, "Should have retried until success")
	})

	t.Run("fail after max retries", func(t *testing.T) {
		ctx := context.Background()
		attempts := 0

		err := tm.WithRetry(ctx, 3, nil, func(tx *sqlx.Tx) error {
			attempts++
			// Always return a retryable error
			return RetryableError{Err: errors.New("persistent deadlock")}
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed after 3 retries")
		assert.Equal(t, 3, attempts, "Should have tried exactly 3 times")
	})

	t.Run("no retry for non-retryable errors", func(t *testing.T) {
		ctx := context.Background()
		attempts := 0

		err := tm.WithRetry(ctx, 3, nil, func(tx *sqlx.Tx) error {
			attempts++
			// Return a non-retryable error
			return errors.New("non-retryable error")
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "non-retryable error")
		assert.Equal(t, 1, attempts, "Should not retry for non-retryable errors")
	})
}

// TestNestedTransactionContext tests passing transaction through context
func TestNestedTransactionContext(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		return
	}
	db := td.DB
	tm := NewTransactionManager(db)
	userRepo := NewUserRepository(db)
	videoRepo := NewVideoRepository(db)

	t.Run("nested repository calls use same transaction", func(t *testing.T) {
		ctx := context.Background()

		userID := uuid.New()
		videoID := uuid.New()

		// Helper function that creates a user and video in the same transaction
		createUserAndVideo := func(ctx context.Context) error {
			user := &domain.User{
				ID:          userID.String(),
				Username:    "nested",
				Email:       "nested@example.com",
				DisplayName: "Nested User",
				Role:        domain.RoleUser,
				IsActive:    true,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}
			if err := userRepo.Create(ctx, user, "password"); err != nil {
				return err
			}

			video := &domain.Video{
				ID:          videoID.String(),
				Title:       "Nested Transaction Video",
				Description: "Testing nested calls",
				Duration:    100,
				Privacy:     domain.PrivacyPublic,
				Status:      domain.StatusCompleted,
				UploadDate:  time.Now(),
				UserID:      userID.String(),
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}
			return videoRepo.Create(ctx, video)
		}

		// Execute in transaction
		err := tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
			txCtx := WithTx(ctx, tx)
			return createUserAndVideo(txCtx)
		})

		require.NoError(t, err)

		// Verify both were created
		user, err := userRepo.GetByID(ctx, userID.String())
		require.NoError(t, err)
		assert.Equal(t, "nested", user.Username)

		video, err := videoRepo.GetByID(ctx, videoID.String())
		require.NoError(t, err)
		assert.Equal(t, "Nested Transaction Video", video.Title)
	})

	t.Run("GetExecutor returns transaction when available", func(t *testing.T) {
		ctx := context.Background()

		err := tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
			txCtx := WithTx(ctx, tx)

			// GetExecutor should return the transaction
			exec := GetExecutor(txCtx, db)
			assert.Equal(t, tx, exec)

			// Without transaction in context, should return db
			exec2 := GetExecutor(ctx, db)
			assert.Equal(t, db, exec2)

			return nil
		})

		require.NoError(t, err)
	})
}

// TestConcurrentTransactions tests concurrent transaction execution
func TestConcurrentTransactions(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		return
	}
	db := td.DB
	tm := NewTransactionManager(db)
	userRepo := NewUserRepository(db)

	t.Run("multiple concurrent transactions", func(t *testing.T) {
		ctx := context.Background()

		// Run multiple transactions concurrently
		numGoroutines := 5
		done := make(chan error, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func(index int) {
				err := tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
					txCtx := WithTx(ctx, tx)

					user := &domain.User{
						ID:          uuid.New().String(),
						Username:    "concurrent" + string(rune('0'+index)),
						Email:       "concurrent" + string(rune('0'+index)) + "@example.com",
						DisplayName: "Concurrent User",
						Role:        domain.RoleUser,
						IsActive:    true,
						CreatedAt:   time.Now(),
						UpdatedAt:   time.Now(),
					}
					return userRepo.Create(txCtx, user, "password")
				})
				done <- err
			}(i)
		}

		// Wait for all goroutines to complete
		for i := 0; i < numGoroutines; i++ {
			err := <-done
			assert.NoError(t, err)
		}

		// Verify all users were created
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM users WHERE username LIKE 'concurrent%'").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, numGoroutines, count)
	})
}
