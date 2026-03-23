package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func setupLiveStreamMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	return sqlxDB, mock
}

func TestLiveStreamRepository_Create(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewLiveStreamRepository(db)
	ctx := context.Background()

	stream := &domain.LiveStream{
		ID:          uuid.New(),
		ChannelID:   uuid.New(),
		UserID:      uuid.New(),
		Title:       "Test Stream",
		Description: "A test stream",
		StreamKey:   "test-key-123",
		Status:      domain.StreamStatusWaiting,
		Privacy:     domain.StreamPrivacyPublic,
		SaveReplay:  true,
	}

	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO live_streams`)).
		WithArgs(
			stream.ID, stream.ChannelID, stream.UserID, stream.Title,
			stream.Description, stream.StreamKey, stream.Status, stream.Privacy,
			sqlmock.AnyArg(), sqlmock.AnyArg(), stream.SaveReplay,
		).
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).
			AddRow(now, now))

	err := repo.Create(ctx, stream)
	require.NoError(t, err)
	assert.Equal(t, now, stream.CreatedAt)
	assert.Equal(t, now, stream.UpdatedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveStreamRepository_GetByID(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewLiveStreamRepository(db)
	ctx := context.Background()

	streamID := uuid.New()
	channelID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	t.Run("Stream found", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{
			"id", "channel_id", "user_id", "title", "description", "stream_key",
			"status", "privacy", "rtmp_url", "hls_playlist_url", "viewer_count",
			"peak_viewer_count", "started_at", "ended_at", "save_replay",
			"replay_video_id", "created_at", "updated_at",
		}).AddRow(
			streamID, channelID, userID, "Test Stream", "Description", "key123",
			domain.StreamStatusLive, domain.StreamPrivacyPublic, "rtmp://test", "https://test.m3u8",
			10, 15, now, nil, true, nil, now, now,
		)

		mock.ExpectQuery(`(?s)SELECT .* FROM live_streams WHERE id = \$1`).
			WithArgs(streamID).
			WillReturnRows(rows)

		stream, err := repo.GetByID(ctx, streamID)
		require.NoError(t, err)
		assert.Equal(t, streamID, stream.ID)
		assert.Equal(t, "Test Stream", stream.Title)
		assert.Equal(t, domain.StreamStatusLive, stream.Status)
		assert.Equal(t, 10, stream.ViewerCount)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Stream not found", func(t *testing.T) {
		mock.ExpectQuery(`(?s)SELECT .* FROM live_streams WHERE id = \$1`).
			WithArgs(streamID).
			WillReturnError(sql.ErrNoRows)

		stream, err := repo.GetByID(ctx, streamID)
		assert.ErrorIs(t, err, domain.ErrStreamNotFound)
		assert.Nil(t, stream)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestLiveStreamRepository_GetByStreamKey(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewLiveStreamRepository(db)
	ctx := context.Background()

	streamKey := "test-stream-key"
	streamID := uuid.New()
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "channel_id", "user_id", "title", "description", "stream_key",
		"status", "privacy", "rtmp_url", "hls_playlist_url", "viewer_count",
		"peak_viewer_count", "started_at", "ended_at", "save_replay",
		"replay_video_id", "created_at", "updated_at",
	}).AddRow(
		streamID, uuid.New(), uuid.New(), "Test", "Desc", streamKey,
		domain.StreamStatusWaiting, domain.StreamPrivacyPublic, "", "",
		0, 0, nil, nil, true, nil, now, now,
	)

	mock.ExpectQuery(`(?s)SELECT .* FROM live_streams WHERE stream_key = \$1`).
		WithArgs(streamKey).
		WillReturnRows(rows)

	stream, err := repo.GetByStreamKey(ctx, streamKey)
	require.NoError(t, err)
	assert.Equal(t, streamKey, stream.StreamKey)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveStreamRepository_GetActiveStreams(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewLiveStreamRepository(db)
	ctx := context.Background()

	now := time.Now()
	rows := sqlmock.NewRows([]string{
		"id", "channel_id", "user_id", "title", "description", "stream_key",
		"status", "privacy", "rtmp_url", "hls_playlist_url", "viewer_count",
		"peak_viewer_count", "started_at", "ended_at", "save_replay",
		"replay_video_id", "created_at", "updated_at",
	}).
		AddRow(uuid.New(), uuid.New(), uuid.New(), "Stream 1", "", "key1",
			domain.StreamStatusLive, domain.StreamPrivacyPublic, "", "", 5, 10, now, nil, true, nil, now, now).
		AddRow(uuid.New(), uuid.New(), uuid.New(), "Stream 2", "", "key2",
			domain.StreamStatusLive, domain.StreamPrivacyPublic, "", "", 3, 8, now, nil, true, nil, now, now)

	mock.ExpectQuery(`(?s)SELECT .* FROM live_streams WHERE status = \$1`).
		WithArgs(domain.StreamStatusLive, 10, 0).
		WillReturnRows(rows)

	streams, err := repo.GetActiveStreams(ctx, 10, 0)
	require.NoError(t, err)
	assert.Len(t, streams, 2)
	assert.Equal(t, "Stream 1", streams[0].Title)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveStreamRepository_Update(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewLiveStreamRepository(db)
	ctx := context.Background()

	stream := &domain.LiveStream{
		ID:    uuid.New(),
		Title: "Updated Title",
	}

	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`UPDATE live_streams SET`)).
		WithArgs(
			stream.Title, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			stream.ID,
		).
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(now))

	err := repo.Update(ctx, stream)
	require.NoError(t, err)
	assert.Equal(t, now, stream.UpdatedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveStreamRepository_UpdateStatus(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewLiveStreamRepository(db)
	ctx := context.Background()

	streamID := uuid.New()
	newStatus := domain.StreamStatusLive

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE live_streams SET status = $1`)).
		WithArgs(newStatus, streamID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpdateStatus(ctx, streamID, newStatus)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveStreamRepository_UpdateViewerCount(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewLiveStreamRepository(db)
	ctx := context.Background()

	streamID := uuid.New()
	count := 25

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE live_streams SET viewer_count = $1`)).
		WithArgs(count, streamID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpdateViewerCount(ctx, streamID, count)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveStreamRepository_EndStream(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewLiveStreamRepository(db)
	ctx := context.Background()

	streamID := uuid.New()

	mock.ExpectExec(regexp.QuoteMeta(`SELECT end_live_stream($1)`)).
		WithArgs(streamID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.EndStream(ctx, streamID)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveStreamRepository_Delete(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewLiveStreamRepository(db)
	ctx := context.Background()

	streamID := uuid.New()

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM live_streams WHERE id = $1`)).
		WithArgs(streamID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.Delete(ctx, streamID)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Stream Key Repository Tests

func TestStreamKeyRepository_Create(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewStreamKeyRepository(db)
	ctx := context.Background()

	channelID := uuid.New()
	keyPlaintext := "my-secret-key"
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO stream_keys`)).
		WithArgs(sqlmock.AnyArg(), channelID, sqlmock.AnyArg(), true, (*time.Time)(nil)).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now))

	key, err := repo.Create(ctx, channelID, keyPlaintext, nil)
	require.NoError(t, err)
	assert.NotNil(t, key)
	assert.Equal(t, channelID, key.ChannelID)
	assert.True(t, key.IsActive)

	// Verify the key was hashed
	err = bcrypt.CompareHashAndPassword([]byte(key.KeyHash), []byte(keyPlaintext))
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStreamKeyRepository_GetActiveByChannelID(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewStreamKeyRepository(db)
	ctx := context.Background()

	channelID := uuid.New()
	keyID := uuid.New()
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "channel_id", "key_hash", "last_used_at", "is_active", "created_at", "expires_at",
	}).AddRow(keyID, channelID, "hash123", nil, true, now, nil)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM stream_keys WHERE channel_id = $1 AND is_active = true`)).
		WithArgs(channelID).
		WillReturnRows(rows)

	key, err := repo.GetActiveByChannelID(ctx, channelID)
	require.NoError(t, err)
	assert.Equal(t, keyID, key.ID)
	assert.True(t, key.IsActive)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStreamKeyRepository_ValidateKey(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewStreamKeyRepository(db)
	ctx := context.Background()

	channelID := uuid.New()
	keyPlaintext := "correct-key"
	keyHash, _ := bcrypt.GenerateFromPassword([]byte(keyPlaintext), bcrypt.DefaultCost)
	now := time.Now()

	t.Run("Valid key", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{
			"id", "channel_id", "key_hash", "last_used_at", "is_active", "created_at", "expires_at",
		}).AddRow(uuid.New(), channelID, string(keyHash), nil, true, now, nil)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM stream_keys WHERE channel_id = $1 AND is_active = true`)).
			WithArgs(channelID).
			WillReturnRows(rows)

		key, err := repo.ValidateKey(ctx, channelID, keyPlaintext)
		require.NoError(t, err)
		assert.NotNil(t, key)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Invalid key", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{
			"id", "channel_id", "key_hash", "last_used_at", "is_active", "created_at", "expires_at",
		}).AddRow(uuid.New(), channelID, string(keyHash), nil, true, now, nil)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM stream_keys WHERE channel_id = $1 AND is_active = true`)).
			WithArgs(channelID).
			WillReturnRows(rows)

		key, err := repo.ValidateKey(ctx, channelID, "wrong-key")
		assert.ErrorIs(t, err, domain.ErrStreamKeyInvalid)
		assert.Nil(t, key)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Expired key", func(t *testing.T) {
		pastTime := time.Now().Add(-time.Hour)
		rows := sqlmock.NewRows([]string{
			"id", "channel_id", "key_hash", "last_used_at", "is_active", "created_at", "expires_at",
		}).AddRow(uuid.New(), channelID, string(keyHash), nil, true, now, pastTime)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM stream_keys WHERE channel_id = $1 AND is_active = true`)).
			WithArgs(channelID).
			WillReturnRows(rows)

		key, err := repo.ValidateKey(ctx, channelID, keyPlaintext)
		assert.ErrorIs(t, err, domain.ErrStreamKeyExpired)
		assert.Nil(t, key)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestStreamKeyRepository_MarkUsed(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewStreamKeyRepository(db)
	ctx := context.Background()

	keyID := uuid.New()

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE stream_keys SET last_used_at = NOW()`)).
		WithArgs(keyID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.MarkUsed(ctx, keyID)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStreamKeyRepository_Deactivate(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewStreamKeyRepository(db)
	ctx := context.Background()

	keyID := uuid.New()

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE stream_keys SET is_active = false`)).
		WithArgs(keyID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.Deactivate(ctx, keyID)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStreamKeyRepository_DeleteExpired(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewStreamKeyRepository(db)
	ctx := context.Background()

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM stream_keys WHERE expires_at IS NOT NULL AND expires_at < NOW()`)).
		WillReturnResult(sqlmock.NewResult(0, 5))

	count, err := repo.DeleteExpired(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Viewer Session Repository Tests

func TestViewerSessionRepository_Create(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewViewerSessionRepository(db)
	ctx := context.Background()

	session := &domain.ViewerSession{
		ID:           uuid.New(),
		LiveStreamID: uuid.New(),
		SessionID:    "session-123",
		IPAddress:    "192.168.1.1",
		UserAgent:    "Mozilla/5.0",
		CountryCode:  "US",
	}

	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO viewer_sessions`)).
		WithArgs(
			session.ID, session.LiveStreamID, session.SessionID, (*uuid.UUID)(nil),
			session.IPAddress, session.UserAgent, session.CountryCode,
		).
		WillReturnRows(sqlmock.NewRows([]string{"joined_at", "last_heartbeat_at"}).
			AddRow(now, now))

	err := repo.Create(ctx, session)
	require.NoError(t, err)
	assert.Equal(t, now, session.JoinedAt)
	assert.Equal(t, now, session.LastHeartbeatAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestViewerSessionRepository_GetBySessionID(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewViewerSessionRepository(db)
	ctx := context.Background()

	sessionID := "session-123"
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "live_stream_id", "session_id", "user_id", "ip_address",
		"user_agent", "country_code", "joined_at", "left_at", "last_heartbeat_at",
	}).AddRow(
		uuid.New(), uuid.New(), sessionID, nil, "192.168.1.1",
		"Mozilla/5.0", "US", now, nil, now,
	)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM viewer_sessions WHERE session_id = $1`)).
		WithArgs(sessionID).
		WillReturnRows(rows)

	session, err := repo.GetBySessionID(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, sessionID, session.SessionID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestViewerSessionRepository_CountActiveViewers(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewViewerSessionRepository(db)
	ctx := context.Background()

	streamID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT get_live_viewer_count($1)`)).
		WithArgs(streamID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))

	count, err := repo.CountActiveViewers(ctx, streamID)
	require.NoError(t, err)
	assert.Equal(t, 42, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestViewerSessionRepository_UpdateHeartbeat(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewViewerSessionRepository(db)
	ctx := context.Background()

	sessionID := "session-123"

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE viewer_sessions SET last_heartbeat_at = NOW()`)).
		WithArgs(sessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpdateHeartbeat(ctx, sessionID)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestViewerSessionRepository_EndSession(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewViewerSessionRepository(db)
	ctx := context.Background()

	sessionID := "session-123"

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE viewer_sessions SET left_at = NOW()`)).
		WithArgs(sessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.EndSession(ctx, sessionID)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestViewerSessionRepository_CleanupStale(t *testing.T) {
	db, mock := setupLiveStreamMockDB(t)
	defer db.Close()

	repo := NewViewerSessionRepository(db)
	ctx := context.Background()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT cleanup_stale_viewer_sessions()`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10))

	count, err := repo.CleanupStale(ctx)
	require.NoError(t, err)
	assert.Equal(t, 10, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}
