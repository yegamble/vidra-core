package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupModerationMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newModerationRepo(t *testing.T) (*ModerationRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupModerationMockDB(t)
	repo := NewModerationRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func sampleAbuseReport() domain.AbuseReport {
	now := time.Now()
	return domain.AbuseReport{
		ID:         "report-1",
		ReporterID: "user-1",
		Reason:     "spam",
		Details:    sql.NullString{String: "lots of spam", Valid: true},
		Status:     domain.AbuseReportStatusPending,
		EntityType: domain.ReportedEntityVideo,
		VideoID:    sql.NullString{String: "video-1", Valid: true},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func sampleBlocklistEntry() domain.BlocklistEntry {
	now := time.Now()
	return domain.BlocklistEntry{
		ID:           "block-1",
		BlockType:    domain.BlockTypeDomain,
		BlockedValue: "evil.com",
		Reason:       sql.NullString{String: "malware", Valid: true},
		BlockedBy:    "admin-1",
		ExpiresAt:    sql.NullTime{},
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func sampleInstanceConfig() domain.InstanceConfig {
	now := time.Now()
	return domain.InstanceConfig{
		Key:         "instance_name",
		Value:       json.RawMessage(`"Athena"`),
		Description: sql.NullString{String: "The instance name", Valid: true},
		IsPublic:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// ---------------------------------------------------------------------------
// GetUserRole
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_GetUserRole(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT role FROM users WHERE id = $1")).
			WithArgs("user-1").
			WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("admin"))

		role, err := repo.GetUserRole(ctx, "user-1")
		require.NoError(t, err)
		assert.Equal(t, domain.UserRole("admin"), role)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT role FROM users WHERE id = $1")).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		role, err := repo.GetUserRole(ctx, "missing")
		require.Error(t, err)
		assert.Equal(t, domain.UserRole(""), role)
		var domErr domain.DomainError
		require.ErrorAs(t, err, &domErr)
		assert.Equal(t, "NOT_FOUND", domErr.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT role FROM users WHERE id = $1")).
			WithArgs("user-1").
			WillReturnError(errors.New("connection refused"))

		_, err := repo.GetUserRole(ctx, "user-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// CreateAbuseReport
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_CreateAbuseReport(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		report := sampleAbuseReport()
		report.ID = ""
		now := time.Now()

		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO abuse_reports (
			reporter_id, reason, details, status,
			reported_entity_type, reported_video_id, reported_comment_id,
			reported_user_id, reported_channel_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at`)).
			WithArgs(
				report.ReporterID,
				report.Reason,
				report.Details,
				report.Status,
				report.EntityType,
				report.VideoID,
				report.CommentID,
				report.UserID,
				report.ChannelID,
			).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
				AddRow("report-new", now, now))

		err := repo.CreateAbuseReport(ctx, &report)
		require.NoError(t, err)
		assert.Equal(t, "report-new", report.ID)
		assert.False(t, report.CreatedAt.IsZero())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		report := sampleAbuseReport()

		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO abuse_reports (
			reporter_id, reason, details, status,
			reported_entity_type, reported_video_id, reported_comment_id,
			reported_user_id, reported_channel_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at`)).
			WithArgs(
				report.ReporterID,
				report.Reason,
				report.Details,
				report.Status,
				report.EntityType,
				report.VideoID,
				report.CommentID,
				report.UserID,
				report.ChannelID,
			).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateAbuseReport(ctx, &report)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetAbuseReport
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_GetAbuseReport(t *testing.T) {
	ctx := context.Background()
	report := sampleAbuseReport()

	abuseReportColumns := []string{
		"id", "reporter_id", "reason", "details", "status", "moderator_notes",
		"moderated_by", "moderated_at", "reported_entity_type",
		"reported_video_id", "reported_comment_id", "reported_user_id",
		"reported_channel_id", "created_at", "updated_at",
	}

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows(abuseReportColumns).AddRow(
			report.ID, report.ReporterID, report.Reason, report.Details, report.Status,
			report.ModeratorNotes, report.ModeratedBy, report.ModeratedAt,
			report.EntityType, report.VideoID, report.CommentID, report.UserID,
			report.ChannelID, report.CreatedAt, report.UpdatedAt,
		)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, reporter_id, reason, details, status, moderator_notes,
		       moderated_by, moderated_at, reported_entity_type,
		       reported_video_id, reported_comment_id, reported_user_id,
		       reported_channel_id, created_at, updated_at
		FROM abuse_reports
		WHERE id = $1`)).
			WithArgs("report-1").
			WillReturnRows(rows)

		got, err := repo.GetAbuseReport(ctx, "report-1")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, report.ID, got.ID)
		assert.Equal(t, report.ReporterID, got.ReporterID)
		assert.Equal(t, report.Reason, got.Reason)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, reporter_id, reason, details, status, moderator_notes,
		       moderated_by, moderated_at, reported_entity_type,
		       reported_video_id, reported_comment_id, reported_user_id,
		       reported_channel_id, created_at, updated_at
		FROM abuse_reports
		WHERE id = $1`)).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetAbuseReport(ctx, "missing")
		require.Nil(t, got)
		require.Error(t, err)
		var domErr domain.DomainError
		require.ErrorAs(t, err, &domErr)
		assert.Equal(t, "NOT_FOUND", domErr.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, reporter_id, reason, details, status, moderator_notes,
		       moderated_by, moderated_at, reported_entity_type,
		       reported_video_id, reported_comment_id, reported_user_id,
		       reported_channel_id, created_at, updated_at
		FROM abuse_reports
		WHERE id = $1`)).
			WithArgs("report-1").
			WillReturnError(errors.New("db error"))

		got, err := repo.GetAbuseReport(ctx, "report-1")
		require.NotNil(t, got) // returns &report with zero values on non-ErrNoRows errors
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db error")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// ListAbuseReports
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_ListAbuseReports(t *testing.T) {
	ctx := context.Background()
	report := sampleAbuseReport()

	abuseReportColumns := []string{
		"id", "reporter_id", "reason", "details", "status", "moderator_notes",
		"moderated_by", "moderated_at", "reported_entity_type",
		"reported_video_id", "reported_comment_id", "reported_user_id",
		"reported_channel_id", "created_at", "updated_at",
	}

	t.Run("success no filters", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM abuse_reports WHERE 1=1")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		rows := sqlmock.NewRows(abuseReportColumns).AddRow(
			report.ID, report.ReporterID, report.Reason, report.Details, report.Status,
			report.ModeratorNotes, report.ModeratedBy, report.ModeratedAt,
			report.EntityType, report.VideoID, report.CommentID, report.UserID,
			report.ChannelID, report.CreatedAt, report.UpdatedAt,
		)

		mock.ExpectQuery(`SELECT id, reporter_id, reason, details, status, moderator_notes,`).
			WithArgs(10, 0).
			WillReturnRows(rows)

		reports, total, err := repo.ListAbuseReports(ctx, "", "", 10, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, reports, 1)
		assert.Equal(t, report.ID, reports[0].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with status filter", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM abuse_reports WHERE 1=1 AND status = $1")).
			WithArgs("pending").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		rows := sqlmock.NewRows(abuseReportColumns).AddRow(
			report.ID, report.ReporterID, report.Reason, report.Details, report.Status,
			report.ModeratorNotes, report.ModeratedBy, report.ModeratedAt,
			report.EntityType, report.VideoID, report.CommentID, report.UserID,
			report.ChannelID, report.CreatedAt, report.UpdatedAt,
		)

		mock.ExpectQuery(`SELECT id, reporter_id, reason, details, status, moderator_notes,`).
			WithArgs("pending", 10, 0).
			WillReturnRows(rows)

		reports, total, err := repo.ListAbuseReports(ctx, "pending", "", 10, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, reports, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with status and entity type filters", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM abuse_reports WHERE 1=1 AND status = $1 AND reported_entity_type = $2")).
			WithArgs("pending", "video").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		rows := sqlmock.NewRows(abuseReportColumns).AddRow(
			report.ID, report.ReporterID, report.Reason, report.Details, report.Status,
			report.ModeratorNotes, report.ModeratedBy, report.ModeratedAt,
			report.EntityType, report.VideoID, report.CommentID, report.UserID,
			report.ChannelID, report.CreatedAt, report.UpdatedAt,
		)

		mock.ExpectQuery(`SELECT id, reporter_id, reason, details, status, moderator_notes,`).
			WithArgs("pending", "video", 10, 0).
			WillReturnRows(rows)

		reports, total, err := repo.ListAbuseReports(ctx, "pending", "video", 10, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, reports, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count query error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM abuse_reports WHERE 1=1")).
			WillReturnError(errors.New("count failed"))

		reports, total, err := repo.ListAbuseReports(ctx, "", "", 10, 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "count failed")
		assert.Nil(t, reports)
		assert.Equal(t, int64(0), total)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("select query error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM abuse_reports WHERE 1=1")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

		mock.ExpectQuery(`SELECT id, reporter_id, reason, details, status, moderator_notes,`).
			WithArgs(10, 0).
			WillReturnError(errors.New("select failed"))

		reports, total, err := repo.ListAbuseReports(ctx, "", "", 10, 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "select failed")
		assert.Nil(t, reports)
		assert.Equal(t, int64(5), total) // count query succeeded before select failed
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// UpdateAbuseReport
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_UpdateAbuseReport(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE abuse_reports
		SET status = $2, moderator_notes = $3, moderated_by = $4, moderated_at = CURRENT_TIMESTAMP
		WHERE id = $1`)).
			WithArgs("report-1", domain.AbuseReportStatusAccepted, sql.NullString{String: "valid report", Valid: true}, "mod-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateAbuseReport(ctx, "report-1", "mod-1", domain.AbuseReportStatusAccepted, "valid report")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with empty notes", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE abuse_reports
		SET status = $2, moderator_notes = $3, moderated_by = $4, moderated_at = CURRENT_TIMESTAMP
		WHERE id = $1`)).
			WithArgs("report-1", domain.AbuseReportStatusRejected, sql.NullString{String: "", Valid: false}, "mod-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateAbuseReport(ctx, "report-1", "mod-1", domain.AbuseReportStatusRejected, "")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE abuse_reports
		SET status = $2, moderator_notes = $3, moderated_by = $4, moderated_at = CURRENT_TIMESTAMP
		WHERE id = $1`)).
			WithArgs("missing", domain.AbuseReportStatusAccepted, sql.NullString{String: "", Valid: false}, "mod-1").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdateAbuseReport(ctx, "missing", "mod-1", domain.AbuseReportStatusAccepted, "")
		require.Error(t, err)
		var domErr domain.DomainError
		require.ErrorAs(t, err, &domErr)
		assert.Equal(t, "NOT_FOUND", domErr.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE abuse_reports
		SET status = $2, moderator_notes = $3, moderated_by = $4, moderated_at = CURRENT_TIMESTAMP
		WHERE id = $1`)).
			WithArgs("report-1", domain.AbuseReportStatusAccepted, sql.NullString{String: "", Valid: false}, "mod-1").
			WillReturnError(errors.New("exec failed"))

		err := repo.UpdateAbuseReport(ctx, "report-1", "mod-1", domain.AbuseReportStatusAccepted, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exec failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE abuse_reports
		SET status = $2, moderator_notes = $3, moderated_by = $4, moderated_at = CURRENT_TIMESTAMP
		WHERE id = $1`)).
			WithArgs("report-1", domain.AbuseReportStatusAccepted, sql.NullString{String: "", Valid: false}, "mod-1").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected err")))

		err := repo.UpdateAbuseReport(ctx, "report-1", "mod-1", domain.AbuseReportStatusAccepted, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rows affected err")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// DeleteAbuseReport
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_DeleteAbuseReport(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM abuse_reports WHERE id = $1")).
			WithArgs("report-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteAbuseReport(ctx, "report-1")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM abuse_reports WHERE id = $1")).
			WithArgs("missing").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.DeleteAbuseReport(ctx, "missing")
		require.Error(t, err)
		var domErr domain.DomainError
		require.ErrorAs(t, err, &domErr)
		assert.Equal(t, "NOT_FOUND", domErr.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM abuse_reports WHERE id = $1")).
			WithArgs("report-1").
			WillReturnError(errors.New("delete failed"))

		err := repo.DeleteAbuseReport(ctx, "report-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM abuse_reports WHERE id = $1")).
			WithArgs("report-1").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows err")))

		err := repo.DeleteAbuseReport(ctx, "report-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rows err")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// CreateBlocklistEntry
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_CreateBlocklistEntry(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		entry := sampleBlocklistEntry()
		entry.ID = ""
		now := time.Now()

		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO blocklist (block_type, blocked_value, reason, blocked_by, expires_at, is_active)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`)).
			WithArgs(entry.BlockType, entry.BlockedValue, entry.Reason, entry.BlockedBy, entry.ExpiresAt, entry.IsActive).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
				AddRow("block-new", now, now))

		err := repo.CreateBlocklistEntry(ctx, &entry)
		require.NoError(t, err)
		assert.Equal(t, "block-new", entry.ID)
		assert.False(t, entry.CreatedAt.IsZero())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		entry := sampleBlocklistEntry()

		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO blocklist (block_type, blocked_value, reason, blocked_by, expires_at, is_active)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`)).
			WithArgs(entry.BlockType, entry.BlockedValue, entry.Reason, entry.BlockedBy, entry.ExpiresAt, entry.IsActive).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateBlocklistEntry(ctx, &entry)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetBlocklistEntry
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_GetBlocklistEntry(t *testing.T) {
	ctx := context.Background()
	entry := sampleBlocklistEntry()

	blocklistColumns := []string{
		"id", "block_type", "blocked_value", "reason", "blocked_by",
		"expires_at", "is_active", "created_at", "updated_at",
	}

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows(blocklistColumns).AddRow(
			entry.ID, entry.BlockType, entry.BlockedValue, entry.Reason,
			entry.BlockedBy, entry.ExpiresAt, entry.IsActive, entry.CreatedAt, entry.UpdatedAt,
		)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, block_type, blocked_value, reason, blocked_by,
		       expires_at, is_active, created_at, updated_at
		FROM blocklist
		WHERE id = $1`)).
			WithArgs("block-1").
			WillReturnRows(rows)

		got, err := repo.GetBlocklistEntry(ctx, "block-1")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, entry.ID, got.ID)
		assert.Equal(t, entry.BlockType, got.BlockType)
		assert.Equal(t, entry.BlockedValue, got.BlockedValue)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, block_type, blocked_value, reason, blocked_by,
		       expires_at, is_active, created_at, updated_at
		FROM blocklist
		WHERE id = $1`)).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetBlocklistEntry(ctx, "missing")
		require.Nil(t, got)
		require.Error(t, err)
		var domErr domain.DomainError
		require.ErrorAs(t, err, &domErr)
		assert.Equal(t, "NOT_FOUND", domErr.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, block_type, blocked_value, reason, blocked_by,
		       expires_at, is_active, created_at, updated_at
		FROM blocklist
		WHERE id = $1`)).
			WithArgs("block-1").
			WillReturnError(errors.New("db error"))

		got, err := repo.GetBlocklistEntry(ctx, "block-1")
		require.NotNil(t, got) // returns &entry with zero values on non-ErrNoRows errors
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db error")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// ListBlocklistEntries
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_ListBlocklistEntries(t *testing.T) {
	ctx := context.Background()
	entry := sampleBlocklistEntry()

	blocklistColumns := []string{
		"id", "block_type", "blocked_value", "reason", "blocked_by",
		"expires_at", "is_active", "created_at", "updated_at",
	}

	t.Run("success no filters", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM blocklist WHERE 1=1")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		rows := sqlmock.NewRows(blocklistColumns).AddRow(
			entry.ID, entry.BlockType, entry.BlockedValue, entry.Reason,
			entry.BlockedBy, entry.ExpiresAt, entry.IsActive, entry.CreatedAt, entry.UpdatedAt,
		)

		mock.ExpectQuery(`SELECT id, block_type, blocked_value, reason, blocked_by,`).
			WithArgs(10, 0).
			WillReturnRows(rows)

		entries, total, err := repo.ListBlocklistEntries(ctx, "", false, 10, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, entries, 1)
		assert.Equal(t, entry.ID, entries[0].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with block type filter", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM blocklist WHERE 1=1 AND block_type = $1")).
			WithArgs("domain").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		rows := sqlmock.NewRows(blocklistColumns).AddRow(
			entry.ID, entry.BlockType, entry.BlockedValue, entry.Reason,
			entry.BlockedBy, entry.ExpiresAt, entry.IsActive, entry.CreatedAt, entry.UpdatedAt,
		)

		mock.ExpectQuery(`SELECT id, block_type, blocked_value, reason, blocked_by,`).
			WithArgs("domain", 10, 0).
			WillReturnRows(rows)

		entries, total, err := repo.ListBlocklistEntries(ctx, "domain", false, 10, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, entries, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with block type and active only filters", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM blocklist WHERE 1=1 AND block_type = $1 AND is_active = $2")).
			WithArgs("domain", true).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		rows := sqlmock.NewRows(blocklistColumns).AddRow(
			entry.ID, entry.BlockType, entry.BlockedValue, entry.Reason,
			entry.BlockedBy, entry.ExpiresAt, entry.IsActive, entry.CreatedAt, entry.UpdatedAt,
		)

		mock.ExpectQuery(`SELECT id, block_type, blocked_value, reason, blocked_by,`).
			WithArgs("domain", true, 10, 0).
			WillReturnRows(rows)

		entries, total, err := repo.ListBlocklistEntries(ctx, "domain", true, 10, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, entries, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count query error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM blocklist WHERE 1=1")).
			WillReturnError(errors.New("count failed"))

		entries, total, err := repo.ListBlocklistEntries(ctx, "", false, 10, 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "count failed")
		assert.Nil(t, entries)
		assert.Equal(t, int64(0), total)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("select query error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM blocklist WHERE 1=1")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

		mock.ExpectQuery(`SELECT id, block_type, blocked_value, reason, blocked_by,`).
			WithArgs(10, 0).
			WillReturnError(errors.New("select failed"))

		entries, total, err := repo.ListBlocklistEntries(ctx, "", false, 10, 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "select failed")
		assert.Nil(t, entries)
		assert.Equal(t, int64(5), total) // count query succeeded before select failed
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// UpdateBlocklistEntry
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_UpdateBlocklistEntry(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		expiresAt := sql.NullTime{Time: time.Now().Add(24 * time.Hour), Valid: true}

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE blocklist
		SET is_active = $2, expires_at = $3
		WHERE id = $1`)).
			WithArgs("block-1", false, expiresAt).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateBlocklistEntry(ctx, "block-1", false, expiresAt)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE blocklist
		SET is_active = $2, expires_at = $3
		WHERE id = $1`)).
			WithArgs("missing", true, sql.NullTime{}).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdateBlocklistEntry(ctx, "missing", true, sql.NullTime{})
		require.Error(t, err)
		var domErr domain.DomainError
		require.ErrorAs(t, err, &domErr)
		assert.Equal(t, "NOT_FOUND", domErr.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE blocklist
		SET is_active = $2, expires_at = $3
		WHERE id = $1`)).
			WithArgs("block-1", true, sql.NullTime{}).
			WillReturnError(errors.New("exec failed"))

		err := repo.UpdateBlocklistEntry(ctx, "block-1", true, sql.NullTime{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exec failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE blocklist
		SET is_active = $2, expires_at = $3
		WHERE id = $1`)).
			WithArgs("block-1", true, sql.NullTime{}).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows err")))

		err := repo.UpdateBlocklistEntry(ctx, "block-1", true, sql.NullTime{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rows err")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// DeleteBlocklistEntry
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_DeleteBlocklistEntry(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM blocklist WHERE id = $1")).
			WithArgs("block-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteBlocklistEntry(ctx, "block-1")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM blocklist WHERE id = $1")).
			WithArgs("missing").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.DeleteBlocklistEntry(ctx, "missing")
		require.Error(t, err)
		var domErr domain.DomainError
		require.ErrorAs(t, err, &domErr)
		assert.Equal(t, "NOT_FOUND", domErr.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM blocklist WHERE id = $1")).
			WithArgs("block-1").
			WillReturnError(errors.New("delete failed"))

		err := repo.DeleteBlocklistEntry(ctx, "block-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM blocklist WHERE id = $1")).
			WithArgs("block-1").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows err")))

		err := repo.DeleteBlocklistEntry(ctx, "block-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rows err")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// IsBlocked
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_IsBlocked(t *testing.T) {
	ctx := context.Background()

	t.Run("blocked", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT EXISTS(
			SELECT 1 FROM blocklist
			WHERE block_type = $1
			AND blocked_value = $2
			AND is_active = true
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		)`)).
			WithArgs(domain.BlockTypeDomain, "evil.com").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		blocked, err := repo.IsBlocked(ctx, domain.BlockTypeDomain, "evil.com")
		require.NoError(t, err)
		assert.True(t, blocked)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not blocked", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT EXISTS(
			SELECT 1 FROM blocklist
			WHERE block_type = $1
			AND blocked_value = $2
			AND is_active = true
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		)`)).
			WithArgs(domain.BlockTypeDomain, "good.com").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		blocked, err := repo.IsBlocked(ctx, domain.BlockTypeDomain, "good.com")
		require.NoError(t, err)
		assert.False(t, blocked)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT EXISTS(
			SELECT 1 FROM blocklist
			WHERE block_type = $1
			AND blocked_value = $2
			AND is_active = true
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		)`)).
			WithArgs(domain.BlockTypeIP, "1.2.3.4").
			WillReturnError(errors.New("query failed"))

		blocked, err := repo.IsBlocked(ctx, domain.BlockTypeIP, "1.2.3.4")
		require.Error(t, err)
		assert.False(t, blocked)
		assert.Contains(t, err.Error(), "query failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetInstanceConfig
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_GetInstanceConfig(t *testing.T) {
	ctx := context.Background()
	cfg := sampleInstanceConfig()

	configColumns := []string{"key", "value", "description", "is_public", "created_at", "updated_at"}

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows(configColumns).AddRow(
			cfg.Key, cfg.Value, cfg.Description, cfg.IsPublic, cfg.CreatedAt, cfg.UpdatedAt,
		)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT key, value, description, is_public, created_at, updated_at
		FROM instance_config
		WHERE key = $1`)).
			WithArgs("instance_name").
			WillReturnRows(rows)

		got, err := repo.GetInstanceConfig(ctx, "instance_name")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, cfg.Key, got.Key)
		assert.True(t, got.IsPublic)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT key, value, description, is_public, created_at, updated_at
		FROM instance_config
		WHERE key = $1`)).
			WithArgs("missing_key").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetInstanceConfig(ctx, "missing_key")
		require.Nil(t, got)
		require.Error(t, err)
		var domErr domain.DomainError
		require.ErrorAs(t, err, &domErr)
		assert.Equal(t, "NOT_FOUND", domErr.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT key, value, description, is_public, created_at, updated_at
		FROM instance_config
		WHERE key = $1`)).
			WithArgs("instance_name").
			WillReturnError(errors.New("db error"))

		got, err := repo.GetInstanceConfig(ctx, "instance_name")
		require.NotNil(t, got) // returns &config with zero values on non-ErrNoRows errors
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db error")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// ListInstanceConfigs
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_ListInstanceConfigs(t *testing.T) {
	ctx := context.Background()
	cfg := sampleInstanceConfig()

	configColumns := []string{"key", "value", "description", "is_public", "created_at", "updated_at"}

	t.Run("success all configs", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows(configColumns).AddRow(
			cfg.Key, cfg.Value, cfg.Description, cfg.IsPublic, cfg.CreatedAt, cfg.UpdatedAt,
		)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT key, value, description, is_public, created_at, updated_at
		FROM instance_config ORDER BY key`)).
			WillReturnRows(rows)

		configs, err := repo.ListInstanceConfigs(ctx, false)
		require.NoError(t, err)
		require.Len(t, configs, 1)
		assert.Equal(t, cfg.Key, configs[0].Key)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success public only", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows(configColumns).AddRow(
			cfg.Key, cfg.Value, cfg.Description, cfg.IsPublic, cfg.CreatedAt, cfg.UpdatedAt,
		)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT key, value, description, is_public, created_at, updated_at
		FROM instance_config WHERE is_public = true ORDER BY key`)).
			WillReturnRows(rows)

		configs, err := repo.ListInstanceConfigs(ctx, true)
		require.NoError(t, err)
		require.Len(t, configs, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT key, value, description, is_public, created_at, updated_at
		FROM instance_config ORDER BY key`)).
			WillReturnError(errors.New("db error"))

		configs, err := repo.ListInstanceConfigs(ctx, false)
		require.Error(t, err)
		assert.Nil(t, configs)
		assert.Contains(t, err.Error(), "db error")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// UpdateInstanceConfig
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_UpdateInstanceConfig(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		value := json.RawMessage(`"Athena Instance"`)

		mock.ExpectExec(regexp.QuoteMeta(
			`INSERT INTO instance_config (key, value, is_public)
		VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE
		SET value = EXCLUDED.value, is_public = EXCLUDED.is_public`)).
			WithArgs("instance_name", []byte(value), true).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateInstanceConfig(ctx, "instance_name", value, true)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		value := json.RawMessage(`"test"`)

		mock.ExpectExec(regexp.QuoteMeta(
			`INSERT INTO instance_config (key, value, is_public)
		VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE
		SET value = EXCLUDED.value, is_public = EXCLUDED.is_public`)).
			WithArgs("key", []byte(value), false).
			WillReturnError(errors.New("exec failed"))

		err := repo.UpdateInstanceConfig(ctx, "key", value, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exec failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetInstanceStats
// ---------------------------------------------------------------------------

func TestModerationRepository_Unit_GetInstanceStats(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM users WHERE is_active = true")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(100))

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM videos WHERE privacy = 'public'")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(500))

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM user_views")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10000))

		totalUsers, totalVideos, totalLocalVideos, totalViews, err := repo.GetInstanceStats(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(100), totalUsers)
		assert.Equal(t, int64(500), totalVideos)
		assert.Equal(t, int64(500), totalLocalVideos) // totalLocalVideos == totalVideos
		assert.Equal(t, int64(10000), totalViews)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("users count error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM users WHERE is_active = true")).
			WillReturnError(errors.New("users count failed"))

		_, _, _, _, err := repo.GetInstanceStats(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "users count failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("videos count error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM users WHERE is_active = true")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(100))

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM videos WHERE privacy = 'public'")).
			WillReturnError(errors.New("videos count failed"))

		_, _, _, _, err := repo.GetInstanceStats(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "videos count failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("views count error", func(t *testing.T) {
		repo, mock, cleanup := newModerationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM users WHERE is_active = true")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(100))

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM videos WHERE privacy = 'public'")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(500))

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM user_views")).
			WillReturnError(errors.New("views count failed"))

		_, _, _, _, err := repo.GetInstanceStats(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "views count failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
