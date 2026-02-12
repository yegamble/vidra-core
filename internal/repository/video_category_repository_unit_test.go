package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupVideoCategoryMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newVideoCategoryRepo(t *testing.T) (*videoCategoryRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupVideoCategoryMockDB(t)
	repo := NewVideoCategoryRepository(db).(*videoCategoryRepository)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func sampleVideoCategory() domain.VideoCategory {
	now := time.Now()
	desc := "A category for gaming videos"
	icon := "gamepad"
	color := "#FF5733"
	createdBy := uuid.New()
	return domain.VideoCategory{
		ID:           uuid.New(),
		Name:         "Gaming",
		Slug:         "gaming",
		Description:  &desc,
		Icon:         &icon,
		Color:        &color,
		DisplayOrder: 1,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
		CreatedBy:    &createdBy,
	}
}

var videoCategoryColumns = []string{
	"id", "name", "slug", "description", "icon", "color",
	"display_order", "is_active", "created_at", "updated_at", "created_by",
}

func makeVideoCategoryRow(cat domain.VideoCategory) *sqlmock.Rows {
	return sqlmock.NewRows(videoCategoryColumns).AddRow(
		cat.ID, cat.Name, cat.Slug, cat.Description, cat.Icon, cat.Color,
		cat.DisplayOrder, cat.IsActive, cat.CreatedAt, cat.UpdatedAt, cat.CreatedBy,
	)
}

// ---------- Create ----------

func TestVideoCategoryRepository_Unit_Create(t *testing.T) {
	ctx := context.Background()

	insertQuery := `INSERT INTO video_categories (
			name, slug, description, icon, color, display_order,
			is_active, created_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		) RETURNING id, created_at, updated_at`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		cat := sampleVideoCategory()
		returnedID := uuid.New()
		now := time.Now()

		mock.ExpectQuery(regexp.QuoteMeta(insertQuery)).
			WithArgs(
				cat.Name, cat.Slug, cat.Description, cat.Icon, cat.Color,
				cat.DisplayOrder, cat.IsActive, cat.CreatedBy,
			).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
				AddRow(returnedID, now, now))

		err := repo.Create(ctx, &cat)
		require.NoError(t, err)
		assert.Equal(t, returnedID, cat.ID)
		assert.False(t, cat.CreatedAt.IsZero())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		cat := sampleVideoCategory()

		mock.ExpectQuery(regexp.QuoteMeta(insertQuery)).
			WithArgs(
				cat.Name, cat.Slug, cat.Description, cat.Icon, cat.Color,
				cat.DisplayOrder, cat.IsActive, cat.CreatedBy,
			).
			WillReturnError(errors.New("insert failed"))

		err := repo.Create(ctx, &cat)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create video category")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- GetByID ----------

func TestVideoCategoryRepository_Unit_GetByID(t *testing.T) {
	ctx := context.Background()

	selectQuery := `SELECT id, name, slug, description, icon, color, display_order,
		       is_active, created_at, updated_at, created_by
		FROM video_categories
		WHERE id = $1`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		cat := sampleVideoCategory()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs(cat.ID).
			WillReturnRows(makeVideoCategoryRow(cat))

		got, err := repo.GetByID(ctx, cat.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, cat.ID, got.ID)
		assert.Equal(t, cat.Name, got.Name)
		assert.Equal(t, cat.Slug, got.Slug)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		id := uuid.New()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs(id).
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetByID(ctx, id)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "video category not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		id := uuid.New()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs(id).
			WillReturnError(errors.New("db error"))

		got, err := repo.GetByID(ctx, id)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get video category")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- GetBySlug ----------

func TestVideoCategoryRepository_Unit_GetBySlug(t *testing.T) {
	ctx := context.Background()

	selectQuery := `SELECT id, name, slug, description, icon, color, display_order,
		       is_active, created_at, updated_at, created_by
		FROM video_categories
		WHERE slug = $1`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		cat := sampleVideoCategory()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs(cat.Slug).
			WillReturnRows(makeVideoCategoryRow(cat))

		got, err := repo.GetBySlug(ctx, cat.Slug)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, cat.Slug, got.Slug)
		assert.Equal(t, cat.Name, got.Name)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs("nonexistent").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetBySlug(ctx, "nonexistent")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "video category not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs("broken").
			WillReturnError(errors.New("db error"))

		got, err := repo.GetBySlug(ctx, "broken")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get video category")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- List ----------

func TestVideoCategoryRepository_Unit_List(t *testing.T) {
	ctx := context.Background()

	t.Run("success with defaults", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		cat := sampleVideoCategory()
		opts := domain.VideoCategoryListOptions{}

		// Default query: no filters, ORDER BY display_order ASC, no LIMIT/OFFSET
		mock.ExpectQuery(`SELECT id, name, slug, description, icon, color, display_order,(.|\n)*is_active, created_at, updated_at, created_by(.|\n)*FROM video_categories(.|\n)*WHERE 1=1(.|\n)*ORDER BY display_order ASC`).
			WillReturnRows(makeVideoCategoryRow(cat))

		got, err := repo.List(ctx, opts)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, cat.ID, got[0].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with active_only and limit and offset", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		cat := sampleVideoCategory()
		opts := domain.VideoCategoryListOptions{
			ActiveOnly: true,
			Limit:      10,
			Offset:     5,
		}

		mock.ExpectQuery(`(?s)SELECT.*FROM video_categories.*WHERE 1=1 AND is_active = \$1.*ORDER BY display_order ASC.*LIMIT \$2.*OFFSET \$3`).
			WithArgs(true, 10, 5).
			WillReturnRows(makeVideoCategoryRow(cat))

		got, err := repo.List(ctx, opts)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with custom order", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		opts := domain.VideoCategoryListOptions{
			OrderBy:  "name",
			OrderDir: "desc",
		}

		mock.ExpectQuery(`(?s)SELECT.*FROM video_categories.*WHERE 1=1.*ORDER BY name DESC`).
			WillReturnRows(sqlmock.NewRows(videoCategoryColumns))

		got, err := repo.List(ctx, opts)
		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		opts := domain.VideoCategoryListOptions{}

		mock.ExpectQuery(`(?s)SELECT.*FROM video_categories.*WHERE 1=1`).
			WillReturnError(errors.New("select failed"))

		got, err := repo.List(ctx, opts)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list video categories")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- Update ----------

func TestVideoCategoryRepository_Unit_Update(t *testing.T) {
	ctx := context.Background()

	t.Run("success with name update", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		id := uuid.New()
		newName := "Updated Gaming"
		updates := &domain.UpdateVideoCategoryRequest{
			Name: &newName,
		}

		mock.ExpectExec(`UPDATE video_categories SET name = \$1 WHERE id = \$2`).
			WithArgs(newName, id).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Update(ctx, id, updates)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with multiple fields", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		id := uuid.New()
		newName := "Updated Gaming"
		newSlug := "updated-gaming"
		updates := &domain.UpdateVideoCategoryRequest{
			Name: &newName,
			Slug: &newSlug,
		}

		mock.ExpectExec(`UPDATE video_categories SET name = \$1, slug = \$2 WHERE id = \$3`).
			WithArgs(newName, newSlug, id).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Update(ctx, id, updates)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("nothing to update returns nil", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		id := uuid.New()
		updates := &domain.UpdateVideoCategoryRequest{}

		err := repo.Update(ctx, id, updates)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		id := uuid.New()
		newName := "Broken"
		updates := &domain.UpdateVideoCategoryRequest{
			Name: &newName,
		}

		mock.ExpectExec(`UPDATE video_categories SET name = \$1 WHERE id = \$2`).
			WithArgs(newName, id).
			WillReturnError(errors.New("update failed"))

		err := repo.Update(ctx, id, updates)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update video category")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		id := uuid.New()
		newName := "Test"
		updates := &domain.UpdateVideoCategoryRequest{
			Name: &newName,
		}

		mock.ExpectExec(`UPDATE video_categories SET name = \$1 WHERE id = \$2`).
			WithArgs(newName, id).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.Update(ctx, id, updates)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		id := uuid.New()
		newName := "Missing"
		updates := &domain.UpdateVideoCategoryRequest{
			Name: &newName,
		}

		mock.ExpectExec(`UPDATE video_categories SET name = \$1 WHERE id = \$2`).
			WithArgs(newName, id).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Update(ctx, id, updates)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "video category not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- Delete ----------

func TestVideoCategoryRepository_Unit_Delete(t *testing.T) {
	ctx := context.Background()

	deleteQuery := `DELETE FROM video_categories WHERE id = $1`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		id := uuid.New()

		mock.ExpectExec(regexp.QuoteMeta(deleteQuery)).
			WithArgs(id).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Delete(ctx, id)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		id := uuid.New()

		mock.ExpectExec(regexp.QuoteMeta(deleteQuery)).
			WithArgs(id).
			WillReturnError(errors.New("delete failed"))

		err := repo.Delete(ctx, id)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete video category")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		id := uuid.New()

		mock.ExpectExec(regexp.QuoteMeta(deleteQuery)).
			WithArgs(id).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.Delete(ctx, id)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		id := uuid.New()

		mock.ExpectExec(regexp.QuoteMeta(deleteQuery)).
			WithArgs(id).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Delete(ctx, id)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "video category not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- GetDefault ----------

func TestVideoCategoryRepository_Unit_GetDefault(t *testing.T) {
	ctx := context.Background()

	selectQuery := `SELECT id, name, slug, description, icon, color, display_order,
		       is_active, created_at, updated_at, created_by
		FROM video_categories
		WHERE slug = 'other'
		LIMIT 1`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		cat := sampleVideoCategory()
		cat.Slug = "other"

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WillReturnRows(makeVideoCategoryRow(cat))

		got, err := repo.GetDefault(ctx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "other", got.Slug)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetDefault(ctx)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "default video category not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newVideoCategoryRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WillReturnError(errors.New("db error"))

		got, err := repo.GetDefault(ctx)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get default video category")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
