package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/testutil"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTransactionManager_WithTransaction tests basic transaction functionality
func TestTransactionManager_WithTransaction(t *testing.T) {
	t.Parallel()

	db := testutil.SetupTestDB(t)
	tm := NewTransactionManager(db)

	t.Run("successful transaction commits", func(t *testing.T) {
		ctx := context.Background()
		userID := uuid.New()

		err := tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
			// Create a test user within transaction
			query := `INSERT INTO users (id, username, email, role, created_at, updated_at)
					  VALUES ($1, $2, $3, $4, $5, $6)`

			_, err := tx.ExecContext(ctx, query,
				userID, "testuser", "test@example.com", "user",
				time.Now(), time.Now())
			return err
		})

		require.NoError(t, err)

		// Verify user was committed
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM users WHERE id = $1", userID).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("failed transaction rolls back", func(t *testing.T) {
		ctx := context.Background()
		userID := uuid.New()

		err := tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
			// Create a test user within transaction
			query := `INSERT INTO users (id, username, email, role, created_at, updated_at)
					  VALUES ($1, $2, $3, $4, $5, $6)`

			_, err := tx.ExecContext(ctx, query,
				userID, "testuser2", "test2@example.com", "user",
				time.Now(), time.Now())
			if err != nil {
				return err
			}

			// Force an error to trigger rollback
			return errors.New("forced error")
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "forced error")

		// Verify user was NOT committed
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM users WHERE id = $1", userID).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("panic triggers rollback", func(t *testing.T) {
		ctx := context.Background()
		userID := uuid.New()

		assert.Panics(t, func() {
			_ = tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
				// Create a test user within transaction
				query := `INSERT INTO users (id, username, email, role, created_at, updated_at)
						  VALUES ($1, $2, $3, $4, $5, $6)`

				_, _ = tx.ExecContext(ctx, query,
					userID, "testuser3", "test3@example.com", "user",
					time.Now(), time.Now())

				// Panic to test rollback
				panic("test panic")
			})
		})

		// Verify user was NOT committed
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM users WHERE id = $1", userID).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

// TestUserRepository_CreateWithTransaction tests user creation with transaction
func TestUserRepository_CreateWithTransaction(t *testing.T) {
	t.Parallel()

	db := testutil.SetupTestDB(t)
	repo := NewUserRepository(db)

	t.Run("create user with channel succeeds", func(t *testing.T) {
		ctx := context.Background()
		user := &domain.User{
			ID:          uuid.New(),
			Username:    "txuser1",
			Email:       "txuser1@example.com",
			DisplayName: "TX User 1",
			Bio:         "Test user bio",
			Role:        domain.UserRoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		err := repo.Create(ctx, user, "hashedpassword123")
		require.NoError(t, err)

		// Verify user was created
		fetchedUser, err := repo.GetByID(ctx, user.ID.String())
		require.NoError(t, err)
		assert.Equal(t, user.Username, fetchedUser.Username)
		assert.Equal(t, user.Email, fetchedUser.Email)
	})

	t.Run("create user rollback on duplicate email", func(t *testing.T) {
		ctx := context.Background()

		// Create first user
		user1 := &domain.User{
			ID:          uuid.New(),
			Username:    "txuser2",
			Email:       "duplicate@example.com",
			DisplayName: "TX User 2",
			Role:        domain.UserRoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err := repo.Create(ctx, user1, "hashedpassword123")
		require.NoError(t, err)

		// Try to create second user with same email (should fail)
		user2 := &domain.User{
			ID:          uuid.New(),
			Username:    "txuser3",
			Email:       "duplicate@example.com", // Same email
			DisplayName: "TX User 3",
			Role:        domain.UserRoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err = repo.Create(ctx, user2, "hashedpassword456")
		require.Error(t, err)

		// Verify second user was not created
		_, err = repo.GetByID(ctx, user2.ID.String())
		assert.Equal(t, domain.ErrUserNotFound, err)
	})
}

// TestVideoRepository_TransactionSupport tests video repository with transactions
func TestVideoRepository_TransactionSupport(t *testing.T) {
	t.Parallel()

	db := testutil.SetupTestDB(t)
	videoRepo := NewVideoRepository(db)
	tm := NewTransactionManager(db)

	t.Run("video operations within transaction", func(t *testing.T) {
		ctx := context.Background()

		// First create a user for the video
		userID := uuid.New()
		_, err := db.Exec(`
			INSERT INTO users (id, username, email, role, created_at, updated_at)
			VALUES ($1, 'videouser', 'videouser@example.com', 'user', NOW(), NOW())
		`, userID)
		require.NoError(t, err)

		video := &domain.Video{
			ID:          uuid.New(),
			Title:       "Test Video",
			Description: "Test Description",
			Duration:    120,
			Views:       0,
			Privacy:     domain.VideoPrivacyPublic,
			Status:      domain.ProcessingStatusPending,
			UploadDate:  time.Now(),
			UserID:      userID,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		// Create video in transaction
		err = tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
			// Add transaction to context
			txCtx := WithTx(ctx, tx)
			return videoRepo.Create(txCtx, video)
		})
		require.NoError(t, err)

		// Verify video was created
		fetchedVideo, err := videoRepo.GetByID(ctx, video.ID.String())
		require.NoError(t, err)
		assert.Equal(t, video.Title, fetchedVideo.Title)

		// Update video in transaction that fails
		video.Title = "Updated Title"
		err = tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
			txCtx := WithTx(ctx, tx)
			err := videoRepo.Update(txCtx, video)
			if err != nil {
				return err
			}
			// Force rollback
			return errors.New("forced rollback")
		})
		require.Error(t, err)

		// Verify title was NOT updated
		fetchedVideo, err = videoRepo.GetByID(ctx, video.ID.String())
		require.NoError(t, err)
		assert.Equal(t, "Test Video", fetchedVideo.Title) // Original title
	})
}

// TestSubscriptionRepository_AtomicOperations tests atomic subscription operations
func TestSubscriptionRepository_AtomicOperations(t *testing.T) {
	t.Parallel()

	db := testutil.SetupTestDB(t)
	subRepo := NewSubscriptionRepository(db)

	t.Run("subscribe with atomic check", func(t *testing.T) {
		ctx := context.Background()

		// Create users and channel
		subscriberID := uuid.New()
		channelOwnerID := uuid.New()
		channelID := uuid.New()

		// Create users
		_, err := db.Exec(`
			INSERT INTO users (id, username, email, role, created_at, updated_at)
			VALUES
				($1, 'subscriber', 'subscriber@example.com', 'user', NOW(), NOW()),
				($2, 'owner', 'owner@example.com', 'user', NOW(), NOW())
		`, subscriberID, channelOwnerID)
		require.NoError(t, err)

		// Create channel
		_, err = db.Exec(`
			INSERT INTO channels (id, account_id, handle, display_name, created_at, updated_at)
			VALUES ($1, $2, 'testchannel', 'Test Channel', NOW(), NOW())
		`, channelID, channelOwnerID)
		require.NoError(t, err)

		// Subscribe should succeed
		err = subRepo.SubscribeToChannel(ctx, subscriberID, channelID)
		require.NoError(t, err)

		// Verify subscription exists
		isSubscribed, err := subRepo.IsSubscribed(ctx, subscriberID, channelID)
		require.NoError(t, err)
		assert.True(t, isSubscribed)

		// Try to subscribe to own channel (should fail atomically)
		err = subRepo.SubscribeToChannel(ctx, channelOwnerID, channelID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot subscribe to your own channel")
	})
}

// TestUploadRepository_AtomicChunkRecording tests atomic chunk recording
func TestUploadRepository_AtomicChunkRecording(t *testing.T) {
	t.Parallel()

	db := testutil.SetupTestDB(t)
	uploadRepo := NewUploadRepository(db)

	t.Run("record chunk atomically", func(t *testing.T) {
		ctx := context.Background()

		// Create user
		userID := uuid.New()
		_, err := db.Exec(`
			INSERT INTO users (id, username, email, role, created_at, updated_at)
			VALUES ($1, 'uploader', 'uploader@example.com', 'user', NOW(), NOW())
		`, userID)
		require.NoError(t, err)

		// Create upload session
		session := &domain.UploadSession{
			ID:             uuid.New().String(),
			VideoID:        uuid.New().String(),
			UserID:         userID.String(),
			FileName:       "test.mp4",
			FileSize:       1024 * 1024, // 1MB
			ChunkSize:      1024,        // 1KB chunks
			TotalChunks:    1024,
			UploadedChunks: []int{},
			Status:         "active",
			TempFilePath:   "/tmp/test.mp4",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
			ExpiresAt:      time.Now().Add(1 * time.Hour),
		}
		err = uploadRepo.CreateSession(ctx, session)
		require.NoError(t, err)

		// Record chunk atomically
		err = uploadRepo.RecordChunk(ctx, session.ID, 0)
		require.NoError(t, err)

		// Verify chunk was recorded
		isUploaded, err := uploadRepo.IsChunkUploaded(ctx, session.ID, 0)
		require.NoError(t, err)
		assert.True(t, isUploaded)

		// Recording same chunk again should be idempotent
		err = uploadRepo.RecordChunk(ctx, session.ID, 0)
		require.NoError(t, err)

		// Record another chunk
		err = uploadRepo.RecordChunk(ctx, session.ID, 1)
		require.NoError(t, err)

		// Verify both chunks are recorded
		chunks, err := uploadRepo.GetUploadedChunks(ctx, session.ID)
		require.NoError(t, err)
		assert.Contains(t, chunks, 0)
		assert.Contains(t, chunks, 1)
	})
}

// TestTransactionManager_IsolationLevels tests different isolation levels
func TestTransactionManager_IsolationLevels(t *testing.T) {
	t.Parallel()

	db := testutil.SetupTestDB(t)
	tm := NewTransactionManager(db)

	t.Run("serializable isolation", func(t *testing.T) {
		ctx := context.Background()

		err := tm.WithSerializableTransaction(ctx, func(tx *sqlx.Tx) error {
			// Perform operations at serializable isolation level
			var count int
			err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count)
			return err
		})
		assert.NoError(t, err)
	})

	t.Run("read-only transaction", func(t *testing.T) {
		ctx := context.Background()

		err := tm.WithReadOnlyTransaction(ctx, func(tx *sqlx.Tx) error {
			// Read operations succeed
			var count int
			err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count)
			if err != nil {
				return err
			}

			// Write operations should fail in read-only transaction
			_, err = tx.ExecContext(ctx,
				"INSERT INTO users (id, username, email, role, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6)",
				uuid.New(), "readonly", "readonly@example.com", "user", time.Now(), time.Now())

			// We expect this to fail
			if err != nil {
				return nil // Expected error, return nil to commit the read-only transaction
			}

			return errors.New("expected write to fail in read-only transaction")
		})
		assert.NoError(t, err)
	})
}

// TestCommentRepository_TransactionSupport tests comment repository with transactions
func TestCommentRepository_TransactionSupport(t *testing.T) {
	t.Parallel()

	db := testutil.SetupTestDB(t)
	commentRepo := NewCommentRepository(db)
	tm := NewTransactionManager(db)

	t.Run("create comment in transaction", func(t *testing.T) {
		ctx := context.Background()

		// Create user and video first
		userID := uuid.New()
		videoID := uuid.New()

		_, err := db.Exec(`
			INSERT INTO users (id, username, email, role, created_at, updated_at)
			VALUES ($1, 'commenter', 'commenter@example.com', 'user', NOW(), NOW())
		`, userID)
		require.NoError(t, err)

		_, err = db.Exec(`
			INSERT INTO videos (id, title, description, user_id, status, privacy, created_at, updated_at)
			VALUES ($1, 'Test Video', 'Description', $2, 'completed', 'public', NOW(), NOW())
		`, videoID, userID)
		require.NoError(t, err)

		comment := &domain.Comment{
			VideoID: videoID,
			UserID:  userID,
			Body:    "Test comment",
		}

		err = tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
			txCtx := WithTx(ctx, tx)
			return commentRepo.Create(txCtx, comment)
		})
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, comment.ID)

		// Verify comment was created
		fetchedComment, err := commentRepo.GetByID(ctx, comment.ID)
		require.NoError(t, err)
		assert.Equal(t, comment.Body, fetchedComment.Body)
	})
}

// TestRatingRepository_TransactionSupport tests rating repository with transactions
func TestRatingRepository_TransactionSupport(t *testing.T) {
	t.Parallel()

	db := testutil.SetupTestDB(t)
	ratingRepo := NewRatingRepository(db)
	tm := NewTransactionManager(db)

	t.Run("set rating in transaction", func(t *testing.T) {
		ctx := context.Background()

		// Create user and video first
		userID := uuid.New()
		videoID := uuid.New()

		_, err := db.Exec(`
			INSERT INTO users (id, username, email, role, created_at, updated_at)
			VALUES ($1, 'rater', 'rater@example.com', 'user', NOW(), NOW())
		`, userID)
		require.NoError(t, err)

		_, err = db.Exec(`
			INSERT INTO videos (id, title, description, user_id, status, privacy, created_at, updated_at)
			VALUES ($1, 'Rated Video', 'Description', $2, 'completed', 'public', NOW(), NOW())
		`, videoID, userID)
		require.NoError(t, err)

		// Set rating in transaction
		err = tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
			txCtx := WithTx(ctx, tx)
			return ratingRepo.SetRating(txCtx, userID, videoID, domain.RatingLike)
		})
		require.NoError(t, err)

		// Verify rating was set
		rating, err := ratingRepo.GetRating(ctx, userID, videoID)
		require.NoError(t, err)
		assert.Equal(t, domain.RatingLike, rating)

		// Update rating in failed transaction
		err = tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
			txCtx := WithTx(ctx, tx)
			err := ratingRepo.SetRating(txCtx, userID, videoID, domain.RatingDislike)
			if err != nil {
				return err
			}
			// Force rollback
			return errors.New("forced rollback")
		})
		require.Error(t, err)

		// Verify rating was NOT changed
		rating, err = ratingRepo.GetRating(ctx, userID, videoID)
		require.NoError(t, err)
		assert.Equal(t, domain.RatingLike, rating) // Still Like, not Dislike
	})
}
