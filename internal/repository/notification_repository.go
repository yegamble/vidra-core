package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"athena/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type NotificationRepository struct {
	db *sqlx.DB
}

func NewNotificationRepository(db *sqlx.DB) *NotificationRepository {
	return &NotificationRepository{db: db}
}

// Create creates a new notification
func (r *NotificationRepository) Create(ctx context.Context, notification *domain.Notification) error {
	query := `
		INSERT INTO notifications (user_id, type, title, message, data)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`

	dataJSON, err := json.Marshal(notification.Data)
	if err != nil {
		return fmt.Errorf("marshaling notification data: %w", err)
	}

	err = r.db.QueryRowContext(
		ctx, query,
		notification.UserID,
		notification.Type,
		notification.Title,
		notification.Message,
		dataJSON,
	).Scan(&notification.ID, &notification.CreatedAt)

	if err != nil {
		return fmt.Errorf("creating notification: %w", err)
	}

	return nil
}

// CreateBatch creates multiple notifications in a single transaction
func (r *NotificationRepository) CreateBatch(ctx context.Context, notifications []domain.Notification) error {
	if len(notifications) == 0 {
		return nil
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO notifications (user_id, type, title, message, data)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`)
	if err != nil {
		return fmt.Errorf("preparing statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i := range notifications {
		dataJSON, err := json.Marshal(notifications[i].Data)
		if err != nil {
			return fmt.Errorf("marshaling notification data: %w", err)
		}

		err = stmt.QueryRowContext(
			ctx,
			notifications[i].UserID,
			notifications[i].Type,
			notifications[i].Title,
			notifications[i].Message,
			dataJSON,
		).Scan(&notifications[i].ID, &notifications[i].CreatedAt)

		if err != nil {
			return fmt.Errorf("inserting notification: %w", err)
		}
	}

	return tx.Commit()
}

// GetByID retrieves a notification by ID
func (r *NotificationRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Notification, error) {
	var notification domain.Notification
	var dataJSON []byte

	query := `
		SELECT id, user_id, type, title, message, data, read, created_at, read_at
		FROM notifications
		WHERE id = $1`

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&notification.ID,
		&notification.UserID,
		&notification.Type,
		&notification.Title,
		&notification.Message,
		&dataJSON,
		&notification.Read,
		&notification.CreatedAt,
		&notification.ReadAt,
	)

	if err == sql.ErrNoRows {
		return nil, domain.ErrNotificationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting notification: %w", err)
	}

	if err := json.Unmarshal(dataJSON, &notification.Data); err != nil {
		return nil, fmt.Errorf("unmarshaling notification data: %w", err)
	}

	return &notification, nil
}

// ListByUser retrieves notifications for a user with filtering options
func (r *NotificationRepository) ListByUser(ctx context.Context, filter domain.NotificationFilter) ([]domain.Notification, error) {
	query := `
		SELECT id, user_id, type, title, message, data, read, created_at, read_at
		FROM notifications
		WHERE user_id = $1`

	args := []interface{}{filter.UserID}
	argCount := 1

	if filter.Unread != nil {
		argCount++
		query += fmt.Sprintf(" AND read = $%d", argCount)
		args = append(args, !*filter.Unread)
	}

	if len(filter.Types) > 0 {
		argCount++
		query += fmt.Sprintf(" AND type = ANY($%d)", argCount)
		args = append(args, filter.Types)
	}

	if filter.StartDate != nil {
		argCount++
		query += fmt.Sprintf(" AND created_at >= $%d", argCount)
		args = append(args, *filter.StartDate)
	}

	if filter.EndDate != nil {
		argCount++
		query += fmt.Sprintf(" AND created_at <= $%d", argCount)
		args = append(args, *filter.EndDate)
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		argCount++
		query += fmt.Sprintf(" LIMIT $%d", argCount)
		args = append(args, filter.Limit)
	}

	if filter.Offset > 0 {
		argCount++
		query += fmt.Sprintf(" OFFSET $%d", argCount)
		args = append(args, filter.Offset)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying notifications: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var notifications []domain.Notification
	for rows.Next() {
		var notification domain.Notification
		var dataJSON []byte

		err := rows.Scan(
			&notification.ID,
			&notification.UserID,
			&notification.Type,
			&notification.Title,
			&notification.Message,
			&dataJSON,
			&notification.Read,
			&notification.CreatedAt,
			&notification.ReadAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning notification: %w", err)
		}

		if err := json.Unmarshal(dataJSON, &notification.Data); err != nil {
			return nil, fmt.Errorf("unmarshaling notification data: %w", err)
		}

		notifications = append(notifications, notification)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating notifications: %w", err)
	}

	return notifications, nil
}

// MarkAsRead marks a notification as read
func (r *NotificationRepository) MarkAsRead(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	query := `
		UPDATE notifications
		SET read = true, read_at = NOW()
		WHERE id = $1 AND user_id = $2 AND read = false`

	result, err := r.db.ExecContext(ctx, query, id, userID)
	if err != nil {
		return fmt.Errorf("marking notification as read: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("getting rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotificationNotFound
	}

	return nil
}

// MarkAllAsRead marks all notifications for a user as read
func (r *NotificationRepository) MarkAllAsRead(ctx context.Context, userID uuid.UUID) error {
	query := `
		UPDATE notifications
		SET read = true, read_at = NOW()
		WHERE user_id = $1 AND read = false`

	_, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("marking all notifications as read: %w", err)
	}

	return nil
}

// Delete deletes a notification
func (r *NotificationRepository) Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	query := `DELETE FROM notifications WHERE id = $1 AND user_id = $2`

	result, err := r.db.ExecContext(ctx, query, id, userID)
	if err != nil {
		return fmt.Errorf("deleting notification: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("getting rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotificationNotFound
	}

	return nil
}

// DeleteOldRead deletes read notifications older than the specified duration
func (r *NotificationRepository) DeleteOldRead(ctx context.Context, olderThan time.Duration) (int64, error) {
	query := `
		DELETE FROM notifications
		WHERE read = true AND read_at < $1`

	cutoffTime := time.Now().Add(-olderThan)
	result, err := r.db.ExecContext(ctx, query, cutoffTime)
	if err != nil {
		return 0, fmt.Errorf("deleting old notifications: %w", err)
	}

	return result.RowsAffected()
}

// GetUnreadCount returns the count of unread notifications for a user
func (r *NotificationRepository) GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read = false`

	err := r.db.QueryRowContext(ctx, query, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("getting unread count: %w", err)
	}

	return count, nil
}

// GetStats returns notification statistics for a user
func (r *NotificationRepository) GetStats(ctx context.Context, userID uuid.UUID) (*domain.NotificationStats, error) {
	stats := &domain.NotificationStats{
		ByType: make(map[domain.NotificationType]int),
	}

	// Get total and unread counts
	query := `
		SELECT 
			COUNT(*) as total,
			SUM(CASE WHEN read = false THEN 1 ELSE 0 END) as unread
		FROM notifications
		WHERE user_id = $1`

	err := r.db.QueryRowContext(ctx, query, userID).Scan(&stats.TotalCount, &stats.UnreadCount)
	if err != nil {
		return nil, fmt.Errorf("getting notification counts: %w", err)
	}

	// Get counts by type
	typeQuery := `
		SELECT type, COUNT(*) as count
		FROM notifications
		WHERE user_id = $1
		GROUP BY type`

	rows, err := r.db.QueryContext(ctx, typeQuery, userID)
	if err != nil {
		return nil, fmt.Errorf("getting type counts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var notifType domain.NotificationType
		var count int
		if err := rows.Scan(&notifType, &count); err != nil {
			return nil, fmt.Errorf("scanning type count: %w", err)
		}
		stats.ByType[notifType] = count
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating type counts: %w", err)
	}

	return stats, nil
}
