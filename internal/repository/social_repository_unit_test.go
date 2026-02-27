package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
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

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// anyValueConverter satisfies driver.ValueConverter to allow any type
// through sqlmock (needed for []string args like pq arrays).
type anyValueConverter struct{}

func (anyValueConverter) ConvertValue(v interface{}) (driver.Value, error) {
	return v, nil
}

func setupSocialMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func setupSocialMockDBWithAnyValues(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New(sqlmock.ValueConverterOption(anyValueConverter{}))
	require.NoError(t, err)
	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newSocialRepo(t *testing.T) (*SocialRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock := setupSocialMockDB(t)
	repo := NewSocialRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func sampleActor() domain.ATProtoActor {
	now := time.Now().Truncate(time.Microsecond)
	displayName := "Alice"
	bio := "Hello world"
	avatarURL := "https://example.com/avatar.png"
	bannerURL := "https://example.com/banner.png"
	localUserID := "local-user-123"
	return domain.ATProtoActor{
		DID:         "did:plc:abc123",
		Handle:      "alice.bsky.social",
		DisplayName: &displayName,
		Bio:         &bio,
		AvatarURL:   &avatarURL,
		BannerURL:   &bannerURL,
		CreatedAt:   now,
		UpdatedAt:   now,
		IndexedAt:   now,
		Labels:      json.RawMessage(`["test"]`),
		LocalUserID: &localUserID,
	}
}

func actorColumns() []string {
	return []string{
		"did", "handle", "display_name", "bio", "avatar_url", "banner_url",
		"created_at", "updated_at", "indexed_at", "labels", "local_user_id",
	}
}

func actorRow(a domain.ATProtoActor) *sqlmock.Rows {
	return sqlmock.NewRows(actorColumns()).AddRow(
		a.DID, a.Handle, a.DisplayName, a.Bio,
		a.AvatarURL, a.BannerURL, a.CreatedAt,
		a.UpdatedAt, a.IndexedAt, a.Labels, a.LocalUserID,
	)
}

func sampleFollow() domain.Follow {
	now := time.Now().Truncate(time.Microsecond)
	cid := "bafyreiabc"
	return domain.Follow{
		ID:           "1",
		FollowerDID:  "did:plc:follower",
		FollowingDID: "did:plc:following",
		URI:          "at://did:plc:follower/app.bsky.graph.follow/abc",
		CID:          &cid,
		CreatedAt:    now,
		Raw:          json.RawMessage(`{}`),
	}
}

func followColumns() []string {
	return []string{
		"id", "follower_did", "following_did", "uri", "cid",
		"created_at", "revoked_at", "raw",
	}
}

func followRow(f domain.Follow) *sqlmock.Rows {
	return sqlmock.NewRows(followColumns()).AddRow(
		f.ID, f.FollowerDID, f.FollowingDID, f.URI, f.CID,
		f.CreatedAt, f.RevokedAt, f.Raw,
	)
}

func sampleLike() domain.Like {
	now := time.Now().Truncate(time.Microsecond)
	cid := "bafyreiabc"
	subjectCID := "bafyreisubject"
	videoID := "video-123"
	return domain.Like{
		ID:         "1",
		ActorDID:   "did:plc:liker",
		SubjectURI: "at://did:plc:author/app.bsky.feed.post/xyz",
		SubjectCID: &subjectCID,
		URI:        "at://did:plc:liker/app.bsky.feed.like/abc",
		CID:        &cid,
		CreatedAt:  now,
		VideoID:    &videoID,
		PostID:     nil,
		Raw:        json.RawMessage(`{}`),
	}
}

func likeColumns() []string {
	return []string{
		"id", "actor_did", "subject_uri", "subject_cid", "uri", "cid",
		"created_at", "video_id", "post_id", "raw",
	}
}

func likeRow(l domain.Like) *sqlmock.Rows {
	return sqlmock.NewRows(likeColumns()).AddRow(
		l.ID, l.ActorDID, l.SubjectURI, l.SubjectCID, l.URI, l.CID,
		l.CreatedAt, l.VideoID, l.PostID, l.Raw,
	)
}

func sampleComment() domain.SocialComment {
	now := time.Now().Truncate(time.Microsecond)
	cid := "bafyreicmt"
	actorHandle := "alice.bsky.social"
	parentURI := "at://did:plc:author/app.bsky.feed.post/parent"
	parentCID := "bafyreiparent"
	rootCID := "bafyreiroot"
	videoID := "video-456"
	return domain.SocialComment{
		ID:          "1",
		ActorDID:    "did:plc:commenter",
		ActorHandle: &actorHandle,
		URI:         "at://did:plc:commenter/app.bsky.feed.post/comment1",
		CID:         &cid,
		Text:        "Great video!",
		ParentURI:   &parentURI,
		ParentCID:   &parentCID,
		RootURI:     "at://did:plc:author/app.bsky.feed.post/root",
		RootCID:     &rootCID,
		CreatedAt:   now,
		IndexedAt:   now,
		VideoID:     &videoID,
		PostID:      nil,
		Labels:      json.RawMessage(`[]`),
		Blocked:     false,
		Raw:         json.RawMessage(`{}`),
	}
}

func commentColumns() []string {
	return []string{
		"id", "actor_did", "actor_handle", "uri", "cid", "text",
		"parent_uri", "parent_cid", "root_uri", "root_cid",
		"created_at", "indexed_at", "video_id", "post_id",
		"labels", "blocked", "raw",
	}
}

func commentRow(c domain.SocialComment) *sqlmock.Rows {
	return sqlmock.NewRows(commentColumns()).AddRow(
		c.ID, c.ActorDID, c.ActorHandle, c.URI, c.CID, c.Text,
		c.ParentURI, c.ParentCID, c.RootURI, c.RootCID,
		c.CreatedAt, c.IndexedAt, c.VideoID, c.PostID,
		c.Labels, c.Blocked, c.Raw,
	)
}

func sampleModerationLabel() domain.ModerationLabel {
	now := time.Now().Truncate(time.Microsecond)
	reason := "spammy behaviour"
	uri := "at://did:plc:spammer/app.bsky.feed.post/spam1"
	expires := now.Add(24 * time.Hour)
	return domain.ModerationLabel{
		ID:        "1",
		ActorDID:  "did:plc:spammer",
		LabelType: "spam",
		Reason:    &reason,
		AppliedBy: "did:plc:admin",
		URI:       &uri,
		CreatedAt: now,
		ExpiresAt: &expires,
		Raw:       json.RawMessage(`{}`),
	}
}

func modLabelColumns() []string {
	return []string{
		"id", "actor_did", "label_type", "reason", "applied_by",
		"uri", "created_at", "expires_at", "raw",
	}
}

func modLabelRow(l domain.ModerationLabel) *sqlmock.Rows {
	return sqlmock.NewRows(modLabelColumns()).AddRow(
		l.ID, l.ActorDID, l.LabelType, l.Reason, l.AppliedBy,
		l.URI, l.CreatedAt, l.ExpiresAt, l.Raw,
	)
}

// ---------------------------------------------------------------------------
// UpsertActor
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_UpsertActor(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		actor := sampleActor()
		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO atproto_actors`)).
			WithArgs(
				actor.DID, actor.Handle, actor.DisplayName, actor.Bio,
				actor.AvatarURL, actor.BannerURL, actor.CreatedAt,
				actor.UpdatedAt, actor.IndexedAt, actor.Labels, actor.LocalUserID,
			).
			WillReturnRows(actorRow(actor))

		err := repo.UpsertActor(ctx, &actor)
		require.NoError(t, err)
		assert.Equal(t, "did:plc:abc123", actor.DID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		actor := sampleActor()
		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO atproto_actors`)).
			WithArgs(
				actor.DID, actor.Handle, actor.DisplayName, actor.Bio,
				actor.AvatarURL, actor.BannerURL, actor.CreatedAt,
				actor.UpdatedAt, actor.IndexedAt, actor.Labels, actor.LocalUserID,
			).
			WillReturnError(errors.New("insert failed"))

		err := repo.UpsertActor(ctx, &actor)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetActorByDID
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_GetActorByDID(t *testing.T) {
	ctx := context.Background()
	actor := sampleActor()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_actors WHERE did = $1`)).
			WithArgs(actor.DID).
			WillReturnRows(actorRow(actor))

		got, err := repo.GetActorByDID(ctx, actor.DID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, actor.DID, got.DID)
		assert.Equal(t, actor.Handle, got.Handle)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_actors WHERE did = $1`)).
			WithArgs("did:plc:missing").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetActorByDID(ctx, "did:plc:missing")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "actor not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_actors WHERE did = $1`)).
			WithArgs(actor.DID).
			WillReturnError(errors.New("connection refused"))

		got, err := repo.GetActorByDID(ctx, actor.DID)
		require.NotNil(t, got) // returns &actor with zero value
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetActorByHandle
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_GetActorByHandle(t *testing.T) {
	ctx := context.Background()
	actor := sampleActor()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_actors WHERE handle = $1`)).
			WithArgs(actor.Handle).
			WillReturnRows(actorRow(actor))

		got, err := repo.GetActorByHandle(ctx, actor.Handle)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, actor.Handle, got.Handle)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_actors WHERE handle = $1`)).
			WithArgs("missing.bsky.social").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetActorByHandle(ctx, "missing.bsky.social")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "actor not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_actors WHERE handle = $1`)).
			WithArgs(actor.Handle).
			WillReturnError(errors.New("timeout"))

		got, err := repo.GetActorByHandle(ctx, actor.Handle)
		require.NotNil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "timeout")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// CreateFollow
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_CreateFollow(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		follow := sampleFollow()
		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO atproto_follows`)).
			WithArgs(
				follow.FollowerDID, follow.FollowingDID,
				follow.URI, follow.CID, follow.CreatedAt, follow.Raw,
			).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("42"))

		err := repo.CreateFollow(ctx, &follow)
		require.NoError(t, err)
		assert.Equal(t, "42", follow.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		follow := sampleFollow()
		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO atproto_follows`)).
			WithArgs(
				follow.FollowerDID, follow.FollowingDID,
				follow.URI, follow.CID, follow.CreatedAt, follow.Raw,
			).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateFollow(ctx, &follow)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// RevokeFollow
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_RevokeFollow(t *testing.T) {
	ctx := context.Background()
	uri := "at://did:plc:follower/app.bsky.graph.follow/abc"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE atproto_follows`)).
			WithArgs(uri).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.RevokeFollow(ctx, uri)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE atproto_follows`)).
			WithArgs(uri).
			WillReturnError(errors.New("exec failed"))

		err := repo.RevokeFollow(ctx, uri)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exec failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetFollowers
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_GetFollowers(t *testing.T) {
	ctx := context.Background()
	did := "did:plc:following"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		f := sampleFollow()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT f.*, a.handle as follower_handle`)).
			WithArgs(did, 10, 0).
			WillReturnRows(followRow(f))

		got, err := repo.GetFollowers(ctx, did, 10, 0)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, f.URI, got[0].URI)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT f.*, a.handle as follower_handle`)).
			WithArgs(did, 10, 0).
			WillReturnRows(sqlmock.NewRows(followColumns()))

		got, err := repo.GetFollowers(ctx, did, 10, 0)
		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT f.*, a.handle as follower_handle`)).
			WithArgs(did, 10, 0).
			WillReturnError(errors.New("query failed"))

		got, err := repo.GetFollowers(ctx, did, 10, 0)
		require.Error(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetFollowing
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_GetFollowing(t *testing.T) {
	ctx := context.Background()
	did := "did:plc:follower"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		f := sampleFollow()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT f.*, a.handle as following_handle`)).
			WithArgs(did, 10, 0).
			WillReturnRows(followRow(f))

		got, err := repo.GetFollowing(ctx, did, 10, 0)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, f.URI, got[0].URI)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT f.*, a.handle as following_handle`)).
			WithArgs(did, 10, 0).
			WillReturnError(errors.New("select failed"))

		got, err := repo.GetFollowing(ctx, did, 10, 0)
		require.Error(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetFollow
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_GetFollow(t *testing.T) {
	ctx := context.Background()
	followerDID := "did:plc:follower"
	followingDID := "did:plc:following"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		f := sampleFollow()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_follows`)).
			WithArgs(followerDID, followingDID).
			WillReturnRows(followRow(f))

		got, err := repo.GetFollow(ctx, followerDID, followingDID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, f.URI, got.URI)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_follows`)).
			WithArgs(followerDID, followingDID).
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetFollow(ctx, followerDID, followingDID)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "follow not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_follows`)).
			WithArgs(followerDID, followingDID).
			WillReturnError(errors.New("connection failed"))

		got, err := repo.GetFollow(ctx, followerDID, followingDID)
		require.Error(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// IsFollowing
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_IsFollowing(t *testing.T) {
	ctx := context.Background()

	t.Run("true", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT EXISTS`)).
			WithArgs("did:plc:a", "did:plc:b").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		ok, err := repo.IsFollowing(ctx, "did:plc:a", "did:plc:b")
		require.NoError(t, err)
		assert.True(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("false", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT EXISTS`)).
			WithArgs("did:plc:a", "did:plc:b").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		ok, err := repo.IsFollowing(ctx, "did:plc:a", "did:plc:b")
		require.NoError(t, err)
		assert.False(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT EXISTS`)).
			WithArgs("did:plc:a", "did:plc:b").
			WillReturnError(errors.New("query failed"))

		ok, err := repo.IsFollowing(ctx, "did:plc:a", "did:plc:b")
		require.Error(t, err)
		assert.False(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// CreateLike
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_CreateLike(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		like := sampleLike()
		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO atproto_likes`)).
			WithArgs(
				like.ActorDID, like.SubjectURI, like.SubjectCID,
				like.URI, like.CID, like.CreatedAt,
				like.VideoID, like.PostID, like.Raw,
			).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("99"))

		err := repo.CreateLike(ctx, &like)
		require.NoError(t, err)
		assert.Equal(t, "99", like.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		like := sampleLike()
		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO atproto_likes`)).
			WithArgs(
				like.ActorDID, like.SubjectURI, like.SubjectCID,
				like.URI, like.CID, like.CreatedAt,
				like.VideoID, like.PostID, like.Raw,
			).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateLike(ctx, &like)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// DeleteLike
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_DeleteLike(t *testing.T) {
	ctx := context.Background()
	uri := "at://did:plc:liker/app.bsky.feed.like/abc"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM atproto_likes WHERE uri = $1`)).
			WithArgs(uri).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteLike(ctx, uri)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM atproto_likes WHERE uri = $1`)).
			WithArgs(uri).
			WillReturnError(errors.New("delete failed"))

		err := repo.DeleteLike(ctx, uri)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetLikes
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_GetLikes(t *testing.T) {
	ctx := context.Background()
	subjectURI := "at://did:plc:author/app.bsky.feed.post/xyz"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		l := sampleLike()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT l.*, a.handle as actor_handle`)).
			WithArgs(subjectURI, 10, 0).
			WillReturnRows(likeRow(l))

		got, err := repo.GetLikes(ctx, subjectURI, 10, 0)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, l.URI, got[0].URI)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT l.*, a.handle as actor_handle`)).
			WithArgs(subjectURI, 10, 0).
			WillReturnRows(sqlmock.NewRows(likeColumns()))

		got, err := repo.GetLikes(ctx, subjectURI, 10, 0)
		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT l.*, a.handle as actor_handle`)).
			WithArgs(subjectURI, 10, 0).
			WillReturnError(errors.New("query failed"))

		got, err := repo.GetLikes(ctx, subjectURI, 10, 0)
		require.Error(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// HasLiked
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_HasLiked(t *testing.T) {
	ctx := context.Background()

	t.Run("true", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT EXISTS`)).
			WithArgs("did:plc:liker", "at://subject").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		ok, err := repo.HasLiked(ctx, "did:plc:liker", "at://subject")
		require.NoError(t, err)
		assert.True(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("false", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT EXISTS`)).
			WithArgs("did:plc:liker", "at://subject").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		ok, err := repo.HasLiked(ctx, "did:plc:liker", "at://subject")
		require.NoError(t, err)
		assert.False(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT EXISTS`)).
			WithArgs("did:plc:liker", "at://subject").
			WillReturnError(errors.New("query failed"))

		ok, err := repo.HasLiked(ctx, "did:plc:liker", "at://subject")
		require.Error(t, err)
		assert.False(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// CreateComment
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_CreateComment(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		c := sampleComment()
		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO atproto_comments`)).
			WithArgs(
				c.ActorDID, c.ActorHandle, c.URI, c.CID,
				c.Text, c.ParentURI, c.ParentCID,
				c.RootURI, c.RootCID, c.CreatedAt,
				c.IndexedAt, c.VideoID, c.PostID,
				c.Labels, c.Blocked, c.Raw,
			).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("77"))

		err := repo.CreateComment(ctx, &c)
		require.NoError(t, err)
		assert.Equal(t, "77", c.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		c := sampleComment()
		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO atproto_comments`)).
			WithArgs(
				c.ActorDID, c.ActorHandle, c.URI, c.CID,
				c.Text, c.ParentURI, c.ParentCID,
				c.RootURI, c.RootCID, c.CreatedAt,
				c.IndexedAt, c.VideoID, c.PostID,
				c.Labels, c.Blocked, c.Raw,
			).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateComment(ctx, &c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// DeleteComment
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_DeleteComment(t *testing.T) {
	ctx := context.Background()
	uri := "at://did:plc:commenter/app.bsky.feed.post/comment1"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM atproto_comments WHERE uri = $1`)).
			WithArgs(uri).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteComment(ctx, uri)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM atproto_comments WHERE uri = $1`)).
			WithArgs(uri).
			WillReturnError(errors.New("delete failed"))

		err := repo.DeleteComment(ctx, uri)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetComments
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_GetComments(t *testing.T) {
	ctx := context.Background()
	rootURI := "at://did:plc:author/app.bsky.feed.post/root"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		c := sampleComment()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT c.*, a.handle as actor_handle, a.display_name`)).
			WithArgs(rootURI, 10, 0).
			WillReturnRows(commentRow(c))

		got, err := repo.GetComments(ctx, rootURI, 10, 0)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, c.Text, got[0].Text)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT c.*, a.handle as actor_handle, a.display_name`)).
			WithArgs(rootURI, 10, 0).
			WillReturnRows(sqlmock.NewRows(commentColumns()))

		got, err := repo.GetComments(ctx, rootURI, 10, 0)
		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT c.*, a.handle as actor_handle, a.display_name`)).
			WithArgs(rootURI, 10, 0).
			WillReturnError(errors.New("query failed"))

		got, err := repo.GetComments(ctx, rootURI, 10, 0)
		require.Error(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetCommentThread
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_GetCommentThread(t *testing.T) {
	ctx := context.Background()
	parentURI := "at://did:plc:author/app.bsky.feed.post/parent"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		c := sampleComment()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT c.*, a.handle as actor_handle, a.display_name`)).
			WithArgs(parentURI, 10, 0).
			WillReturnRows(commentRow(c))

		got, err := repo.GetCommentThread(ctx, parentURI, 10, 0)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, c.Text, got[0].Text)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT c.*, a.handle as actor_handle, a.display_name`)).
			WithArgs(parentURI, 10, 0).
			WillReturnError(errors.New("query failed"))

		got, err := repo.GetCommentThread(ctx, parentURI, 10, 0)
		require.Error(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// CreateModerationLabel
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_CreateModerationLabel(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		label := sampleModerationLabel()
		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO atproto_moderation_labels`)).
			WithArgs(
				label.ActorDID, label.LabelType, label.Reason,
				label.AppliedBy, label.URI, label.CreatedAt,
				label.ExpiresAt, label.Raw,
			).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("55"))

		err := repo.CreateModerationLabel(ctx, &label)
		require.NoError(t, err)
		assert.Equal(t, "55", label.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		label := sampleModerationLabel()
		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO atproto_moderation_labels`)).
			WithArgs(
				label.ActorDID, label.LabelType, label.Reason,
				label.AppliedBy, label.URI, label.CreatedAt,
				label.ExpiresAt, label.Raw,
			).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateModerationLabel(ctx, &label)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// RemoveModerationLabel
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_RemoveModerationLabel(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM atproto_moderation_labels WHERE id = $1`)).
			WithArgs("42").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.RemoveModerationLabel(ctx, "42")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM atproto_moderation_labels WHERE id = $1`)).
			WithArgs("42").
			WillReturnError(errors.New("delete failed"))

		err := repo.RemoveModerationLabel(ctx, "42")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetModerationLabels
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_GetModerationLabels(t *testing.T) {
	ctx := context.Background()
	actorDID := "did:plc:spammer"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		label := sampleModerationLabel()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_moderation_labels`)).
			WithArgs(actorDID).
			WillReturnRows(modLabelRow(label))

		got, err := repo.GetModerationLabels(ctx, actorDID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, label.LabelType, got[0].LabelType)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_moderation_labels`)).
			WithArgs(actorDID).
			WillReturnRows(sqlmock.NewRows(modLabelColumns()))

		got, err := repo.GetModerationLabels(ctx, actorDID)
		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_moderation_labels`)).
			WithArgs(actorDID).
			WillReturnError(errors.New("query failed"))

		got, err := repo.GetModerationLabels(ctx, actorDID)
		require.Error(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// HasBlockedLabel
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_HasBlockedLabel(t *testing.T) {
	ctx := context.Background()

	t.Run("empty labels returns false immediately", func(t *testing.T) {
		repo, _, cleanup := newSocialRepo(t)
		defer cleanup()

		ok, err := repo.HasBlockedLabel(ctx, "did:plc:actor", []string{})
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("true", func(t *testing.T) {
		db, mock := setupSocialMockDBWithAnyValues(t)
		defer func() { _ = db.Close() }()
		repo := NewSocialRepository(db)

		mock.ExpectQuery(`SELECT EXISTS`).
			WithArgs("did:plc:actor", []string{"spam", "impersonation"}).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		ok, err := repo.HasBlockedLabel(ctx, "did:plc:actor", []string{"spam", "impersonation"})
		require.NoError(t, err)
		assert.True(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("false", func(t *testing.T) {
		db, mock := setupSocialMockDBWithAnyValues(t)
		defer func() { _ = db.Close() }()
		repo := NewSocialRepository(db)

		mock.ExpectQuery(`SELECT EXISTS`).
			WithArgs("did:plc:actor", []string{"spam"}).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		ok, err := repo.HasBlockedLabel(ctx, "did:plc:actor", []string{"spam"})
		require.NoError(t, err)
		assert.False(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		db, mock := setupSocialMockDBWithAnyValues(t)
		defer func() { _ = db.Close() }()
		repo := NewSocialRepository(db)

		mock.ExpectQuery(`SELECT EXISTS`).
			WithArgs("did:plc:actor", []string{"spam"}).
			WillReturnError(errors.New("query failed"))

		ok, err := repo.HasBlockedLabel(ctx, "did:plc:actor", []string{"spam"})
		require.Error(t, err)
		assert.False(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetSocialStats
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_GetSocialStats(t *testing.T) {
	ctx := context.Background()
	did := "did:plc:alice"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"followers", "follows", "likes", "comments", "reposts",
		}).AddRow(100, 50, 200, 30, 0)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
			WithArgs(did).
			WillReturnRows(rows)

		stats, err := repo.GetSocialStats(ctx, did)
		require.NoError(t, err)
		require.NotNil(t, stats)
		assert.Equal(t, int64(100), stats.Followers)
		assert.Equal(t, int64(50), stats.Follows)
		assert.Equal(t, int64(200), stats.Likes)
		assert.Equal(t, int64(30), stats.Comments)
		assert.Equal(t, int64(0), stats.Reposts)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
			WithArgs(did).
			WillReturnError(errors.New("query failed"))

		stats, err := repo.GetSocialStats(ctx, did)
		require.Error(t, err)
		require.NotNil(t, stats) // returns &stats with zero values
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// RefreshSocialStats
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_RefreshSocialStats(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`SELECT refresh_social_stats()`)).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.RefreshSocialStats(ctx)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`SELECT refresh_social_stats()`)).
			WillReturnError(errors.New("function not found"))

		err := repo.RefreshSocialStats(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "function not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// LinkLocalUser
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_LinkLocalUser(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE atproto_actors`)).
			WithArgs("did:plc:alice", "local-user-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.LinkLocalUser(ctx, "did:plc:alice", "local-user-1")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE atproto_actors`)).
			WithArgs("did:plc:alice", "local-user-1").
			WillReturnError(errors.New("update failed"))

		err := repo.LinkLocalUser(ctx, "did:plc:alice", "local-user-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "update failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetBlockedLabels (config fetch)
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_GetBlockedLabels(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT value FROM instance_config`)).
			WillReturnRows(
				sqlmock.NewRows([]string{"value"}).
					AddRow(json.RawMessage(`["spam","impersonation"]`)),
			)

		got, err := repo.GetBlockedLabels(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"spam", "impersonation"}, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no rows returns empty slice", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT value FROM instance_config`)).
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetBlockedLabels(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{}, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT value FROM instance_config`)).
			WillReturnError(errors.New("connection lost"))

		got, err := repo.GetBlockedLabels(ctx)
		require.Error(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid json", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT value FROM instance_config`)).
			WillReturnRows(
				sqlmock.NewRows([]string{"value"}).
					AddRow(json.RawMessage(`not-json`)),
			)

		got, err := repo.GetBlockedLabels(ctx)
		require.Error(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// BatchUpsertActors
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_BatchUpsertActors(t *testing.T) {
	ctx := context.Background()

	t.Run("empty slice returns nil immediately", func(t *testing.T) {
		repo, _, cleanup := newSocialRepo(t)
		defer cleanup()

		err := repo.BatchUpsertActors(ctx, []domain.ATProtoActor{})
		require.NoError(t, err)
	})

	t.Run("success with one actor uses single INSERT", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		actor := sampleActor()
		mock.ExpectBegin()
		// Single multi-row INSERT (no Prepare)
		mock.ExpectExec(`INSERT INTO atproto_actors`).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		err := repo.BatchUpsertActors(ctx, []domain.ATProtoActor{actor})
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with multiple actors uses single INSERT", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		actors := []domain.ATProtoActor{sampleActor(), sampleActor(), sampleActor()}
		mock.ExpectBegin()
		// One INSERT for all three actors
		mock.ExpectExec(`INSERT INTO atproto_actors`).
			WillReturnResult(sqlmock.NewResult(0, 3))
		mock.ExpectCommit()

		err := repo.BatchUpsertActors(ctx, actors)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("begin tx error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		actor := sampleActor()
		mock.ExpectBegin().WillReturnError(errors.New("begin failed"))

		err := repo.BatchUpsertActors(ctx, []domain.ATProtoActor{actor})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "begin failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error rolls back", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		actor := sampleActor()
		mock.ExpectBegin()
		mock.ExpectExec(`INSERT INTO atproto_actors`).
			WillReturnError(errors.New("exec failed"))
		mock.ExpectRollback()

		err := repo.BatchUpsertActors(ctx, []domain.ATProtoActor{actor})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exec failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// CleanupExpiredLabels
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_CleanupExpiredLabels(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM atproto_moderation_labels`)).
			WillReturnResult(sqlmock.NewResult(0, 5))

		count, err := repo.CleanupExpiredLabels(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(5), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("zero deleted", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM atproto_moderation_labels`)).
			WillReturnResult(sqlmock.NewResult(0, 0))

		count, err := repo.CleanupExpiredLabels(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(0), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM atproto_moderation_labels`)).
			WillReturnError(errors.New("exec failed"))

		count, err := repo.CleanupExpiredLabels(ctx)
		require.Error(t, err)
		assert.Equal(t, int64(0), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM atproto_moderation_labels`)).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected failed")))

		count, err := repo.CleanupExpiredLabels(ctx)
		require.Error(t, err)
		assert.Equal(t, int64(0), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetVideoURI
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_GetVideoURI(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT value FROM instance_config WHERE key = 'atproto_did'`)).
			WillReturnRows(
				sqlmock.NewRows([]string{"value"}).
					AddRow(json.RawMessage(`"did:plc:instance123"`)),
			)

		uri, err := repo.GetVideoURI(ctx, "video-abc")
		require.NoError(t, err)
		assert.Contains(t, uri, "at://did:plc:instance123/app.bsky.feed.post/video_video-abc_")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("config not found", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT value FROM instance_config WHERE key = 'atproto_did'`)).
			WillReturnError(sql.ErrNoRows)

		uri, err := repo.GetVideoURI(ctx, "video-abc")
		require.Error(t, err)
		assert.Empty(t, uri)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT value FROM instance_config WHERE key = 'atproto_did'`)).
			WillReturnError(errors.New("connection lost"))

		uri, err := repo.GetVideoURI(ctx, "video-abc")
		require.Error(t, err)
		assert.Empty(t, uri)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid json in config", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT value FROM instance_config WHERE key = 'atproto_did'`)).
			WillReturnRows(
				sqlmock.NewRows([]string{"value"}).
					AddRow(json.RawMessage(`not-valid-json`)),
			)

		uri, err := repo.GetVideoURI(ctx, "video-abc")
		require.Error(t, err)
		assert.Empty(t, uri)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetLike
// ---------------------------------------------------------------------------

func TestSocialRepository_Unit_GetLike(t *testing.T) {
	ctx := context.Background()
	actorDID := "did:plc:liker"
	subjectURI := "at://subject"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		l := sampleLike()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_likes`)).
			WithArgs(actorDID, subjectURI).
			WillReturnRows(likeRow(l))

		got, err := repo.GetLike(ctx, actorDID, subjectURI)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, l.URI, got.URI)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_likes`)).
			WithArgs(actorDID, subjectURI).
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetLike(ctx, actorDID, subjectURI)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "like not found")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newSocialRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM atproto_likes`)).
			WithArgs(actorDID, subjectURI).
			WillReturnError(errors.New("connection failed"))

		got, err := repo.GetLike(ctx, actorDID, subjectURI)
		require.Error(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
