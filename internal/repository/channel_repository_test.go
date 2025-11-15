package repository

import (
	"context"
	"testing"

	"athena/internal/domain"
	"athena/internal/testutil"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelRepository_Create(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewChannelRepository(testDB.DB)

	ctx := context.Background()
	accountID := uuid.New()

	tests := []struct {
		name    string
		channel *domain.Channel
		wantErr error
	}{
		{
			name: "successful creation",
			channel: &domain.Channel{
				AccountID:   accountID,
				Handle:      "testchannel",
				DisplayName: "Test Channel",
				Description: "Test description",
				Support:     "Test support info",
			},
			wantErr: nil,
		},
		{
			name: "duplicate handle",
			channel: &domain.Channel{
				AccountID:   uuid.New(),
				Handle:      "testchannel",
				DisplayName: "Another Channel",
			},
			wantErr: domain.ErrDuplicateEntry,
		},
		{
			name: "with minimal fields",
			channel: &domain.Channel{
				AccountID: uuid.New(),
				Handle:    "minimal",
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.Create(ctx, tt.channel)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.NotEqual(t, uuid.Nil, tt.channel.ID)
				assert.NotZero(t, tt.channel.CreatedAt)
				assert.NotZero(t, tt.channel.UpdatedAt)
				assert.True(t, tt.channel.IsLocal)

				// Verify it was created
				retrieved, err := repo.GetByID(ctx, tt.channel.ID)
				require.NoError(t, err)
				assert.Equal(t, tt.channel.Handle, retrieved.Handle)
				assert.Equal(t, tt.channel.DisplayName, retrieved.DisplayName)
				assert.Equal(t, tt.channel.AccountID, retrieved.AccountID)
			}
		})
	}
}

func TestChannelRepository_GetByID(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewChannelRepository(testDB.DB)

	ctx := context.Background()
	accountID := uuid.New()

	channel := createTestChannel(t, repo, ctx, accountID, "test_channel")

	tests := []struct {
		name    string
		id      uuid.UUID
		wantErr error
	}{
		{
			name:    "existing channel",
			id:      channel.ID,
			wantErr: nil,
		},
		{
			name:    "non-existent channel",
			id:      uuid.New(),
			wantErr: domain.ErrNotFound,
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
				assert.Equal(t, channel.Handle, result.Handle)
			}
		})
	}
}

func TestChannelRepository_GetByHandle(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewChannelRepository(testDB.DB)

	ctx := context.Background()
	accountID := uuid.New()

	channel := createTestChannel(t, repo, ctx, accountID, "unique_handle")

	tests := []struct {
		name    string
		handle  string
		wantErr error
	}{
		{
			name:    "existing handle",
			handle:  "unique_handle",
			wantErr: nil,
		},
		{
			name:    "non-existent handle",
			handle:  "nonexistent_handle",
			wantErr: domain.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := repo.GetByHandle(ctx, tt.handle)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, channel.ID, result.ID)
				assert.Equal(t, tt.handle, result.Handle)
			}
		})
	}
}

func TestChannelRepository_List(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewChannelRepository(testDB.DB)

	ctx := context.Background()

	account1 := uuid.New()
	account2 := uuid.New()

	// Create multiple channels
	ch1 := createTestChannel(t, repo, ctx, account1, "channel1")
	ch1.DisplayName = "Alpha Channel"
	createTestChannel(t, repo, ctx, account1, "channel2")
	createTestChannel(t, repo, ctx, account2, "channel3")

	tests := []struct {
		name      string
		params    domain.ChannelListParams
		wantCount int
		wantTotal int
	}{
		{
			name: "list all channels",
			params: domain.ChannelListParams{
				Page:     1,
				PageSize: 10,
			},
			wantCount: 3,
			wantTotal: 3,
		},
		{
			name: "filter by account",
			params: domain.ChannelListParams{
				AccountID: &account1,
				Page:      1,
				PageSize:  10,
			},
			wantCount: 2,
			wantTotal: 2,
		},
		{
			name: "filter by is_local",
			params: domain.ChannelListParams{
				IsLocal:  boolPtr(true),
				Page:     1,
				PageSize: 10,
			},
			wantCount: 3,
			wantTotal: 3,
		},
		{
			name: "search by handle",
			params: domain.ChannelListParams{
				Search:   "channel1",
				Page:     1,
				PageSize: 10,
			},
			wantCount: 1,
			wantTotal: 1,
		},
		{
			name: "search by display name",
			params: domain.ChannelListParams{
				Search:   "Alpha",
				Page:     1,
				PageSize: 10,
			},
			wantCount: 1,
			wantTotal: 1,
		},
		{
			name: "pagination - first page",
			params: domain.ChannelListParams{
				Page:     1,
				PageSize: 2,
			},
			wantCount: 2,
			wantTotal: 3,
		},
		{
			name: "pagination - second page",
			params: domain.ChannelListParams{
				Page:     2,
				PageSize: 2,
			},
			wantCount: 1,
			wantTotal: 3,
		},
		{
			name: "sort by name ascending",
			params: domain.ChannelListParams{
				Sort:     "name",
				Page:     1,
				PageSize: 10,
			},
			wantCount: 3,
			wantTotal: 3,
		},
		{
			name: "sort by created date descending",
			params: domain.ChannelListParams{
				Sort:     "-createdAt",
				Page:     1,
				PageSize: 10,
			},
			wantCount: 3,
			wantTotal: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := repo.List(ctx, tt.params)
			require.NoError(t, err)
			assert.Len(t, result.Data, tt.wantCount)
			assert.Equal(t, tt.wantTotal, result.Total)

			// Verify page metadata
			assert.Equal(t, tt.params.Page, result.Page)
			if tt.params.PageSize > 0 {
				assert.Equal(t, tt.params.PageSize, result.PageSize)
			}
		})
	}
}

func TestChannelRepository_List_Defaults(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewChannelRepository(testDB.DB)

	ctx := context.Background()

	// Create a channel
	createTestChannel(t, repo, ctx, uuid.New(), "test")

	t.Run("default pagination", func(t *testing.T) {
		result, err := repo.List(ctx, domain.ChannelListParams{})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Page)     // Default page
		assert.Equal(t, 20, result.PageSize) // Default page size
	})

	t.Run("max page size enforced", func(t *testing.T) {
		result, err := repo.List(ctx, domain.ChannelListParams{
			PageSize: 200, // Try to request more than max
		})
		require.NoError(t, err)
		assert.Equal(t, 100, result.PageSize) // Should be capped at 100
	})
}

func TestChannelRepository_Update(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewChannelRepository(testDB.DB)

	ctx := context.Background()
	accountID := uuid.New()

	channel := createTestChannel(t, repo, ctx, accountID, "update_test")

	tests := []struct {
		name    string
		id      uuid.UUID
		updates domain.ChannelUpdateRequest
		wantErr error
		check   func(t *testing.T, updated *domain.Channel)
	}{
		{
			name: "update display name",
			id:   channel.ID,
			updates: domain.ChannelUpdateRequest{
				DisplayName: stringPtr("Updated Name"),
			},
			wantErr: nil,
			check: func(t *testing.T, updated *domain.Channel) {
				assert.Equal(t, "Updated Name", updated.DisplayName)
				assert.Equal(t, channel.Description, updated.Description) // Others unchanged
			},
		},
		{
			name: "update description",
			id:   channel.ID,
			updates: domain.ChannelUpdateRequest{
				Description: stringPtr("Updated description"),
			},
			wantErr: nil,
			check: func(t *testing.T, updated *domain.Channel) {
				assert.Equal(t, "Updated description", updated.Description)
			},
		},
		{
			name: "update support",
			id:   channel.ID,
			updates: domain.ChannelUpdateRequest{
				Support: stringPtr("Updated support"),
			},
			wantErr: nil,
			check: func(t *testing.T, updated *domain.Channel) {
				assert.Equal(t, "Updated support", updated.Support)
			},
		},
		{
			name: "update multiple fields",
			id:   channel.ID,
			updates: domain.ChannelUpdateRequest{
				DisplayName: stringPtr("Multi Update"),
				Description: stringPtr("Multi Description"),
				Support:     stringPtr("Multi Support"),
			},
			wantErr: nil,
			check: func(t *testing.T, updated *domain.Channel) {
				assert.Equal(t, "Multi Update", updated.DisplayName)
				assert.Equal(t, "Multi Description", updated.Description)
				assert.Equal(t, "Multi Support", updated.Support)
			},
		},
		{
			name:    "empty update",
			id:      channel.ID,
			updates: domain.ChannelUpdateRequest{},
			wantErr: domain.ErrInvalidInput,
		},
		{
			name: "non-existent channel",
			id:   uuid.New(),
			updates: domain.ChannelUpdateRequest{
				DisplayName: stringPtr("Non-existent"),
			},
			wantErr: domain.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updated, err := repo.Update(ctx, tt.id, tt.updates)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, updated)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, updated)
				if tt.check != nil {
					tt.check(t, updated)
				}

				// Verify updated_at changed
				assert.True(t, updated.UpdatedAt.After(channel.CreatedAt) ||
					updated.UpdatedAt.Equal(channel.CreatedAt))
			}
		})
	}
}

func TestChannelRepository_Delete(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewChannelRepository(testDB.DB)

	ctx := context.Background()
	accountID := uuid.New()

	channel := createTestChannel(t, repo, ctx, accountID, "delete_test")

	tests := []struct {
		name    string
		id      uuid.UUID
		wantErr error
	}{
		{
			name:    "delete existing channel",
			id:      channel.ID,
			wantErr: nil,
		},
		{
			name:    "delete already deleted channel",
			id:      channel.ID,
			wantErr: domain.ErrNotFound,
		},
		{
			name:    "delete non-existent channel",
			id:      uuid.New(),
			wantErr: domain.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.Delete(ctx, tt.id)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)

				// Verify it was deleted
				_, err := repo.GetByID(ctx, tt.id)
				assert.ErrorIs(t, err, domain.ErrNotFound)
			}
		})
	}
}

func TestChannelRepository_Concurrency(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewChannelRepository(testDB.DB)

	ctx := context.Background()
	accountID := uuid.New()

	t.Run("concurrent creates with different handles", func(t *testing.T) {
		done := make(chan bool, 5)
		errors := make(chan error, 5)

		for i := 0; i < 5; i++ {
			go func(idx int) {
				channel := &domain.Channel{
					AccountID:   accountID,
					Handle:      uuid.New().String(), // Unique handle
					DisplayName: "Concurrent Channel",
				}
				err := repo.Create(ctx, channel)
				if err != nil {
					errors <- err
				}
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < 5; i++ {
			<-done
		}
		close(errors)

		// Check for errors
		for err := range errors {
			t.Errorf("Concurrent create failed: %v", err)
		}
	})

	t.Run("concurrent duplicate handle creates", func(t *testing.T) {
		duplicateHandle := "duplicate_" + uuid.New().String()
		done := make(chan bool, 3)
		successCount := 0
		duplicateCount := 0

		for i := 0; i < 3; i++ {
			go func() {
				channel := &domain.Channel{
					AccountID:   accountID,
					Handle:      duplicateHandle,
					DisplayName: "Duplicate Test",
				}
				err := repo.Create(ctx, channel)
				if err == nil {
					successCount++
				} else if err == domain.ErrDuplicateEntry {
					duplicateCount++
				}
				done <- true
			}()
		}

		for i := 0; i < 3; i++ {
			<-done
		}

		// Exactly one should succeed, others should get duplicate error
		assert.Equal(t, 1, successCount, "Expected exactly one successful create")
		assert.Equal(t, 2, duplicateCount, "Expected two duplicate errors")
	})
}

// Helper functions

func stringPtr(s string) *string {
	return &s
}
