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

func setupFederationMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newFederationRepo(t *testing.T) (*FederationRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupFederationMockDB(t)
	repo := NewFederationRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func TestFederationRepository_Unit_Jobs(t *testing.T) {
	ctx := context.Background()

	t.Run("enqueue job success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		payload := map[string]string{"key": "value"}
		runAt := time.Now()

		mock.ExpectExec(`(?s)INSERT INTO federation_jobs`).
			WithArgs(sqlmock.AnyArg(), "deliver", sqlmock.AnyArg(), runAt).
			WillReturnResult(sqlmock.NewResult(0, 1))

		jobID, err := repo.EnqueueJob(ctx, "deliver", payload, runAt)
		require.NoError(t, err)
		assert.NotEmpty(t, jobID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get next job success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		jobID := uuid.NewString()
		now := time.Now()
		rows := sqlmock.NewRows([]string{
			"id", "job_type", "payload", "status", "attempts", "max_attempts", "next_attempt_at", "last_error", "created_at", "updated_at",
		}).AddRow(jobID, "deliver", []byte(`{"key":"value"}`), "pending", 0, 3, now, nil, now, now)

		mock.ExpectBegin()
		mock.ExpectQuery(`(?s)SELECT id, job_type, payload, status, attempts, max_attempts, next_attempt_at, last_error, created_at, updated_at FROM federation_jobs`).
			WillReturnRows(rows)
		mock.ExpectExec(`(?s)UPDATE federation_jobs SET status='processing', attempts = attempts \+ 1, updated_at = CURRENT_TIMESTAMP WHERE id`).
			WithArgs(jobID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		job, err := repo.GetNextJob(ctx)
		require.NoError(t, err)
		require.NotNil(t, job)
		assert.Equal(t, jobID, job.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("complete job success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		jobID := uuid.NewString()
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE federation_jobs SET status='completed', updated_at=CURRENT_TIMESTAMP WHERE id=$1`)).
			WithArgs(jobID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.CompleteJob(ctx, jobID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("reschedule job success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		jobID := uuid.NewString()
		mock.ExpectExec(`(?s)UPDATE federation_jobs\s+SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END`).
			WithArgs(jobID, "error message", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.RescheduleJob(ctx, jobID, "error message", 5*time.Minute)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete job success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		jobID := uuid.NewString()
		mock.ExpectExec(`(?s)DELETE FROM federation_jobs WHERE id\s*=\s*\$1`).
			WithArgs(jobID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteJob(ctx, jobID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete job not found", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		jobID := uuid.NewString()
		mock.ExpectExec(`(?s)DELETE FROM federation_jobs WHERE id\s*=\s*\$1`).
			WithArgs(jobID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.DeleteJob(ctx, jobID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete job error", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		jobID := uuid.NewString()
		mock.ExpectExec(`(?s)DELETE FROM federation_jobs WHERE id\s*=\s*\$1`).
			WithArgs(jobID).
			WillReturnError(errors.New("delete failed"))

		err := repo.DeleteJob(ctx, jobID)
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("list jobs success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		now := time.Now()
		rows := sqlmock.NewRows([]string{
			"id", "job_type", "payload", "status", "attempts", "max_attempts", "next_attempt_at", "last_error", "created_at", "updated_at",
		}).AddRow(uuid.NewString(), "deliver", []byte(`{}`), "pending", 0, 3, now, nil, now, now)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM federation_jobs WHERE status = $1`)).
			WithArgs("pending").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery(`(?s)SELECT id, job_type, payload, status, attempts, max_attempts, next_attempt_at, last_error, created_at, updated_at\s+FROM federation_jobs WHERE status`).
			WithArgs("pending", 10, 0).
			WillReturnRows(rows)

		jobs, total, err := repo.ListJobs(ctx, "pending", 10, 0)
		require.NoError(t, err)
		require.Len(t, jobs, 1)
		assert.Equal(t, 1, total)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestFederationRepository_Unit_Posts(t *testing.T) {
	ctx := context.Background()

	t.Run("upsert post success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		text := "Post text"
		handle := "user.bsky.social"
		cid := "bafyreib..."
		post := &domain.FederatedPost{
			ActorDID:    "did:plc:abc123",
			ActorHandle: &handle,
			URI:         "at://did:plc:abc123/app.bsky.feed.post/xyz",
			CID:         &cid,
			Text:        &text,
		}

		mock.ExpectExec(`(?s)INSERT INTO federated_posts`).
			WithArgs(
				post.ActorDID,
				post.ActorHandle,
				post.URI,
				post.CID,
				post.Text,
				post.CreatedAt,
				post.IndexedAt,
				post.EmbedType,
				post.EmbedURL,
				post.EmbedTitle,
				post.EmbedDescription,
				sqlmock.AnyArg(),
				sqlmock.AnyArg(),
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpsertPost(ctx, post)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("list timeline success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		now := time.Now()
		text := "post text"
		handle := "user.bsky.social"
		rows := sqlmock.NewRows([]string{
			"id", "actor_did", "actor_handle", "uri", "cid", "text", "created_at", "indexed_at",
			"embed_type", "embed_url", "embed_title", "embed_description", "labels", "raw", "inserted_at", "updated_at",
		}).AddRow(
			uuid.NewString(), "did:plc:abc", &handle, "at://uri", nil, &text, &now, &now,
			nil, nil, nil, nil, []byte(`[]`), []byte(`{}`), now, now,
		)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM federated_posts`)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery(`(?s)SELECT .* FROM federated_posts`).
			WithArgs(10, 0).
			WillReturnRows(rows)

		posts, total, err := repo.ListTimeline(ctx, 10, 0)
		require.NoError(t, err)
		require.Len(t, posts, 1)
		assert.Equal(t, 1, total)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get post by id success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		postID := uuid.NewString()
		uri := "at://did:plc:abc/app.bsky.feed.post/xyz"
		text := "post text"
		handle := "user.bsky.social"
		now := time.Now()
		rows := sqlmock.NewRows([]string{
			"id", "actor_did", "actor_handle", "uri", "cid", "text", "created_at", "indexed_at",
			"embed_type", "embed_url", "embed_title", "embed_description", "labels", "raw",
			"inserted_at", "updated_at", "content_hash", "duplicate_of", "is_canonical", "version_number",
		}).AddRow(
			postID, "did:plc:abc", &handle, uri, nil, &text, &now, &now,
			nil, nil, nil, nil, []byte(`[]`), []byte(`{}`),
			now, now, nil, nil, true, 1,
		)

		mock.ExpectQuery(`(?s)SELECT .* FROM federated_posts WHERE id`).
			WithArgs(postID).
			WillReturnRows(rows)

		post, err := repo.GetPost(ctx, postID)
		require.NoError(t, err)
		require.NotNil(t, post)
		assert.Equal(t, postID, post.ID)
		assert.Equal(t, uri, post.URI)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestFederationRepository_Unit_Actors(t *testing.T) {
	ctx := context.Background()
	actor := "user@example.com"

	t.Run("upsert actor success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)INSERT INTO federation_actors`).
			WithArgs(actor, true, 60).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpsertActor(ctx, actor, true, 60)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("list actors success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		now := time.Now()
		rows := sqlmock.NewRows([]string{
			"actor", "enabled", "cursor", "next_at", "attempts", "rate_limit_seconds", "last_error", "created_at", "updated_at",
		}).AddRow(actor, true, "cursor1", now, 0, 60, nil, now, now)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM federation_actors`)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery(`(?s)SELECT actor, enabled, cursor, next_at, attempts, rate_limit_seconds, last_error, created_at, updated_at\s+FROM federation_actors`).
			WithArgs(10, 0).
			WillReturnRows(rows)

		actors, total, err := repo.ListActors(ctx, 10, 0)
		require.NoError(t, err)
		require.Len(t, actors, 1)
		assert.Equal(t, 1, total)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get actor success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		now := time.Now()
		rows := sqlmock.NewRows([]string{
			"actor", "enabled", "cursor", "next_at", "attempts", "rate_limit_seconds", "last_error", "created_at", "updated_at",
		}).AddRow(actor, true, "cursor1", now, 0, 60, nil, now, now)

		mock.ExpectQuery(`(?s)SELECT actor, enabled, cursor, next_at, attempts, rate_limit_seconds, last_error, created_at, updated_at FROM federation_actors WHERE actor`).
			WithArgs(actor).
			WillReturnRows(rows)

		a, err := repo.GetActor(ctx, actor)
		require.NoError(t, err)
		require.NotNil(t, a)
		assert.Equal(t, actor, a.Actor)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update actor success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		enabled := true
		rateLimitSeconds := 120
		cursor := "new_cursor"
		nextAt := time.Now()
		attempts := 3

		mock.ExpectExec(`(?s)UPDATE federation_actors SET`).
			WithArgs(enabled, rateLimitSeconds, cursor, nextAt, attempts, actor).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateActor(ctx, actor, &enabled, &rateLimitSeconds, &cursor, &nextAt, &attempts)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete actor success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)DELETE FROM federation_actors WHERE actor\s*=\s*\$1`).
			WithArgs(actor).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteActor(ctx, actor)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete actor not found", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)DELETE FROM federation_actors WHERE actor\s*=\s*\$1`).
			WithArgs(actor).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.DeleteActor(ctx, actor)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete actor error", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)DELETE FROM federation_actors WHERE actor\s*=\s*\$1`).
			WithArgs(actor).
			WillReturnError(errors.New("delete failed"))

		err := repo.DeleteActor(ctx, actor)
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("list enabled actors success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{"actor"}).
			AddRow("user1@example.com").
			AddRow("user2@example.com")

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT actor FROM federation_actors WHERE enabled = true ORDER BY actor ASC`)).
			WillReturnRows(rows)

		actors, err := repo.ListEnabledActors(ctx)
		require.NoError(t, err)
		require.Len(t, actors, 2)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get actor state success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{"cursor", "next_at", "attempts", "rate_limit_seconds"}).
			AddRow("cursor1", time.Now(), 2, 60)

		mock.ExpectQuery(`(?s)SELECT cursor, next_at, attempts, rate_limit_seconds FROM federation_actors WHERE actor\s*=\s*\$1`).
			WithArgs(actor).
			WillReturnRows(rows)

		cursor, nextAt, attempts, rateLimitSeconds, err := repo.GetActorState(ctx, actor)
		require.NoError(t, err)
		assert.Equal(t, "cursor1", cursor)
		assert.True(t, nextAt.Valid)
		assert.Equal(t, 2, attempts)
		assert.Equal(t, 60, rateLimitSeconds)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("set actor cursor success", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE federation_actors SET cursor=$2 WHERE actor=$1`)).
			WithArgs(actor, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.SetActorCursor(ctx, actor, "new_cursor")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestFederationRepository_Unit_Errors(t *testing.T) {
	ctx := context.Background()

	t.Run("enqueue job failure", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)INSERT INTO federation_jobs`).
			WillReturnError(errors.New("insert failed"))

		jobID, err := repo.EnqueueJob(ctx, "deliver", nil, time.Now())
		assert.Empty(t, jobID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "enqueue job")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get next job no jobs available", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(`(?s)SELECT id, job_type, payload, status, attempts, max_attempts, next_attempt_at, last_error, created_at, updated_at FROM federation_jobs`).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectCommit()

		job, err := repo.GetNextJob(ctx)
		require.Nil(t, job)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("upsert post failure", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		post := &domain.FederatedPost{URI: "at://test"}
		mock.ExpectExec(`(?s)INSERT INTO federated_posts`).
			WillReturnError(errors.New("insert failed"))

		err := repo.UpsertPost(ctx, post)
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get post by id not found", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		postID := uuid.NewString()
		mock.ExpectQuery(`(?s)SELECT .* FROM federated_posts WHERE id`).
			WithArgs(postID).
			WillReturnError(sql.ErrNoRows)

		post, err := repo.GetPost(ctx, postID)
		require.Nil(t, post)
		require.Error(t, err)
		var domainErr domain.DomainError
		require.True(t, errors.As(err, &domainErr))
		assert.Equal(t, "NOT_FOUND", domainErr.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get actor not found", func(t *testing.T) {
		repo, mock, cleanup := newFederationRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT actor, enabled, cursor, next_at, attempts, rate_limit_seconds, last_error, created_at, updated_at FROM federation_actors WHERE actor`).
			WithArgs("nonexistent@example.com").
			WillReturnError(sql.ErrNoRows)

		a, err := repo.GetActor(ctx, "nonexistent@example.com")
		require.Nil(t, a)
		require.Error(t, err)
		var domainErr domain.DomainError
		require.True(t, errors.As(err, &domainErr))
		assert.Equal(t, "NOT_FOUND", domainErr.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestFederationRepository_GetJob(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()
		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationRepository(db)
		ctx := context.Background()

		now := time.Now()
		rows := sqlmock.NewRows([]string{"id", "job_type", "payload", "status", "attempts", "max_attempts", "next_attempt_at", "last_error", "created_at", "updated_at"}).
			AddRow("job-123", "deliver", []byte(`{}`), "pending", 0, 5, now, nil, now, now)
		mock.ExpectQuery(`SELECT id, job_type, payload, status, attempts, max_attempts, next_attempt_at, last_error, created_at, updated_at FROM federation_jobs WHERE id`).
			WithArgs("job-123").
			WillReturnRows(rows)

		job, err := repo.GetJob(ctx, "job-123")
		require.NoError(t, err)
		assert.Equal(t, "job-123", job.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()
		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationRepository(db)
		ctx := context.Background()

		mock.ExpectQuery(`SELECT id, job_type, payload, status, attempts, max_attempts, next_attempt_at, last_error, created_at, updated_at FROM federation_jobs WHERE id`).
			WithArgs("nonexistent").
			WillReturnError(sql.ErrNoRows)

		job, err := repo.GetJob(ctx, "nonexistent")
		require.Nil(t, job)
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestFederationRepository_RetryJob(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()
	db := sqlx.NewDb(mockDB, "sqlmock")
	repo := NewFederationRepository(db)
	ctx := context.Background()

	when := time.Now().Add(time.Hour)
	mock.ExpectExec(`UPDATE federation_jobs SET status='pending', next_attempt_at`).
		WithArgs("job-123", when).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.RetryJob(ctx, "job-123", when)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFederationRepository_GetActorStateSimple(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()
	db := sqlx.NewDb(mockDB, "sqlmock")
	repo := NewFederationRepository(db)
	ctx := context.Background()

	now := time.Now()
	rows := sqlmock.NewRows([]string{"cursor", "next_at", "attempts", "rate_limit_seconds"}).
		AddRow("cursor-123", now, 2, 60)
	mock.ExpectQuery(`SELECT cursor, next_at, attempts, rate_limit_seconds FROM federation_actors WHERE actor`).
		WithArgs("did:plc:test").
		WillReturnRows(rows)

	cursor, nextAt, attempts, rateLimitSeconds, err := repo.GetActorStateSimple(ctx, "did:plc:test")
	require.NoError(t, err)
	assert.Equal(t, "cursor-123", cursor)
	assert.NotNil(t, nextAt)
	assert.Equal(t, 2, attempts)
	assert.Equal(t, 60, rateLimitSeconds)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFederationRepository_SetActorNextAt(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()
	db := sqlx.NewDb(mockDB, "sqlmock")
	repo := NewFederationRepository(db)
	ctx := context.Background()

	nextAt := time.Now().Add(time.Hour)
	mock.ExpectExec(`UPDATE federation_actors SET next_at`).
		WithArgs("did:plc:test", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.SetActorNextAt(ctx, "did:plc:test", nextAt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFederationRepository_SetActorAttempts(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()
	db := sqlx.NewDb(mockDB, "sqlmock")
	repo := NewFederationRepository(db)
	ctx := context.Background()

	mock.ExpectExec(`UPDATE federation_actors SET attempts`).
		WithArgs("did:plc:test", 3).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.SetActorAttempts(ctx, "did:plc:test", 3)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFederationRepository_GetPostByContentHash(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()
	db := sqlx.NewDb(mockDB, "sqlmock")
	repo := NewFederationRepository(db)
	ctx := context.Background()

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "actor_did", "actor_handle", "uri", "cid", "text", "created_at", "indexed_at", "embed_type", "embed_url", "embed_title", "embed_description", "labels", "raw", "inserted_at", "updated_at", "content_hash", "duplicate_of", "is_canonical", "version_number"}).
		AddRow("post-123", "did:plc:test", "test@example.com", "at://uri", "cid-123", "test post", now, now, nil, nil, nil, nil, []byte(`[]`), []byte(`{}`), now, now, "hash-123", nil, true, 1)
	mock.ExpectQuery(`SELECT id, actor_did, actor_handle, uri, cid, text, created_at, indexed_at`).
		WithArgs("hash-123").
		WillReturnRows(rows)

	post, err := repo.GetPostByContentHash(ctx, "hash-123")
	require.NoError(t, err)
	assert.NotNil(t, post)
	assert.Equal(t, "post-123", post.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFederationRepository_UpdatePostCanonical(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()
	db := sqlx.NewDb(mockDB, "sqlmock")
	repo := NewFederationRepository(db)
	ctx := context.Background()

	mock.ExpectExec(`UPDATE federated_posts SET is_canonical`).
		WithArgs("post-123", true).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.UpdatePostCanonical(ctx, "post-123", true)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFederationRepository_UpdatePostDuplicateOf(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()
	db := sqlx.NewDb(mockDB, "sqlmock")
	repo := NewFederationRepository(db)
	ctx := context.Background()

	duplicateOf := "post-456"
	mock.ExpectExec(`UPDATE federated_posts SET duplicate_of`).
		WithArgs("post-123", &duplicateOf).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.UpdatePostDuplicateOf(ctx, "post-123", &duplicateOf)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFederationRepository_GetPostDuplicates(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()
	db := sqlx.NewDb(mockDB, "sqlmock")
	repo := NewFederationRepository(db)
	ctx := context.Background()

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "actor_did", "actor_handle", "uri", "cid", "text", "created_at", "indexed_at", "embed_type", "embed_url", "embed_title", "embed_description", "labels", "raw", "inserted_at", "updated_at", "content_hash", "duplicate_of", "is_canonical", "version_number"}).
		AddRow("post-789", "did:plc:test", "test@example.com", "at://uri2", "cid-789", "duplicate post", now, now, nil, nil, nil, nil, []byte(`[]`), []byte(`{}`), now, now, "hash-123", "post-123", false, 2)
	mock.ExpectQuery(`SELECT id, actor_did, actor_handle, uri, cid, text, created_at, indexed_at`).
		WithArgs("post-123").
		WillReturnRows(rows)

	posts, err := repo.GetPostDuplicates(ctx, "post-123")
	require.NoError(t, err)
	assert.Len(t, posts, 1)
	assert.Equal(t, "post-789", posts[0].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestJsonRawToDBValue(t *testing.T) {
	t.Run("empty raw returns nil", func(t *testing.T) {
		result := jsonRawToDBValue(nil)
		assert.Nil(t, result)

		result = jsonRawToDBValue([]byte{})
		assert.Nil(t, result)
	})

	t.Run("valid JSON returns string", func(t *testing.T) {
		raw := []byte(`{"key":"value"}`)
		result := jsonRawToDBValue(raw)
		assert.Equal(t, `{"key":"value"}`, result)
	})

	t.Run("invalid JSON gets marshaled", func(t *testing.T) {
		raw := []byte(`invalid json`)
		result := jsonRawToDBValue(raw)
		assert.NotNil(t, result)
		assert.Contains(t, result.(string), "invalid json")
	})
}
