package repository

import (
	"athena/internal/domain"
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoRepository_Search_SQLInjection_Fix(t *testing.T) {
	// Setup mock DB
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	repo := NewVideoRepository(sqlxDB)

	ctx := context.Background()
	now := time.Now()

	// Define search request that triggers the relevance sort path
	req := &domain.VideoSearchRequest{
		Query: "test_query",
		Sort:  "relevance",
		Limit: 20,
	}

	// The expected query should use a placeholder ($4) for the relevance sort parameter
	// instead of injecting the string literal.
	// We use a regex that looks for plainto_tsquery('english', $4) in the ORDER BY clause.
	// Note: The args are:
	// $1: query (for to_tsvector match)
	// $2: %query% (for title ilike)
	// $3: %query% (for description ilike)
	// $4: query (for ts_rank plainto_tsquery) - THIS IS WHAT WE WANT TO VERIFY
	// $5: limit
	// $6: offset

	// Using [\s\S]* to match any character including newlines
	expectedQueryRegex := `SELECT [\s\S]* FROM videos [\s\S]* ORDER BY ts_rank\(to_tsvector\('english', title \|\| ' ' \|\| description\), plainto_tsquery\('english', \$4\)\) DESC LIMIT \$5 OFFSET \$6`

	rows := sqlmock.NewRows([]string{
		"id", "thumbnail_id", "title", "description", "duration", "views",
		"privacy", "status", "upload_date", "user_id",
		"original_cid", "processed_cids", "thumbnail_cid",
		"tags", "category_id", "language", "file_size", "mime_type", "metadata",
		"created_at", "updated_at", "output_paths", "thumbnail_path", "preview_path",
	}).AddRow(
		"uuid-1", "thumb-1", "Title", "Desc", 120, 0,
		"public", "completed", now, "user-1",
		"cid1", nil, "cid2",
		pq.Array([]string{}), nil, "en", 1000, "video/mp4", nil,
		now, now, nil, "path/thumb", "path/preview",
	)

	// Also expectation for the Count query which runs first
	// The count query also uses the sort logic? No, count query doesn't need order by.
	// But let's check Search function.
	// countQuery := ...
	// if req.Query != "" { ... adds condition ... }
	// countQuery += searchCondition
	// ...
	// The Sort logic is ONLY applied to baseQuery (lines 485-496 in original file)
	// So count query uses args $1, $2, $3.

	// Count query expectation
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM videos`).
		WithArgs("test_query", "%test_query%", "%test_query%").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// Search query expectation
	mock.ExpectQuery(expectedQueryRegex).
		WithArgs(
			"test_query",      // $1
			"%test_query%",    // $2
			"%test_query%",    // $3
			"test_query",      // $4 - The crucial fix!
			20,                // $5
			0,                 // $6
		).
		WillReturnRows(rows)

	// Execute Search
	videos, total, err := repo.Search(ctx, req)

	// In the vulnerable version, this should fail because:
	// 1. The query won't match the regex (it will contain literal string).
	// 2. The arguments provided by code will be fewer than expected (or not match).

	// We assert that it succeeds to enforce the requirement of the fix.
	// If this fails, it means the code is still vulnerable (or broken).
	assert.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, videos, 1)

	assert.NoError(t, mock.ExpectationsWereMet())
}
