package repository

import (
	"vidra-core/internal/domain"
	"vidra-core/internal/usecase"
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type uploadRepository struct {
	db *sqlx.DB
	tm *TransactionManager
}

func NewUploadRepository(db *sqlx.DB) usecase.UploadRepository {
	return &uploadRepository{
		db: db,
		tm: NewTransactionManager(db),
	}
}

func (r *uploadRepository) CreateSession(ctx context.Context, session *domain.UploadSession) error {
	query := `
		INSERT INTO upload_sessions (
			id, video_id, user_id, filename, file_size, chunk_size,
			total_chunks, uploaded_chunks, status, temp_file_path,
			created_at, updated_at, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)`

	_, err := r.db.ExecContext(ctx, query,
		session.ID, session.VideoID, session.UserID, session.FileName,
		session.FileSize, session.ChunkSize, session.TotalChunks,
		pq.Array(session.UploadedChunks), session.Status, session.TempFilePath,
		session.CreatedAt, session.UpdatedAt, session.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create upload session: %w", err)
	}
	return nil
}

func (r *uploadRepository) GetSession(ctx context.Context, sessionID string) (*domain.UploadSession, error) {
	query := `
		SELECT id, video_id, user_id, filename, file_size, chunk_size,
		       total_chunks, uploaded_chunks, status, temp_file_path,
		       created_at, updated_at, expires_at
		FROM upload_sessions WHERE id = $1`

	var session domain.UploadSession
	var uploadedChunks pq.Int32Array

	err := r.db.QueryRowContext(ctx, query, sessionID).Scan(
		&session.ID, &session.VideoID, &session.UserID, &session.FileName,
		&session.FileSize, &session.ChunkSize, &session.TotalChunks,
		&uploadedChunks, &session.Status, &session.TempFilePath,
		&session.CreatedAt, &session.UpdatedAt, &session.ExpiresAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.NewDomainError("SESSION_NOT_FOUND", "Upload session not found")
		}
		return nil, fmt.Errorf("failed to get upload session: %w", err)
	}

	// Convert pq.Int32Array to []int
	session.UploadedChunks = make([]int, len(uploadedChunks))
	for i, chunk := range uploadedChunks {
		session.UploadedChunks[i] = int(chunk)
	}

	return &session, nil
}

func (r *uploadRepository) UpdateSession(ctx context.Context, session *domain.UploadSession) error {
	query := `
		UPDATE upload_sessions SET
			uploaded_chunks = $2, status = $3, temp_file_path = $4,
			updated_at = $5
		WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query,
		session.ID, pq.Array(session.UploadedChunks), session.Status,
		session.TempFilePath, session.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update upload session: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return domain.NewDomainError("SESSION_NOT_FOUND", "Upload session not found")
	}

	return nil
}

func (r *uploadRepository) DeleteSession(ctx context.Context, sessionID string) error {
	query := `DELETE FROM upload_sessions WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, sessionID)
	if err != nil {
		return fmt.Errorf("failed to delete upload session: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return domain.NewDomainError("SESSION_NOT_FOUND", "Upload session not found")
	}

	return nil
}

func (r *uploadRepository) RecordChunk(ctx context.Context, sessionID string, chunkIndex int) error {
	// Use transaction for atomic check-then-update
	return r.tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
		// Check if chunk is already recorded
		checkQuery := `SELECT $2 = ANY(uploaded_chunks) FROM upload_sessions WHERE id = $1`
		var isUploaded bool
		err := tx.QueryRowContext(ctx, checkQuery, sessionID, chunkIndex).Scan(&isUploaded)
		if err != nil {
			if err == sql.ErrNoRows {
				return domain.NewDomainError("SESSION_NOT_FOUND", "Upload session not found")
			}
			return fmt.Errorf("failed to check chunk status: %w", err)
		}

		if isUploaded {
			return nil // Already recorded
		}

		// Add chunk to uploaded_chunks array
		updateQuery := `
			UPDATE upload_sessions
			SET uploaded_chunks = array_append(uploaded_chunks, $2),
			    updated_at = NOW()
			WHERE id = $1`

		result, err := tx.ExecContext(ctx, updateQuery, sessionID, chunkIndex)
		if err != nil {
			return fmt.Errorf("failed to record chunk: %w", err)
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to get rows affected: %w", err)
		}
		if rowsAffected == 0 {
			return domain.NewDomainError("SESSION_NOT_FOUND", "Upload session not found")
		}

		return nil
	})
}

func (r *uploadRepository) GetUploadedChunks(ctx context.Context, sessionID string) ([]int, error) {
	query := `SELECT uploaded_chunks FROM upload_sessions WHERE id = $1`

	var uploadedChunks pq.Int32Array
	err := r.db.QueryRowContext(ctx, query, sessionID).Scan(&uploadedChunks)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.NewDomainError("SESSION_NOT_FOUND", "Upload session not found")
		}
		return nil, fmt.Errorf("failed to get uploaded chunks: %w", err)
	}

	// Convert pq.Int32Array to []int
	chunks := make([]int, len(uploadedChunks))
	for i, chunk := range uploadedChunks {
		chunks[i] = int(chunk)
	}

	return chunks, nil
}

func (r *uploadRepository) IsChunkUploaded(ctx context.Context, sessionID string, chunkIndex int) (bool, error) {
	query := `SELECT $2 = ANY(uploaded_chunks) FROM upload_sessions WHERE id = $1`

	var isUploaded bool
	err := r.db.QueryRowContext(ctx, query, sessionID, chunkIndex).Scan(&isUploaded)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, domain.NewDomainError("SESSION_NOT_FOUND", "Upload session not found")
		}
		return false, fmt.Errorf("failed to check chunk status: %w", err)
	}

	return isUploaded, nil
}

func (r *uploadRepository) ExpireOldSessions(ctx context.Context) error {
	query := `
		UPDATE upload_sessions
		SET status = 'expired', updated_at = NOW()
		WHERE expires_at < NOW() AND status = 'active'`

	_, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to expire old sessions: %w", err)
	}

	return nil
}

func (r *uploadRepository) GetExpiredSessions(ctx context.Context) ([]*domain.UploadSession, error) {
	query := `
		SELECT id, video_id, user_id, filename, file_size, chunk_size,
		       total_chunks, uploaded_chunks, status, temp_file_path,
		       created_at, updated_at, expires_at
		FROM upload_sessions
		WHERE status = 'expired' OR (expires_at < NOW() AND status != 'completed')`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get expired sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var sessions []*domain.UploadSession
	for rows.Next() {
		var session domain.UploadSession
		var uploadedChunks pq.Int32Array

		err := rows.Scan(
			&session.ID, &session.VideoID, &session.UserID, &session.FileName,
			&session.FileSize, &session.ChunkSize, &session.TotalChunks,
			&uploadedChunks, &session.Status, &session.TempFilePath,
			&session.CreatedAt, &session.UpdatedAt, &session.ExpiresAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan expired session: %w", err)
		}

		// Convert pq.Int32Array to []int
		session.UploadedChunks = make([]int, len(uploadedChunks))
		for i, chunk := range uploadedChunks {
			session.UploadedChunks[i] = int(chunk)
		}

		sessions = append(sessions, &session)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	return sessions, nil
}
