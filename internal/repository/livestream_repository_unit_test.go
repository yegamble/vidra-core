package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupLiveStreamRepositoryTest(t *testing.T) (LiveStreamRepository, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	repo := NewLiveStreamRepository(sqlxDB)

	return repo, mock
}

func TestLiveStreamRepositoryUnit_Create(t *testing.T) {
	tests := []struct {
		name      string
		stream    *domain.LiveStream
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
	}{
		{
			name: "success",
			stream: &domain.LiveStream{
				ID:        uuid.New(),
				ChannelID: uuid.New(),
				UserID:    uuid.New(),
				Title:     "Test Stream",
				StreamKey: "test-key-123",
				Status:    domain.StreamStatusLive,
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"created_at", "updated_at"}).
					AddRow(time.Now(), time.Now())
				mock.ExpectQuery(`INSERT INTO live_streams`).
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "database error",
			stream: &domain.LiveStream{
				ID:        uuid.New(),
				ChannelID: uuid.New(),
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`INSERT INTO live_streams`).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupLiveStreamRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.Create(context.Background(), tt.stream)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotZero(t, tt.stream.CreatedAt)
				assert.NotZero(t, tt.stream.UpdatedAt)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLiveStreamRepositoryUnit_GetByID(t *testing.T) {
	streamID := uuid.New()
	channelID := uuid.New()
	userID := uuid.New()

	tests := []struct {
		name      string
		id        uuid.UUID
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			id:   streamID,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "channel_id", "user_id", "title", "description",
					"stream_key", "status", "privacy", "rtmp_url", "hls_playlist_url",
					"viewer_count", "peak_viewer_count", "started_at", "ended_at",
					"save_replay", "replay_video_id", "created_at", "updated_at",
				}).AddRow(
					streamID, channelID, userID, "Test Stream", "Description",
					"key-123", "live", "public", "rtmp://test", "http://hls",
					10, 20, time.Now(), nil, true, nil, time.Now(), time.Now(),
				)
				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams WHERE id = \$1`).
					WithArgs(streamID).
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "not found",
			id:   uuid.New(),
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams WHERE id = \$1`).
					WillReturnError(sql.ErrNoRows)
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrStreamNotFound)
			},
		},
		{
			name: "database error",
			id:   streamID,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams WHERE id = \$1`).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupLiveStreamRepositoryTest(t)
			tt.setupMock(mock)

			stream, err := repo.GetByID(context.Background(), tt.id)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, stream)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, stream)
				assert.Equal(t, tt.id, stream.ID)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLiveStreamRepositoryUnit_GetByStreamKey(t *testing.T) {
	streamID := uuid.New()
	channelID := uuid.New()
	userID := uuid.New()

	tests := []struct {
		name      string
		streamKey string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name:      "success",
			streamKey: "test-key-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "channel_id", "user_id", "title", "description",
					"stream_key", "status", "privacy", "rtmp_url", "hls_playlist_url",
					"viewer_count", "peak_viewer_count", "started_at", "ended_at",
					"save_replay", "replay_video_id", "created_at", "updated_at",
				}).AddRow(
					streamID, channelID, userID, "Test Stream", "Description",
					"test-key-123", "live", "public", "rtmp://test", "http://hls",
					10, 20, time.Now(), nil, true, nil, time.Now(), time.Now(),
				)
				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams WHERE stream_key = \$1`).
					WithArgs("test-key-123").
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name:      "not found",
			streamKey: "nonexistent-key",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams WHERE stream_key = \$1`).
					WithArgs("nonexistent-key").
					WillReturnError(sql.ErrNoRows)
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrStreamNotFound)
			},
		},
		{
			name:      "database error",
			streamKey: "test-key",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams WHERE stream_key = \$1`).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupLiveStreamRepositoryTest(t)
			tt.setupMock(mock)

			stream, err := repo.GetByStreamKey(context.Background(), tt.streamKey)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, stream)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, stream)
				assert.Equal(t, tt.streamKey, stream.StreamKey)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLiveStreamRepositoryUnit_GetByChannelID(t *testing.T) {
	channelID := uuid.New()

	tests := []struct {
		name      string
		channelID uuid.UUID
		limit     int
		offset    int
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		wantCount int
	}{
		{
			name:      "success - multiple streams",
			channelID: channelID,
			limit:     10,
			offset:    0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "channel_id", "user_id", "title", "description",
					"stream_key", "status", "privacy", "rtmp_url", "hls_playlist_url",
					"viewer_count", "peak_viewer_count", "started_at", "ended_at",
					"save_replay", "replay_video_id", "created_at", "updated_at",
				}).
					AddRow(uuid.New(), channelID, uuid.New(), "Stream 1", "Desc", "key1", "live", "public", "rtmp", "hls", 0, 0, time.Now(), nil, true, nil, time.Now(), time.Now()).
					AddRow(uuid.New(), channelID, uuid.New(), "Stream 2", "Desc", "key2", "ended", "public", "rtmp", "hls", 0, 0, time.Now(), time.Now(), true, nil, time.Now(), time.Now())

				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams\s+WHERE channel_id = \$1`).
					WithArgs(channelID, 10, 0).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 2,
		},
		{
			name:      "success - no streams",
			channelID: channelID,
			limit:     10,
			offset:    0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "channel_id", "user_id", "title", "description",
					"stream_key", "status", "privacy", "rtmp_url", "hls_playlist_url",
					"viewer_count", "peak_viewer_count", "started_at", "ended_at",
					"save_replay", "replay_video_id", "created_at", "updated_at",
				})

				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams\s+WHERE channel_id = \$1`).
					WithArgs(channelID, 10, 0).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 0,
		},
		{
			name:      "database error",
			channelID: channelID,
			limit:     10,
			offset:    0,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams\s+WHERE channel_id = \$1`).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupLiveStreamRepositoryTest(t)
			tt.setupMock(mock)

			streams, err := repo.GetByChannelID(context.Background(), tt.channelID, tt.limit, tt.offset)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, streams, tt.wantCount)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLiveStreamRepositoryUnit_GetByUserID(t *testing.T) {
	userID := uuid.New()

	tests := []struct {
		name      string
		userID    uuid.UUID
		limit     int
		offset    int
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		wantCount int
	}{
		{
			name:   "success - multiple streams",
			userID: userID,
			limit:  10,
			offset: 0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "channel_id", "user_id", "title", "description",
					"stream_key", "status", "privacy", "rtmp_url", "hls_playlist_url",
					"viewer_count", "peak_viewer_count", "started_at", "ended_at",
					"save_replay", "replay_video_id", "created_at", "updated_at",
				}).
					AddRow(uuid.New(), uuid.New(), userID, "Stream 1", "Desc", "key1", "live", "public", "rtmp", "hls", 0, 0, time.Now(), nil, true, nil, time.Now(), time.Now()).
					AddRow(uuid.New(), uuid.New(), userID, "Stream 2", "Desc", "key2", "ended", "public", "rtmp", "hls", 0, 0, time.Now(), time.Now(), true, nil, time.Now(), time.Now())

				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams\s+WHERE user_id = \$1`).
					WithArgs(userID, 10, 0).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 2,
		},
		{
			name:   "success - no streams",
			userID: userID,
			limit:  10,
			offset: 0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "channel_id", "user_id", "title", "description",
					"stream_key", "status", "privacy", "rtmp_url", "hls_playlist_url",
					"viewer_count", "peak_viewer_count", "started_at", "ended_at",
					"save_replay", "replay_video_id", "created_at", "updated_at",
				})

				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams\s+WHERE user_id = \$1`).
					WithArgs(userID, 10, 0).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 0,
		},
		{
			name:   "database error",
			userID: userID,
			limit:  10,
			offset: 0,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams\s+WHERE user_id = \$1`).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupLiveStreamRepositoryTest(t)
			tt.setupMock(mock)

			streams, err := repo.GetByUserID(context.Background(), tt.userID, tt.limit, tt.offset)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, streams, tt.wantCount)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLiveStreamRepositoryUnit_GetActiveStreams(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		offset    int
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		wantCount int
	}{
		{
			name:   "success - multiple active streams",
			limit:  10,
			offset: 0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "channel_id", "user_id", "title", "description",
					"stream_key", "status", "privacy", "rtmp_url", "hls_playlist_url",
					"viewer_count", "peak_viewer_count", "started_at", "ended_at",
					"save_replay", "replay_video_id", "created_at", "updated_at",
				}).
					AddRow(uuid.New(), uuid.New(), uuid.New(), "Stream 1", "Desc", "key1", "live", "public", "rtmp", "hls", 10, 15, time.Now(), nil, true, nil, time.Now(), time.Now()).
					AddRow(uuid.New(), uuid.New(), uuid.New(), "Stream 2", "Desc", "key2", "live", "public", "rtmp", "hls", 5, 8, time.Now(), nil, true, nil, time.Now(), time.Now())

				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams\s+WHERE status = \$1`).
					WithArgs("live", 10, 0).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 2,
		},
		{
			name:   "success - no active streams",
			limit:  10,
			offset: 0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "channel_id", "user_id", "title", "description",
					"stream_key", "status", "privacy", "rtmp_url", "hls_playlist_url",
					"viewer_count", "peak_viewer_count", "started_at", "ended_at",
					"save_replay", "replay_video_id", "created_at", "updated_at",
				})

				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams\s+WHERE status = \$1`).
					WithArgs("live", 10, 0).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 0,
		},
		{
			name:   "database error",
			limit:  10,
			offset: 0,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`(?s)SELECT .* FROM live_streams\s+WHERE status = \$1`).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupLiveStreamRepositoryTest(t)
			tt.setupMock(mock)

			streams, err := repo.GetActiveStreams(context.Background(), tt.limit, tt.offset)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, streams, tt.wantCount)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLiveStreamRepositoryUnit_CountByChannelID(t *testing.T) {
	channelID := uuid.New()

	tests := []struct {
		name      string
		channelID uuid.UUID
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		wantCount int
	}{
		{
			name:      "success - has streams",
			channelID: channelID,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"count"}).AddRow(5)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM live_streams WHERE channel_id = $1`)).
					WithArgs(channelID).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 5,
		},
		{
			name:      "success - no streams",
			channelID: channelID,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"count"}).AddRow(0)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM live_streams WHERE channel_id = $1`)).
					WithArgs(channelID).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 0,
		},
		{
			name:      "database error",
			channelID: channelID,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM live_streams WHERE channel_id = $1`)).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupLiveStreamRepositoryTest(t)
			tt.setupMock(mock)

			count, err := repo.CountByChannelID(context.Background(), tt.channelID)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, count)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLiveStreamRepositoryUnit_Update(t *testing.T) {
	tests := []struct {
		name      string
		stream    *domain.LiveStream
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
	}{
		{
			name: "success",
			stream: &domain.LiveStream{
				ID:          uuid.New(),
				Title:       "Updated Stream",
				Description: "Updated description",
				Status:      domain.StreamStatusLive,
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"updated_at"}).
					AddRow(time.Now())
				mock.ExpectQuery(`UPDATE live_streams SET`).
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "database error",
			stream: &domain.LiveStream{
				ID: uuid.New(),
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`UPDATE live_streams SET`).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupLiveStreamRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.Update(context.Background(), tt.stream)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotZero(t, tt.stream.UpdatedAt)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLiveStreamRepositoryUnit_UpdateStatus(t *testing.T) {
	streamID := uuid.New()

	tests := []struct {
		name      string
		id        uuid.UUID
		status    string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name:   "success",
			id:     streamID,
			status: domain.StreamStatusEnded,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE live_streams SET status = $1, updated_at = NOW() WHERE id = $2`)).
					WithArgs(domain.StreamStatusEnded, streamID).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name:   "not found",
			id:     uuid.New(),
			status: domain.StreamStatusEnded,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE live_streams SET status = $1, updated_at = NOW() WHERE id = $2`)).
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrStreamNotFound)
			},
		},
		{
			name:   "database error",
			id:     streamID,
			status: domain.StreamStatusEnded,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE live_streams SET status = $1, updated_at = NOW() WHERE id = $2`)).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupLiveStreamRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.UpdateStatus(context.Background(), tt.id, tt.status)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLiveStreamRepositoryUnit_UpdateViewerCount(t *testing.T) {
	streamID := uuid.New()

	tests := []struct {
		name      string
		id        uuid.UUID
		count     int
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name:  "success",
			id:    streamID,
			count: 25,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE live_streams SET viewer_count = $1, peak_viewer_count = GREATEST(peak_viewer_count, $1), updated_at = NOW() WHERE id = $2`)).
					WithArgs(25, streamID).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name:  "not found",
			id:    uuid.New(),
			count: 25,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE live_streams SET viewer_count = $1, peak_viewer_count = GREATEST(peak_viewer_count, $1), updated_at = NOW() WHERE id = $2`)).
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrStreamNotFound)
			},
		},
		{
			name:  "database error",
			id:    streamID,
			count: 25,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE live_streams SET viewer_count = $1, peak_viewer_count = GREATEST(peak_viewer_count, $1), updated_at = NOW() WHERE id = $2`)).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupLiveStreamRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.UpdateViewerCount(context.Background(), tt.id, tt.count)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLiveStreamRepositoryUnit_Delete(t *testing.T) {
	streamID := uuid.New()

	tests := []struct {
		name      string
		id        uuid.UUID
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			id:   streamID,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM live_streams WHERE id = $1`)).
					WithArgs(streamID).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "not found",
			id:   uuid.New(),
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM live_streams WHERE id = $1`)).
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrStreamNotFound)
			},
		},
		{
			name: "database error",
			id:   streamID,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM live_streams WHERE id = $1`)).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupLiveStreamRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.Delete(context.Background(), tt.id)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLiveStreamRepositoryUnit_EndStream(t *testing.T) {
	streamID := uuid.New()

	tests := []struct {
		name      string
		id        uuid.UUID
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
	}{
		{
			name: "success",
			id:   streamID,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`SELECT end_live_stream($1)`)).
					WithArgs(streamID).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "database error",
			id:   streamID,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`SELECT end_live_stream($1)`)).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupLiveStreamRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.EndStream(context.Background(), tt.id)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func setupViewerSessionRepositoryTest(t *testing.T) (ViewerSessionRepository, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	db := sqlx.NewDb(mockDB, "sqlmock")
	repo := NewViewerSessionRepository(db)
	return repo, mock
}

func TestViewerSessionRepositoryUnit_GetByID(t *testing.T) {
	sessionID := uuid.New()
	streamID := uuid.New()
	userID := uuid.New()

	tests := []struct {
		name      string
		id        uuid.UUID
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
	}{
		{
			name: "success",
			id:   sessionID,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "live_stream_id", "session_id", "user_id", "ip_address",
					"user_agent", "country_code", "joined_at", "left_at", "last_heartbeat_at",
				}).AddRow(
					sessionID, streamID, "session-abc", &userID, "192.168.1.1",
					"Mozilla/5.0", "US", time.Now(), nil, time.Now(),
				)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM viewer_sessions WHERE id = $1`)).
					WithArgs(sessionID).
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "not found",
			id:   sessionID,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM viewer_sessions WHERE id = $1`)).
					WithArgs(sessionID).
					WillReturnError(sql.ErrNoRows)
			},
			wantErr: true,
		},
		{
			name: "database error",
			id:   sessionID,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM viewer_sessions WHERE id = $1`)).
					WithArgs(sessionID).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupViewerSessionRepositoryTest(t)
			tt.setupMock(mock)

			session, err := repo.GetByID(context.Background(), tt.id)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, session)
			} else {
				require.NoError(t, err)
				require.NotNil(t, session)
				assert.Equal(t, tt.id, session.ID)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestViewerSessionRepositoryUnit_GetBySessionIDStr(t *testing.T) {
	sessionUUID := uuid.New()
	streamID := uuid.New()
	sessionIDStr := "session-abc-123"

	tests := []struct {
		name      string
		sessionID string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
	}{
		{
			name:      "success",
			sessionID: sessionIDStr,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "live_stream_id", "session_id", "user_id", "ip_address",
					"user_agent", "country_code", "joined_at", "left_at", "last_heartbeat_at",
				}).AddRow(
					sessionUUID, streamID, sessionIDStr, (*uuid.UUID)(nil), "10.0.0.1",
					"curl/7.0", "UK", time.Now(), nil, time.Now(),
				)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM viewer_sessions WHERE session_id = $1`)).
					WithArgs(sessionIDStr).
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name:      "not found",
			sessionID: "nonexistent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM viewer_sessions WHERE session_id = $1`)).
					WithArgs("nonexistent").
					WillReturnError(sql.ErrNoRows)
			},
			wantErr: true,
		},
		{
			name:      "database error",
			sessionID: sessionIDStr,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM viewer_sessions WHERE session_id = $1`)).
					WithArgs(sessionIDStr).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupViewerSessionRepositoryTest(t)
			tt.setupMock(mock)

			session, err := repo.GetBySessionID(context.Background(), tt.sessionID)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, session)
			} else {
				require.NoError(t, err)
				require.NotNil(t, session)
				assert.Equal(t, tt.sessionID, session.SessionID)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestViewerSessionRepositoryUnit_GetActiveByStream(t *testing.T) {
	streamID := uuid.New()
	session1 := uuid.New()
	session2 := uuid.New()

	tests := []struct {
		name      string
		streamID  uuid.UUID
		limit     int
		offset    int
		setupMock func(sqlmock.Sqlmock)
		wantCount int
		wantErr   bool
	}{
		{
			name:     "success - multiple sessions",
			streamID: streamID,
			limit:    10,
			offset:   0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "live_stream_id", "session_id", "user_id", "ip_address",
					"user_agent", "country_code", "joined_at", "left_at", "last_heartbeat_at",
				}).
					AddRow(session1, streamID, "s1", (*uuid.UUID)(nil), "10.0.0.1", "ua1", "US", time.Now(), nil, time.Now()).
					AddRow(session2, streamID, "s2", (*uuid.UUID)(nil), "10.0.0.2", "ua2", "CA", time.Now(), nil, time.Now())

				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM viewer_sessions WHERE live_stream_id = $1 AND left_at IS NULL ORDER BY joined_at DESC LIMIT $2 OFFSET $3`)).
					WithArgs(streamID, 10, 0).
					WillReturnRows(rows)
			},
			wantCount: 2,
			wantErr:   false,
		},
		{
			name:     "success - no active sessions",
			streamID: streamID,
			limit:    10,
			offset:   0,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM viewer_sessions WHERE live_stream_id = $1 AND left_at IS NULL ORDER BY joined_at DESC LIMIT $2 OFFSET $3`)).
					WithArgs(streamID, 10, 0).
					WillReturnRows(sqlmock.NewRows([]string{"id", "live_stream_id", "session_id", "user_id", "ip_address", "user_agent", "country_code", "joined_at", "left_at", "last_heartbeat_at"}))
			},
			wantCount: 0,
			wantErr:   false,
		},
		{
			name:     "database error",
			streamID: streamID,
			limit:    10,
			offset:   0,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM viewer_sessions WHERE live_stream_id = $1 AND left_at IS NULL ORDER BY joined_at DESC LIMIT $2 OFFSET $3`)).
					WithArgs(streamID, 10, 0).
					WillReturnError(sql.ErrConnDone)
			},
			wantCount: 0,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupViewerSessionRepositoryTest(t)
			tt.setupMock(mock)

			sessions, err := repo.GetActiveByStream(context.Background(), tt.streamID, tt.limit, tt.offset)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, sessions, tt.wantCount)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestStreamKeyRepositoryUnit_GetByChannelID(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()

	t.Run("success", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewStreamKeyRepository(db)

		expectedKey := &domain.StreamKey{
			ID:        uuid.New(),
			ChannelID: channelID,
			KeyHash:   "$2a$10$hashedkey",
			IsActive:  true,
			CreatedAt: time.Now(),
		}

		rows := sqlmock.NewRows([]string{"id", "channel_id", "key_hash", "last_used_at", "is_active", "created_at", "expires_at"}).
			AddRow(expectedKey.ID, expectedKey.ChannelID, expectedKey.KeyHash, nil, expectedKey.IsActive, expectedKey.CreatedAt, nil)

		mock.ExpectQuery("SELECT \\* FROM stream_keys WHERE channel_id").
			WithArgs(channelID).
			WillReturnRows(rows)

		result, err := repo.GetByChannelID(ctx, channelID)
		require.NoError(t, err)
		assert.Equal(t, expectedKey.ID, result.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewStreamKeyRepository(db)

		mock.ExpectQuery("SELECT \\* FROM stream_keys WHERE channel_id").
			WithArgs(channelID).
			WillReturnError(sql.ErrNoRows)

		result, err := repo.GetByChannelID(ctx, channelID)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestStreamKeyRepositoryUnit_DeactivateAllForChannel(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()

	t.Run("success", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewStreamKeyRepository(db)

		mock.ExpectExec("UPDATE stream_keys SET is_active").
			WithArgs(channelID).
			WillReturnResult(sqlmock.NewResult(0, 2))

		err = repo.DeactivateAllForChannel(ctx, channelID)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewStreamKeyRepository(db)

		mock.ExpectExec("UPDATE stream_keys SET is_active").
			WithArgs(channelID).
			WillReturnError(assert.AnError)

		err = repo.DeactivateAllForChannel(ctx, channelID)
		assert.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
