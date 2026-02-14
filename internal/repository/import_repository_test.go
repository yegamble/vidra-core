package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportRepository_Create(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	now := time.Now()
	imp := &domain.VideoImport{
		UserID:        "user-123",
		SourceURL:     "https://youtube.com/watch?v=test",
		Status:        domain.ImportStatusPending,
		TargetPrivacy: "private",
		Progress:      0,
	}

	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO video_imports (
			user_id, channel_id, source_url, status, target_privacy, target_category, metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		)
		RETURNING id, created_at, updated_at, progress, downloaded_bytes`)).
		WithArgs(
			imp.UserID,
			imp.ChannelID,
			imp.SourceURL,
			imp.Status,
			imp.TargetPrivacy,
			imp.TargetCategory,
			imp.Metadata,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at", "progress", "downloaded_bytes"}).
			AddRow("import-123", now, now, 0, int64(0)))

	err = repo.Create(ctx, imp)
	assert.NoError(t, err)
	assert.Equal(t, "import-123", imp.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_GetByID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	importID := "import-123"
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "channel_id", "source_url", "status", "video_id",
		"error_message", "progress", "metadata", "file_size_bytes",
		"downloaded_bytes", "target_privacy", "target_category",
		"created_at", "started_at", "completed_at", "updated_at",
	}).AddRow(
		importID, "user-123", nil, "https://youtube.com/watch?v=test",
		domain.ImportStatusDownloading, nil, nil, 45, []byte("{}"), nil, int64(50000000),
		"private", nil, now, &now, nil, now,
	)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, user_id, channel_id, source_url, status, video_id, error_message,
		       progress, metadata, file_size_bytes, downloaded_bytes, target_privacy,
		       target_category, created_at, started_at, completed_at, updated_at
		FROM video_imports
		WHERE id = $1`)).
		WithArgs(importID).
		WillReturnRows(rows)

	imp, err := repo.GetByID(ctx, importID)
	assert.NoError(t, err)
	assert.NotNil(t, imp)
	assert.Equal(t, importID, imp.ID)
	assert.Equal(t, "user-123", imp.UserID)
	assert.Equal(t, domain.ImportStatusDownloading, imp.Status)
	assert.Equal(t, 45, imp.Progress)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_GetByID_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	importID := "nonexistent"

	mock.ExpectQuery(`SELECT (.+) FROM video_imports WHERE id`).
		WithArgs(importID).
		WillReturnError(sql.ErrNoRows)

	imp, err := repo.GetByID(ctx, importID)
	assert.Error(t, err)
	assert.Equal(t, domain.ErrImportNotFound, err)
	assert.Nil(t, imp)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_GetByUserID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	userID := "user-123"
	limit := 20
	offset := 0
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "channel_id", "source_url", "status", "video_id",
		"error_message", "progress", "metadata", "file_size_bytes",
		"downloaded_bytes", "target_privacy", "target_category",
		"created_at", "started_at", "completed_at", "updated_at",
	}).
		AddRow("import-1", userID, nil, "https://youtube.com/watch?v=1",
			domain.ImportStatusCompleted, strPtr("video-1"), nil, 100, []byte("{}"), nil, int64(0),
			"private", nil, now, &now, &now, now).
		AddRow("import-2", userID, nil, "https://youtube.com/watch?v=2",
			domain.ImportStatusDownloading, nil, nil, 50, []byte("{}"), nil, int64(50000000),
			"private", nil, now, &now, nil, now)

	mock.ExpectQuery(`SELECT (.+) FROM video_imports WHERE user_id`).
		WithArgs(userID, limit, offset).
		WillReturnRows(rows)

	imports, err := repo.GetByUserID(ctx, userID, limit, offset)
	assert.NoError(t, err)
	assert.Len(t, imports, 2)
	assert.Equal(t, "import-1", imports[0].ID)
	assert.Equal(t, domain.ImportStatusCompleted, imports[0].Status)
	assert.Equal(t, "import-2", imports[1].ID)
	assert.Equal(t, domain.ImportStatusDownloading, imports[1].Status)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_CountByUserID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	userID := "user-123"

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM video_imports WHERE user_id`).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))

	count, err := repo.CountByUserID(ctx, userID)
	assert.NoError(t, err)
	assert.Equal(t, 42, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_CountByUserIDToday(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	userID := "user-123"

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM video_imports WHERE user_id.*AND created_at`).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

	count, err := repo.CountByUserIDToday(ctx, userID)
	assert.NoError(t, err)
	assert.Equal(t, 5, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_CountByUserIDAndStatus(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	userID := "user-123"
	status := domain.ImportStatusDownloading

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM video_imports WHERE user_id.*AND status`).
		WithArgs(userID, status).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	count, err := repo.CountByUserIDAndStatus(ctx, userID, status)
	assert.NoError(t, err)
	assert.Equal(t, 3, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_UpdateProgress(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	importID := "import-123"
	progress := 75
	downloadedBytes := int64(75000000)

	mock.ExpectExec(`UPDATE video_imports SET progress.*downloaded_bytes.*updated_at.*WHERE id`).
		WithArgs(importID, progress, downloadedBytes).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.UpdateProgress(ctx, importID, progress, downloadedBytes)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_MarkFailed(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	importID := "import-123"
	errorMessage := "download failed: connection timeout"

	mock.ExpectExec(`UPDATE video_imports SET status.*error_message.*updated_at.*WHERE id`).
		WithArgs(importID, domain.ImportStatusFailed, errorMessage).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.MarkFailed(ctx, importID, errorMessage)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_MarkCompleted(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	importID := "import-123"
	videoID := "video-456"

	mock.ExpectExec(`UPDATE video_imports SET status.*video_id.*progress.*completed_at.*WHERE id`).
		WithArgs(importID, domain.ImportStatusCompleted, videoID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.MarkCompleted(ctx, importID, videoID)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_GetPending(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	limit := 10
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "channel_id", "source_url", "status", "video_id",
		"error_message", "progress", "metadata", "file_size_bytes",
		"downloaded_bytes", "target_privacy", "target_category",
		"created_at", "started_at", "completed_at", "updated_at",
	}).
		AddRow("import-1", "user-123", nil, "https://youtube.com/watch?v=1",
			domain.ImportStatusPending, nil, nil, 0, []byte("{}"), nil, int64(0),
			"private", nil, now, nil, nil, now).
		AddRow("import-2", "user-456", nil, "https://youtube.com/watch?v=2",
			domain.ImportStatusPending, nil, nil, 0, []byte("{}"), nil, int64(0),
			"private", nil, now, nil, nil, now)

	mock.ExpectQuery(`SELECT (.+) FROM video_imports WHERE status.*ORDER BY created_at`).
		WithArgs(domain.ImportStatusPending, limit).
		WillReturnRows(rows)

	imports, err := repo.GetPending(ctx, limit)
	assert.NoError(t, err)
	assert.Len(t, imports, 2)
	assert.Equal(t, "import-1", imports[0].ID)
	assert.Equal(t, domain.ImportStatusPending, imports[0].Status)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_GetStuckImports(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	hoursStuck := 2
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "channel_id", "source_url", "status", "video_id",
		"error_message", "progress", "metadata", "file_size_bytes",
		"downloaded_bytes", "target_privacy", "target_category",
		"created_at", "started_at", "completed_at", "updated_at",
	}).
		AddRow("import-stuck", "user-123", nil, "https://youtube.com/watch?v=stuck",
			domain.ImportStatusDownloading, nil, nil, 30, []byte("{}"), nil, int64(30000000),
			"private", nil, now.Add(-3*time.Hour), nil, nil, now.Add(-3*time.Hour))

	mock.ExpectQuery(`SELECT (.+) FROM video_imports WHERE status IN.*AND updated_at`).
		WithArgs(domain.ImportStatusDownloading, domain.ImportStatusProcessing, hoursStuck).
		WillReturnRows(rows)

	imports, err := repo.GetStuckImports(ctx, hoursStuck)
	assert.NoError(t, err)
	assert.Len(t, imports, 1)
	assert.Equal(t, "import-stuck", imports[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_CleanupOldImports(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	daysOld := 30

	mock.ExpectExec(`DELETE FROM video_imports WHERE status IN.*AND updated_at`).
		WithArgs(domain.ImportStatusCompleted, domain.ImportStatusFailed, domain.ImportStatusCancelled, daysOld).
		WillReturnResult(sqlmock.NewResult(0, 15))

	deleted, err := repo.CleanupOldImports(ctx, daysOld)
	assert.NoError(t, err)
	assert.Equal(t, int64(15), deleted)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_Update(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewImportRepository(sqlxDB)

	ctx := context.Background()
	now := time.Now()
	imp := &domain.VideoImport{
		ID:              "import-123",
		UserID:          "user-123",
		SourceURL:       "https://youtube.com/watch?v=test",
		Status:          domain.ImportStatusDownloading,
		Progress:        50,
		DownloadedBytes: 50000000,
		TargetPrivacy:   "private",
		CreatedAt:       now,
		StartedAt:       &now,
		UpdatedAt:       now,
	}

	mock.ExpectExec(`UPDATE video_imports SET`).
		WithArgs(
			imp.ID,
			imp.Status,
			imp.VideoID,
			imp.ErrorMessage,
			imp.Progress,
			imp.Metadata,
			imp.FileSizeBytes,
			imp.DownloadedBytes,
			imp.StartedAt,
			imp.CompletedAt,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.Update(ctx, imp)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportRepository_UpdateStatus(t *testing.T) {
	tests := []struct {
		name        string
		importID    string
		status      domain.ImportStatus
		setupMock   func(sqlmock.Sqlmock)
		wantErr     bool
		expectError error
	}{
		{
			name:     "success",
			importID: "import-123",
			status:   domain.ImportStatusCompleted,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE video_imports SET status = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`)).
					WithArgs("import-123", domain.ImportStatusCompleted).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name:     "not found",
			importID: "nonexistent",
			status:   domain.ImportStatusCompleted,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE video_imports SET status = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`)).
					WithArgs("nonexistent", domain.ImportStatusCompleted).
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr:     true,
			expectError: domain.ErrImportNotFound,
		},
		{
			name:     "database error",
			importID: "import-123",
			status:   domain.ImportStatusFailed,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE video_imports SET status = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`)).
					WithArgs("import-123", domain.ImportStatusFailed).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			sqlxDB := sqlx.NewDb(db, "postgres")
			repo := NewImportRepository(sqlxDB)

			tt.setupMock(mock)

			err = repo.UpdateStatus(context.Background(), tt.importID, tt.status)

			if tt.wantErr {
				require.Error(t, err)
				if tt.expectError != nil {
					assert.ErrorIs(t, err, tt.expectError)
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestImportRepository_UpdateMetadata(t *testing.T) {
	tests := []struct {
		name        string
		importID    string
		metadata    []byte
		setupMock   func(sqlmock.Sqlmock)
		wantErr     bool
		expectError error
	}{
		{
			name:     "success",
			importID: "import-123",
			metadata: []byte(`{"title":"Test Video","duration":300}`),
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE video_imports SET metadata = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`)).
					WithArgs("import-123", []byte(`{"title":"Test Video","duration":300}`)).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name:     "not found",
			importID: "nonexistent",
			metadata: []byte(`{}`),
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE video_imports SET metadata = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`)).
					WithArgs("nonexistent", []byte(`{}`)).
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr:     true,
			expectError: domain.ErrImportNotFound,
		},
		{
			name:     "database error",
			importID: "import-123",
			metadata: []byte(`{}`),
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE video_imports SET metadata = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`)).
					WithArgs("import-123", []byte(`{}`)).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			sqlxDB := sqlx.NewDb(db, "postgres")
			repo := NewImportRepository(sqlxDB)

			tt.setupMock(mock)

			err = repo.UpdateMetadata(context.Background(), tt.importID, tt.metadata)

			if tt.wantErr {
				require.Error(t, err)
				if tt.expectError != nil {
					assert.ErrorIs(t, err, tt.expectError)
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestImportRepository_Delete(t *testing.T) {
	tests := []struct {
		name        string
		importID    string
		setupMock   func(sqlmock.Sqlmock)
		wantErr     bool
		expectError error
	}{
		{
			name:     "success",
			importID: "import-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_imports WHERE id = $1`)).
					WithArgs("import-123").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name:     "not found",
			importID: "nonexistent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_imports WHERE id = $1`)).
					WithArgs("nonexistent").
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr:     true,
			expectError: domain.ErrImportNotFound,
		},
		{
			name:     "database error",
			importID: "import-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_imports WHERE id = $1`)).
					WithArgs("import-123").
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			sqlxDB := sqlx.NewDb(db, "postgres")
			repo := NewImportRepository(sqlxDB)

			tt.setupMock(mock)

			err = repo.Delete(context.Background(), tt.importID)

			if tt.wantErr {
				require.Error(t, err)
				if tt.expectError != nil {
					assert.ErrorIs(t, err, tt.expectError)
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func strPtr(s string) *string {
	return &s
}
