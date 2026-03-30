package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

// ScheduleStreamParams groups the scheduling fields for ScheduleStream,
// replacing the previous 4-parameter flat signature.
type ScheduleStreamParams struct {
	ScheduledStart     *time.Time
	ScheduledEnd       *time.Time
	WaitingRoomEnabled bool
	WaitingRoomMessage string
}

type LiveStreamRepository interface {
	Create(ctx context.Context, stream *domain.LiveStream) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error)
	GetByStreamKey(ctx context.Context, streamKey string) (*domain.LiveStream, error)
	GetByChannelID(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error)
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error)
	GetActiveStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error)
	CountByChannelID(ctx context.Context, channelID uuid.UUID) (int, error)
	Update(ctx context.Context, stream *domain.LiveStream) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) error
	UpdateViewerCount(ctx context.Context, id uuid.UUID, count int) error
	Delete(ctx context.Context, id uuid.UUID) error
	EndStream(ctx context.Context, id uuid.UUID) error
	GetChannelByStreamID(ctx context.Context, streamID uuid.UUID) (*domain.Channel, error)
	UpdateWaitingRoom(ctx context.Context, streamID uuid.UUID, enabled bool, message string) error
	ScheduleStream(ctx context.Context, streamID uuid.UUID, params ScheduleStreamParams) error
	CancelSchedule(ctx context.Context, streamID uuid.UUID) error
	GetScheduledStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error)
	GetUpcomingStreams(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.LiveStream, error)
}

type liveStreamRepository struct {
	db *sqlx.DB
}

func NewLiveStreamRepository(db *sqlx.DB) LiveStreamRepository {
	return &liveStreamRepository{db: db}
}

func liveStreamSelectColumns(prefix string) string {
	if prefix != "" {
		prefix += "."
	}

	return fmt.Sprintf(`
		%sid,
		%schannel_id,
		%suser_id,
		%stitle,
		COALESCE(%sdescription, '') AS description,
		%sstream_key,
		%sstatus,
		%sprivacy,
		COALESCE(%srtmp_url, '') AS rtmp_url,
		COALESCE(%shls_playlist_url, '') AS hls_playlist_url,
		%sviewer_count,
		%speak_viewer_count,
		%sstarted_at,
		%sended_at,
		%ssave_replay,
		%sreplay_video_id,
		%sscheduled_start,
		%sscheduled_end,
		%swaiting_room_enabled,
		COALESCE(%swaiting_room_message, '') AS waiting_room_message,
		%sreminder_sent,
		%schat_enabled,
		%screated_at,
		%supdated_at
	`,
		prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix,
		prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix,
		prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix,
	)
}

func (r *liveStreamRepository) Create(ctx context.Context, stream *domain.LiveStream) error {
	query := `
		INSERT INTO live_streams (
			id, channel_id, user_id, title, description, stream_key,
			status, privacy, rtmp_url, hls_playlist_url, save_replay
		) VALUES (
			:id, :channel_id, :user_id, :title, :description, :stream_key,
			:status, :privacy, :rtmp_url, :hls_playlist_url, :save_replay
		)
		RETURNING created_at, updated_at
	`

	rows, err := r.db.NamedQueryContext(ctx, query, stream)
	if err != nil {
		return fmt.Errorf("failed to create live stream: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	if rows.Next() {
		if err := rows.Scan(&stream.CreatedAt, &stream.UpdatedAt); err != nil {
			return fmt.Errorf("failed to scan created stream: %w", err)
		}
	}

	return nil
}

func (r *liveStreamRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error) {
	var stream domain.LiveStream
	query := fmt.Sprintf(`SELECT %s FROM live_streams WHERE id = $1`, liveStreamSelectColumns(""))

	if err := r.db.GetContext(ctx, &stream, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrStreamNotFound
		}
		return nil, fmt.Errorf("failed to get live stream: %w", err)
	}

	return &stream, nil
}

func (r *liveStreamRepository) GetByStreamKey(ctx context.Context, streamKey string) (*domain.LiveStream, error) {
	var stream domain.LiveStream
	query := fmt.Sprintf(`SELECT %s FROM live_streams WHERE stream_key = $1`, liveStreamSelectColumns(""))

	if err := r.db.GetContext(ctx, &stream, query, streamKey); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrStreamNotFound
		}
		return nil, fmt.Errorf("failed to get live stream by key: %w", err)
	}

	return &stream, nil
}

func (r *liveStreamRepository) GetByChannelID(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	var streams []*domain.LiveStream
	query := fmt.Sprintf(`
		SELECT %s FROM live_streams
		WHERE channel_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, liveStreamSelectColumns(""))

	if err := r.db.SelectContext(ctx, &streams, query, channelID, limit, offset); err != nil {
		return nil, fmt.Errorf("failed to get streams by channel: %w", err)
	}

	return streams, nil
}

func (r *liveStreamRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	var streams []*domain.LiveStream
	query := fmt.Sprintf(`
		SELECT %s FROM live_streams
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, liveStreamSelectColumns(""))

	if err := r.db.SelectContext(ctx, &streams, query, userID, limit, offset); err != nil {
		return nil, fmt.Errorf("failed to get streams by user: %w", err)
	}

	return streams, nil
}

func (r *liveStreamRepository) GetActiveStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error) {
	var streams []*domain.LiveStream
	query := fmt.Sprintf(`
		SELECT %s FROM live_streams
		WHERE status = $1
		ORDER BY started_at DESC
		LIMIT $2 OFFSET $3
	`, liveStreamSelectColumns(""))

	if err := r.db.SelectContext(ctx, &streams, query, domain.StreamStatusLive, limit, offset); err != nil {
		return nil, fmt.Errorf("failed to get active streams: %w", err)
	}

	return streams, nil
}

func (r *liveStreamRepository) CountByChannelID(ctx context.Context, channelID uuid.UUID) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM live_streams WHERE channel_id = $1`

	if err := r.db.GetContext(ctx, &count, query, channelID); err != nil {
		return 0, fmt.Errorf("failed to count streams by channel: %w", err)
	}

	return count, nil
}

func (r *liveStreamRepository) Update(ctx context.Context, stream *domain.LiveStream) error {
	query := `
		UPDATE live_streams SET
			title = :title,
			description = :description,
			privacy = :privacy,
			status = :status,
			rtmp_url = :rtmp_url,
			hls_playlist_url = :hls_playlist_url,
			viewer_count = :viewer_count,
			peak_viewer_count = :peak_viewer_count,
			started_at = :started_at,
			ended_at = :ended_at,
			save_replay = :save_replay,
			replay_video_id = :replay_video_id
		WHERE id = :id
		RETURNING updated_at
	`

	rows, err := r.db.NamedQueryContext(ctx, query, stream)
	if err != nil {
		return fmt.Errorf("failed to update live stream: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	if rows.Next() {
		if err := rows.Scan(&stream.UpdatedAt); err != nil {
			return fmt.Errorf("failed to scan updated stream: %w", err)
		}
	}

	return nil
}

func (r *liveStreamRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	query := `
		UPDATE live_streams SET status = $1, updated_at = NOW()
		WHERE id = $2
	`

	result, err := r.db.ExecContext(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("failed to update stream status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return domain.ErrStreamNotFound
	}

	return nil
}

func (r *liveStreamRepository) UpdateViewerCount(ctx context.Context, id uuid.UUID, count int) error {
	query := `
		UPDATE live_streams SET
			viewer_count = $1,
			peak_viewer_count = GREATEST(peak_viewer_count, $1),
			updated_at = NOW()
		WHERE id = $2
	`

	result, err := r.db.ExecContext(ctx, query, count, id)
	if err != nil {
		return fmt.Errorf("failed to update viewer count: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return domain.ErrStreamNotFound
	}

	return nil
}

func (r *liveStreamRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM live_streams WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete live stream: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return domain.ErrStreamNotFound
	}

	return nil
}

func (r *liveStreamRepository) EndStream(ctx context.Context, id uuid.UUID) error {
	query := `SELECT end_live_stream($1)`

	if _, err := r.db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("failed to end live stream: %w", err)
	}

	return nil
}

func (r *liveStreamRepository) GetChannelByStreamID(ctx context.Context, streamID uuid.UUID) (*domain.Channel, error) {
	query := `
		SELECT c.* FROM channels c
		JOIN live_streams ls ON ls.channel_id = c.id
		WHERE ls.id = $1
	`
	var channel domain.Channel
	if err := r.db.GetContext(ctx, &channel, query, streamID); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get channel by stream ID: %w", err)
	}
	return &channel, nil
}

func (r *liveStreamRepository) UpdateWaitingRoom(ctx context.Context, streamID uuid.UUID, enabled bool, message string) error {
	query := `
		UPDATE live_streams
		SET waiting_room_enabled = $2, waiting_room_message = $3
		WHERE id = $1
	`
	result, err := r.db.ExecContext(ctx, query, streamID, enabled, message)
	if err != nil {
		return fmt.Errorf("failed to update waiting room: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return domain.ErrStreamNotFound
	}
	return nil
}

func (r *liveStreamRepository) ScheduleStream(ctx context.Context, streamID uuid.UUID, params ScheduleStreamParams) error {
	query := `
		UPDATE live_streams
		SET scheduled_start = $2, scheduled_end = $3,
		    waiting_room_enabled = $4, waiting_room_message = $5,
		    status = 'scheduled'
		WHERE id = $1
	`
	result, err := r.db.ExecContext(ctx, query, streamID, params.ScheduledStart, params.ScheduledEnd, params.WaitingRoomEnabled, params.WaitingRoomMessage)
	if err != nil {
		return fmt.Errorf("failed to schedule stream: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return domain.ErrStreamNotFound
	}
	return nil
}

func (r *liveStreamRepository) CancelSchedule(ctx context.Context, streamID uuid.UUID) error {
	query := `
		UPDATE live_streams
		SET scheduled_start = NULL, scheduled_end = NULL,
		    waiting_room_enabled = false, status = 'waiting'
		WHERE id = $1
	`
	result, err := r.db.ExecContext(ctx, query, streamID)
	if err != nil {
		return fmt.Errorf("failed to cancel schedule: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return domain.ErrStreamNotFound
	}
	return nil
}

func (r *liveStreamRepository) GetScheduledStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error) {
	query := fmt.Sprintf(`
		SELECT %s FROM live_streams
		WHERE status = 'scheduled'
		ORDER BY scheduled_start ASC
		LIMIT $1 OFFSET $2
	`, liveStreamSelectColumns(""))
	var streams []*domain.LiveStream
	if err := r.db.SelectContext(ctx, &streams, query, limit, offset); err != nil {
		return nil, fmt.Errorf("failed to get scheduled streams: %w", err)
	}
	return streams, nil
}

func (r *liveStreamRepository) GetUpcomingStreams(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.LiveStream, error) {
	query := fmt.Sprintf(`
		SELECT %s FROM live_streams ls
		JOIN channels c ON c.id = ls.channel_id
		WHERE ls.status = 'scheduled'
		  AND ls.scheduled_start > NOW()
		  AND c.account_id = $1
		ORDER BY ls.scheduled_start ASC
		LIMIT $2
	`, liveStreamSelectColumns("ls"))
	var streams []*domain.LiveStream
	if err := r.db.SelectContext(ctx, &streams, query, userID, limit); err != nil {
		return nil, fmt.Errorf("failed to get upcoming streams: %w", err)
	}
	return streams, nil
}

type StreamKeyRepository interface {
	Create(ctx context.Context, channelID uuid.UUID, keyPlaintext string, expiresAt *time.Time) (*domain.StreamKey, error)
	GetByChannelID(ctx context.Context, channelID uuid.UUID) (*domain.StreamKey, error)
	GetActiveByChannelID(ctx context.Context, channelID uuid.UUID) (*domain.StreamKey, error)
	ValidateKey(ctx context.Context, channelID uuid.UUID, keyPlaintext string) (*domain.StreamKey, error)
	MarkUsed(ctx context.Context, id uuid.UUID) error
	Deactivate(ctx context.Context, id uuid.UUID) error
	DeactivateAllForChannel(ctx context.Context, channelID uuid.UUID) error
	DeleteExpired(ctx context.Context) (int, error)
}

type streamKeyRepository struct {
	db *sqlx.DB
}

func NewStreamKeyRepository(db *sqlx.DB) StreamKeyRepository {
	return &streamKeyRepository{db: db}
}

func (r *streamKeyRepository) Create(ctx context.Context, channelID uuid.UUID, keyPlaintext string, expiresAt *time.Time) (*domain.StreamKey, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(keyPlaintext), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash stream key: %w", err)
	}

	key := &domain.StreamKey{
		ID:        uuid.New(),
		ChannelID: channelID,
		KeyHash:   string(hash),
		IsActive:  true,
		ExpiresAt: expiresAt,
	}

	query := `
		INSERT INTO stream_keys (id, channel_id, key_hash, is_active, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at
	`

	if err := r.db.QueryRowContext(ctx, query, key.ID, key.ChannelID, key.KeyHash, key.IsActive, key.ExpiresAt).Scan(&key.CreatedAt); err != nil {
		return nil, fmt.Errorf("failed to create stream key: %w", err)
	}

	return key, nil
}

func (r *streamKeyRepository) GetByChannelID(ctx context.Context, channelID uuid.UUID) (*domain.StreamKey, error) {
	var key domain.StreamKey
	query := `
		SELECT * FROM stream_keys
		WHERE channel_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`

	if err := r.db.GetContext(ctx, &key, query, channelID); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrStreamKeyInvalid
		}
		return nil, fmt.Errorf("failed to get stream key: %w", err)
	}

	return &key, nil
}

func (r *streamKeyRepository) GetActiveByChannelID(ctx context.Context, channelID uuid.UUID) (*domain.StreamKey, error) {
	var key domain.StreamKey
	query := `
		SELECT * FROM stream_keys
		WHERE channel_id = $1 AND is_active = true
		ORDER BY created_at DESC
		LIMIT 1
	`

	if err := r.db.GetContext(ctx, &key, query, channelID); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrStreamKeyInvalid
		}
		return nil, fmt.Errorf("failed to get active stream key: %w", err)
	}

	return &key, nil
}

func (r *streamKeyRepository) ValidateKey(ctx context.Context, channelID uuid.UUID, keyPlaintext string) (*domain.StreamKey, error) {
	key, err := r.GetActiveByChannelID(ctx, channelID)
	if err != nil {
		return nil, err
	}

	if err := key.CanUse(); err != nil {
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(key.KeyHash), []byte(keyPlaintext)); err != nil {
		return nil, domain.ErrStreamKeyInvalid
	}

	return key, nil
}

func (r *streamKeyRepository) MarkUsed(ctx context.Context, id uuid.UUID) error {
	query := `
		UPDATE stream_keys SET last_used_at = NOW()
		WHERE id = $1
	`

	if _, err := r.db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("failed to mark stream key as used: %w", err)
	}

	return nil
}

func (r *streamKeyRepository) Deactivate(ctx context.Context, id uuid.UUID) error {
	query := `
		UPDATE stream_keys SET is_active = false
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to deactivate stream key: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return domain.ErrStreamKeyInvalid
	}

	return nil
}

func (r *streamKeyRepository) DeactivateAllForChannel(ctx context.Context, channelID uuid.UUID) error {
	query := `
		UPDATE stream_keys SET is_active = false
		WHERE channel_id = $1
	`

	if _, err := r.db.ExecContext(ctx, query, channelID); err != nil {
		return fmt.Errorf("failed to deactivate channel stream keys: %w", err)
	}

	return nil
}

func (r *streamKeyRepository) DeleteExpired(ctx context.Context) (int, error) {
	query := `
		DELETE FROM stream_keys
		WHERE expires_at IS NOT NULL AND expires_at < NOW()
	`

	result, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired keys: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return int(rows), nil
}

type ViewerSessionRepository interface {
	Create(ctx context.Context, session *domain.ViewerSession) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.ViewerSession, error)
	GetBySessionID(ctx context.Context, sessionID string) (*domain.ViewerSession, error)
	GetActiveByStream(ctx context.Context, streamID uuid.UUID, limit, offset int) ([]*domain.ViewerSession, error)
	CountActiveViewers(ctx context.Context, streamID uuid.UUID) (int, error)
	UpdateHeartbeat(ctx context.Context, sessionID string) error
	BatchUpdateHeartbeats(ctx context.Context, sessionIDs []string) error
	EndSession(ctx context.Context, sessionID string) error
	CleanupStale(ctx context.Context) (int, error)
}

type viewerSessionRepository struct {
	db *sqlx.DB
}

func NewViewerSessionRepository(db *sqlx.DB) ViewerSessionRepository {
	return &viewerSessionRepository{db: db}
}

func (r *viewerSessionRepository) Create(ctx context.Context, session *domain.ViewerSession) error {
	query := `
		INSERT INTO viewer_sessions (
			id, live_stream_id, session_id, user_id, ip_address,
			user_agent, country_code
		) VALUES (
			:id, :live_stream_id, :session_id, :user_id, :ip_address,
			:user_agent, :country_code
		)
		RETURNING joined_at, last_heartbeat_at
	`

	rows, err := r.db.NamedQueryContext(ctx, query, session)
	if err != nil {
		return fmt.Errorf("failed to create viewer session: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	if rows.Next() {
		if err := rows.Scan(&session.JoinedAt, &session.LastHeartbeatAt); err != nil {
			return fmt.Errorf("failed to scan created session: %w", err)
		}
	}

	return nil
}

func (r *viewerSessionRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.ViewerSession, error) {
	var session domain.ViewerSession
	query := `SELECT * FROM viewer_sessions WHERE id = $1`

	if err := r.db.GetContext(ctx, &session, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrViewerSessionNotFound
		}
		return nil, fmt.Errorf("failed to get viewer session: %w", err)
	}

	return &session, nil
}

func (r *viewerSessionRepository) GetBySessionID(ctx context.Context, sessionID string) (*domain.ViewerSession, error) {
	var session domain.ViewerSession
	query := `SELECT * FROM viewer_sessions WHERE session_id = $1`

	if err := r.db.GetContext(ctx, &session, query, sessionID); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrViewerSessionNotFound
		}
		return nil, fmt.Errorf("failed to get viewer session: %w", err)
	}

	return &session, nil
}

func (r *viewerSessionRepository) GetActiveByStream(ctx context.Context, streamID uuid.UUID, limit, offset int) ([]*domain.ViewerSession, error) {
	var sessions []*domain.ViewerSession
	query := `
		SELECT * FROM viewer_sessions
		WHERE live_stream_id = $1 AND left_at IS NULL
		ORDER BY joined_at DESC
		LIMIT $2 OFFSET $3
	`

	if err := r.db.SelectContext(ctx, &sessions, query, streamID, limit, offset); err != nil {
		return nil, fmt.Errorf("failed to get active viewer sessions: %w", err)
	}

	return sessions, nil
}

func (r *viewerSessionRepository) CountActiveViewers(ctx context.Context, streamID uuid.UUID) (int, error) {
	var count int
	query := `SELECT get_live_viewer_count($1)`

	if err := r.db.GetContext(ctx, &count, query, streamID); err != nil {
		return 0, fmt.Errorf("failed to count active viewers: %w", err)
	}

	return count, nil
}

func (r *viewerSessionRepository) UpdateHeartbeat(ctx context.Context, sessionID string) error {
	query := `
		UPDATE viewer_sessions
		SET last_heartbeat_at = NOW()
		WHERE session_id = $1 AND left_at IS NULL
	`

	result, err := r.db.ExecContext(ctx, query, sessionID)
	if err != nil {
		return fmt.Errorf("failed to update heartbeat: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return domain.ErrViewerSessionNotFound
	}

	return nil
}

func (r *viewerSessionRepository) BatchUpdateHeartbeats(ctx context.Context, sessionIDs []string) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE viewer_sessions
		SET last_heartbeat_at = NOW()
		WHERE session_id = ANY($1) AND left_at IS NULL
	`, pq.Array(sessionIDs))
	if err != nil {
		return fmt.Errorf("failed to batch update heartbeats: %w", err)
	}
	return nil
}

func (r *viewerSessionRepository) EndSession(ctx context.Context, sessionID string) error {
	query := `
		UPDATE viewer_sessions
		SET left_at = NOW()
		WHERE session_id = $1 AND left_at IS NULL
	`

	result, err := r.db.ExecContext(ctx, query, sessionID)
	if err != nil {
		return fmt.Errorf("failed to end session: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return domain.ErrViewerSessionNotFound
	}

	return nil
}

func (r *viewerSessionRepository) CleanupStale(ctx context.Context) (int, error) {
	query := `SELECT cleanup_stale_viewer_sessions()`

	var count int
	if err := r.db.GetContext(ctx, &count, query); err != nil {
		return 0, fmt.Errorf("failed to cleanup stale sessions: %w", err)
	}

	return count, nil
}
