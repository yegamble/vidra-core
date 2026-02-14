package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAPMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func stringPtr(s string) *string { return &s }

func TestActivityPubRepository_Unit_GetActorKeys(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name           string
		actorID        string
		setupMock      func(sqlmock.Sqlmock)
		wantPublicKey  string
		wantPrivateKey string
		wantErr        bool
	}{
		{
			name:    "success - no encryption",
			actorID: "actor-1",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT public_key_pem, private_key_pem FROM ap_actor_keys WHERE actor_id = $1`)).
					WithArgs("actor-1").
					WillReturnRows(sqlmock.NewRows([]string{"public_key_pem", "private_key_pem"}).
						AddRow("pub-key", "priv-key"))
			},
			wantPublicKey:  "pub-key",
			wantPrivateKey: "priv-key",
			wantErr:        false,
		},
		{
			name:    "not found",
			actorID: "nonexistent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT public_key_pem, private_key_pem FROM ap_actor_keys WHERE actor_id = $1`)).
					WithArgs("nonexistent").
					WillReturnError(sql.ErrNoRows)
			},
			wantErr: true,
		},
		{
			name:    "database error",
			actorID: "actor-1",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT public_key_pem, private_key_pem FROM ap_actor_keys WHERE actor_id = $1`)).
					WithArgs("actor-1").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setupMock(mock)

			pub, priv, err := repo.GetActorKeys(ctx, tt.actorID)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantPublicKey, pub)
				assert.Equal(t, tt.wantPrivateKey, priv)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_StoreActorKeys(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ap_actor_keys`)).
					WithArgs("actor-1", "pub-key", "priv-key").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ap_actor_keys`)).
					WithArgs("actor-1", "pub-key", "priv-key").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			err := repo.StoreActorKeys(ctx, "actor-1", "pub-key", "priv-key")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_StoreActivity(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	objType := "Note"
	targetID := "target-1"
	activityJSON := json.RawMessage(`{"type":"Create"}`)

	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success - new activity",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO ap_activities`)).
					WithArgs(
						sqlmock.AnyArg(),
						sqlmock.AnyArg(),
						"actor-1",
						"Create",
						stringPtr("obj-1"),
						&objType,
						&targetID,
						now,
						activityJSON,
						true,
					).
					WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("generated-id"))
			},
			wantErr: false,
		},
		{
			name: "conflict - already exists",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO ap_activities`)).
					WithArgs(
						sqlmock.AnyArg(), sqlmock.AnyArg(), "actor-1", "Create",
						stringPtr("obj-1"), &objType, &targetID, now, activityJSON, true,
					).
					WillReturnError(fmt.Errorf("no rows in result set"))
			},
			wantErr: false,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO ap_activities`)).
					WithArgs(
						sqlmock.AnyArg(), sqlmock.AnyArg(), "actor-1", "Create",
						stringPtr("obj-1"), &objType, &targetID, now, activityJSON, true,
					).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			activity := &domain.APActivity{
				ActorID:      "actor-1",
				Type:         "Create",
				ObjectID:     stringPtr("obj-1"),
				ObjectType:   &objType,
				TargetID:     &targetID,
				Published:    now,
				ActivityJSON: activityJSON,
				Local:        true,
			}
			err := repo.StoreActivity(ctx, activity)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_UpsertFollower(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO ap_followers`)).
					WithArgs(sqlmock.AnyArg(), "actor-1", "follower-1", "accepted").
					WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("follower-id"))
			},
			wantErr: false,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO ap_followers`)).
					WithArgs(sqlmock.AnyArg(), "actor-1", "follower-1", "accepted").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			follower := &domain.APFollower{
				ActorID:    "actor-1",
				FollowerID: "follower-1",
				State:      "accepted",
			}
			err := repo.UpsertFollower(ctx, follower)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_DeleteFollower(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM ap_followers WHERE actor_id = $1 AND follower_id = $2`)).
					WithArgs("actor-1", "follower-1").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM ap_followers WHERE actor_id = $1 AND follower_id = $2`)).
					WithArgs("actor-1", "follower-1").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			err := repo.DeleteFollower(ctx, "actor-1", "follower-1")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_EnqueueDelivery(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ap_delivery_queue`)).
					WithArgs(
						sqlmock.AnyArg(),
						"activity-1",
						"https://example.com/inbox", // inbox_url
						"actor-1",
						0,
						3,
						now,
						nil,
						"pending",
					).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ap_delivery_queue`)).
					WithArgs(
						sqlmock.AnyArg(), "activity-1", "https://example.com/inbox",
						"actor-1", 0, 3, now, nil, "pending",
					).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			delivery := &domain.APDeliveryQueue{
				ActivityID:  "activity-1",
				InboxURL:    "https://example.com/inbox",
				ActorID:     "actor-1",
				Attempts:    0,
				MaxAttempts: 3,
				NextAttempt: now,
				LastError:   nil,
				Status:      "pending",
			}
			err := repo.EnqueueDelivery(ctx, delivery)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_UpdateDeliveryStatus(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	lastErr := "connection refused"

	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE ap_delivery_queue`)).
					WithArgs("failed", 3, &lastErr, now, "delivery-1").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE ap_delivery_queue`)).
					WithArgs("failed", 3, &lastErr, now, "delivery-1").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			err := repo.UpdateDeliveryStatus(ctx, "delivery-1", "failed", 3, &lastErr, now)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_IsActivityReceived(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		setup      func(sqlmock.Sqlmock)
		wantExists bool
		wantErr    bool
	}{
		{
			name: "exists",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM ap_received_activities WHERE activity_uri = $1)`)).
					WithArgs("https://example.com/activity/1").
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
			},
			wantExists: true,
		},
		{
			name: "does not exist",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM ap_received_activities WHERE activity_uri = $1)`)).
					WithArgs("https://example.com/activity/2").
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			},
			wantExists: false,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM ap_received_activities WHERE activity_uri = $1)`)).
					WithArgs("uri").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			exists, err := repo.IsActivityReceived(ctx, func() string {
				if tt.name == "exists" {
					return "https://example.com/activity/1"
				}
				if tt.name == "does not exist" {
					return "https://example.com/activity/2"
				}
				return "uri"
			}())
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantExists, exists)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_MarkActivityReceived(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ap_received_activities (activity_uri) VALUES ($1) ON CONFLICT DO NOTHING`)).
					WithArgs("https://example.com/activity/1").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ap_received_activities (activity_uri) VALUES ($1) ON CONFLICT DO NOTHING`)).
					WithArgs("https://example.com/activity/1").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			err := repo.MarkActivityReceived(ctx, "https://example.com/activity/1")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_UpsertVideoReaction(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ap_video_reactions`)).
					WithArgs("video-1", "https://remote.example/user/1", "like", "https://remote.example/activity/1").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ap_video_reactions`)).
					WithArgs("video-1", "https://remote.example/user/1", "like", "https://remote.example/activity/1").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			err := repo.UpsertVideoReaction(ctx, "video-1", "https://remote.example/user/1", "like", "https://remote.example/activity/1")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_DeleteVideoReaction(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM ap_video_reactions WHERE activity_uri = $1`)).
					WithArgs("https://remote.example/activity/1").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM ap_video_reactions WHERE activity_uri = $1`)).
					WithArgs("https://remote.example/activity/1").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			err := repo.DeleteVideoReaction(ctx, "https://remote.example/activity/1")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_GetVideoReactionStats(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name         string
		setup        func(sqlmock.Sqlmock)
		wantLikes    int
		wantDislikes int
		wantErr      bool
	}{
		{
			name: "success - with reactions",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
					WithArgs("video-1").
					WillReturnRows(sqlmock.NewRows([]string{"likes", "dislikes"}).AddRow(10, 2))
			},
			wantLikes:    10,
			wantDislikes: 2,
		},
		{
			name: "success - no reactions",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
					WithArgs("video-1").
					WillReturnRows(sqlmock.NewRows([]string{"likes", "dislikes"}).AddRow(0, 0))
			},
			wantLikes:    0,
			wantDislikes: 0,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
					WithArgs("video-1").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			likes, dislikes, err := repo.GetVideoReactionStats(ctx, "video-1")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantLikes, likes)
				assert.Equal(t, tt.wantDislikes, dislikes)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_UpsertVideoShare(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ap_video_shares`)).
					WithArgs("video-1", "https://remote.example/user/1", "https://remote.example/activity/1").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ap_video_shares`)).
					WithArgs("video-1", "https://remote.example/user/1", "https://remote.example/activity/1").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			err := repo.UpsertVideoShare(ctx, "video-1", "https://remote.example/user/1", "https://remote.example/activity/1")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_DeleteVideoShare(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM ap_video_shares WHERE activity_uri = $1`)).
					WithArgs("https://remote.example/activity/1").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM ap_video_shares WHERE activity_uri = $1`)).
					WithArgs("https://remote.example/activity/1").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			err := repo.DeleteVideoShare(ctx, "https://remote.example/activity/1")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_GetVideoShareCount(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		setup     func(sqlmock.Sqlmock)
		wantCount int
		wantErr   bool
	}{
		{
			name: "success - has shares",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM ap_video_shares WHERE video_id = $1`)).
					WithArgs("video-1").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
			},
			wantCount: 5,
		},
		{
			name: "success - no shares",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM ap_video_shares WHERE video_id = $1`)).
					WithArgs("video-1").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			},
			wantCount: 0,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM ap_video_shares WHERE video_id = $1`)).
					WithArgs("video-1").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			count, err := repo.GetVideoShareCount(ctx, "video-1")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, count)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_UpsertRemoteActor(t *testing.T) {
	ctx := context.Background()
	displayName := "Test User"

	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO ap_remote_actors`)).
					WithArgs(
						sqlmock.AnyArg(),
						"https://remote.example/user/1", // actor_uri
						"Person",
						"testuser",
						"remote.example",
						&displayName,
						(*string)(nil),
						"https://remote.example/inbox", // inbox_url
						(*string)(nil),
						(*string)(nil),
						(*string)(nil),
						(*string)(nil),
						"https://remote.example/user/1#main-key", // public_key_id
						"-----BEGIN PUBLIC KEY-----",
						(*string)(nil),
						(*string)(nil),
						sqlmock.AnyArg(),
					).
					WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("remote-actor-id"))
			},
			wantErr: false,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO ap_remote_actors`)).
					WithArgs(
						sqlmock.AnyArg(), "https://remote.example/user/1", "Person", "testuser",
						"remote.example", &displayName, (*string)(nil),
						"https://remote.example/inbox", (*string)(nil), (*string)(nil),
						(*string)(nil), (*string)(nil),
						"https://remote.example/user/1#main-key", "-----BEGIN PUBLIC KEY-----",
						(*string)(nil), (*string)(nil), sqlmock.AnyArg(),
					).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			actor := &domain.APRemoteActor{
				ActorURI:     "https://remote.example/user/1",
				Type:         "Person",
				Username:     "testuser",
				Domain:       "remote.example",
				DisplayName:  &displayName,
				InboxURL:     "https://remote.example/inbox",
				PublicKeyID:  "https://remote.example/user/1#main-key",
				PublicKeyPem: "-----BEGIN PUBLIC KEY-----",
			}
			err := repo.UpsertRemoteActor(ctx, actor)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_BulkEnqueueDelivery(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	tests := []struct {
		name    string
		items   []*domain.APDeliveryQueue
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name:  "empty list - noop",
			items: nil,
			setup: func(mock sqlmock.Sqlmock) {
			},
			wantErr: false,
		},
		{
			name: "single delivery",
			items: []*domain.APDeliveryQueue{
				{
					ActivityID: "act-1", InboxURL: "https://example.com/inbox",
					ActorID: "actor-1", Attempts: 0, MaxAttempts: 3,
					NextAttempt: now, Status: "pending",
				},
			},
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ap_delivery_queue`)).
					WithArgs(
						sqlmock.AnyArg(), "act-1", "https://example.com/inbox",
						"actor-1", 0, 3, now, nil, "pending",
					).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "database error",
			items: []*domain.APDeliveryQueue{
				{
					ActivityID: "act-1", InboxURL: "https://example.com/inbox",
					ActorID: "actor-1", Attempts: 0, MaxAttempts: 3,
					NextAttempt: now, Status: "pending",
				},
			},
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ap_delivery_queue`)).
					WithArgs(
						sqlmock.AnyArg(), "act-1", "https://example.com/inbox",
						"actor-1", 0, 3, now, nil, "pending",
					).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setup(mock)

			err := repo.BulkEnqueueDelivery(ctx, tt.items)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_GetRemoteActors_Empty(t *testing.T) {
	db, mock := setupAPMockDB(t)
	defer db.Close()
	repo := NewActivityPubRepository(db, nil)

	actors, err := repo.GetRemoteActors(context.Background(), []string{})
	assert.NoError(t, err)
	assert.Nil(t, actors)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestActivityPubRepository_Unit_GetRemoteActors(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		actorURIs []string
		setupMock func(sqlmock.Sqlmock)
		wantCount int
		wantErr   bool
	}{
		{
			name:      "success - single actor",
			actorURIs: []string{"https://example.com/users/alice"},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "actor_uri", "type", "username", "domain", "display_name", "summary",
					"inbox_url", "outbox_url", "shared_inbox", "followers_url", "following_url",
					"public_key_id", "public_key_pem", "icon_url", "image_url", "last_fetched_at",
					"created_at", "updated_at",
				}).AddRow(
					"actor-1", "https://example.com/users/alice", "Person", "alice", "example.com",
					stringPtr("Alice"), stringPtr("Bio"), "https://example.com/inbox", nil,
					nil, nil, nil, "https://example.com/users/alice#main-key", "-----BEGIN PUBLIC KEY-----",
					nil, nil, nil, time.Now(), time.Now(),
				)
				mock.ExpectQuery(`SELECT \* FROM ap_remote_actors WHERE actor_uri IN \(\?\)`).
					WithArgs("https://example.com/users/alice").
					WillReturnRows(rows)
			},
			wantCount: 1,
			wantErr:   false,
		},
		{
			name:      "success - multiple actors",
			actorURIs: []string{"https://example.com/users/alice", "https://example.com/users/bob"},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "actor_uri", "type", "username", "domain", "display_name", "summary",
					"inbox_url", "outbox_url", "shared_inbox", "followers_url", "following_url",
					"public_key_id", "public_key_pem", "icon_url", "image_url", "last_fetched_at",
					"created_at", "updated_at",
				}).AddRow(
					"actor-1", "https://example.com/users/alice", "Person", "alice", "example.com",
					stringPtr("Alice"), nil, "https://example.com/inbox", nil,
					nil, nil, nil, "key-1", "pub-key-1", nil, nil, nil, time.Now(), time.Now(),
				).AddRow(
					"actor-2", "https://example.com/users/bob", "Person", "bob", "example.com",
					stringPtr("Bob"), nil, "https://example.com/inbox2", nil,
					nil, nil, nil, "key-2", "pub-key-2", nil, nil, nil, time.Now(), time.Now(),
				)
				mock.ExpectQuery(`SELECT \* FROM ap_remote_actors WHERE actor_uri IN \(\?, \?\)`).
					WithArgs("https://example.com/users/alice", "https://example.com/users/bob").
					WillReturnRows(rows)
			},
			wantCount: 2,
			wantErr:   false,
		},
		{
			name:      "success - no actors found",
			actorURIs: []string{"https://example.com/users/nonexistent"},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "actor_uri", "type", "username", "domain", "display_name", "summary",
					"inbox_url", "outbox_url", "shared_inbox", "followers_url", "following_url",
					"public_key_id", "public_key_pem", "icon_url", "image_url", "last_fetched_at",
					"created_at", "updated_at",
				})
				mock.ExpectQuery(`SELECT \* FROM ap_remote_actors WHERE actor_uri IN \(\?\)`).
					WithArgs("https://example.com/users/nonexistent").
					WillReturnRows(rows)
			},
			wantCount: 0,
			wantErr:   false,
		},
		{
			name:      "database error",
			actorURIs: []string{"https://example.com/users/alice"},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`SELECT \* FROM ap_remote_actors WHERE actor_uri IN \(\?\)`).
					WithArgs("https://example.com/users/alice").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setupMock(mock)

			actors, err := repo.GetRemoteActors(ctx, tt.actorURIs)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, actors, tt.wantCount)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_GetActivity(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name        string
		activityURI string
		setupMock   func(sqlmock.Sqlmock)
		wantNil     bool
		wantErr     bool
	}{
		{
			name:        "success - activity found",
			activityURI: "https://example.com/activity/123",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "activity_uri", "actor_id", "type", "object_id", "object_type", "target_id", "published", "activity_json", "local", "created_at"}).
					AddRow("act-1", "https://example.com/activity/123", "actor-1", "Create", "obj-1", "Note", nil, time.Now(), json.RawMessage(`{"type":"Create"}`), true, time.Now())
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_activities WHERE activity_uri = $1`)).
					WithArgs("https://example.com/activity/123").
					WillReturnRows(rows)
			},
			wantNil: false,
			wantErr: false,
		},
		{
			name:        "not found - returns nil without error",
			activityURI: "https://example.com/activity/nonexistent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_activities WHERE activity_uri = $1`)).
					WithArgs("https://example.com/activity/nonexistent").
					WillReturnError(sql.ErrNoRows)
			},
			wantNil: true,
			wantErr: false,
		},
		{
			name:        "database error",
			activityURI: "https://example.com/activity/123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_activities WHERE activity_uri = $1`)).
					WithArgs("https://example.com/activity/123").
					WillReturnError(assert.AnError)
			},
			wantNil: false,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setupMock(mock)

			activity, err := repo.GetActivity(ctx, tt.activityURI)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.wantNil {
					assert.Nil(t, activity)
				} else {
					assert.NotNil(t, activity)
					assert.Equal(t, "act-1", activity.ID)
					assert.Equal(t, tt.activityURI, activity.ActivityURI)
					assert.Equal(t, "Create", activity.Type)
				}
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_GetActivitiesByActor(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		actorID   string
		limit     int
		offset    int
		setupMock func(sqlmock.Sqlmock)
		wantCount int
		wantTotal int
		wantErr   bool
	}{
		{
			name:    "success - multiple activities",
			actorID: "actor-1",
			limit:   10,
			offset:  0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "activity_uri", "actor_id", "type", "object_id", "object_type", "target_id", "published", "activity_json", "local", "created_at"}).
					AddRow("act-1", "https://ex.com/act/1", "actor-1", "Create", "obj-1", "Note", nil, time.Now(), json.RawMessage(`{}`), true, time.Now()).
					AddRow("act-2", "https://ex.com/act/2", "actor-1", "Update", "obj-2", "Note", nil, time.Now(), json.RawMessage(`{}`), true, time.Now())
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_activities WHERE actor_id = $1 ORDER BY published DESC LIMIT $2 OFFSET $3`)).
					WithArgs("actor-1", 10, 0).
					WillReturnRows(rows)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM ap_activities WHERE actor_id = $1`)).
					WithArgs("actor-1").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
			},
			wantCount: 2,
			wantTotal: 5,
			wantErr:   false,
		},
		{
			name:    "success - no activities",
			actorID: "actor-2",
			limit:   10,
			offset:  0,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_activities WHERE actor_id = $1 ORDER BY published DESC LIMIT $2 OFFSET $3`)).
					WithArgs("actor-2", 10, 0).
					WillReturnRows(sqlmock.NewRows([]string{"id", "activity_uri", "actor_id", "type", "object_id", "object_type", "target_id", "published", "activity_json", "local", "created_at"}))
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM ap_activities WHERE actor_id = $1`)).
					WithArgs("actor-2").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			},
			wantCount: 0,
			wantTotal: 0,
			wantErr:   false,
		},
		{
			name:    "database error on select",
			actorID: "actor-1",
			limit:   10,
			offset:  0,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_activities WHERE actor_id = $1 ORDER BY published DESC LIMIT $2 OFFSET $3`)).
					WithArgs("actor-1", 10, 0).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
		{
			name:    "database error on count",
			actorID: "actor-1",
			limit:   10,
			offset:  0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "activity_uri", "actor_id", "type", "object_id", "object_type", "target_id", "published", "activity_json", "local", "created_at"}).
					AddRow("act-1", "https://ex.com/act/1", "actor-1", "Create", "obj-1", "Note", nil, time.Now(), json.RawMessage(`{}`), true, time.Now())
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_activities WHERE actor_id = $1 ORDER BY published DESC LIMIT $2 OFFSET $3`)).
					WithArgs("actor-1", 10, 0).
					WillReturnRows(rows)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM ap_activities WHERE actor_id = $1`)).
					WithArgs("actor-1").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setupMock(mock)

			activities, total, err := repo.GetActivitiesByActor(ctx, tt.actorID, tt.limit, tt.offset)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, activities, tt.wantCount)
				assert.Equal(t, tt.wantTotal, total)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_GetFollower(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		actorID    string
		followerID string
		setupMock  func(sqlmock.Sqlmock)
		wantNil    bool
		wantErr    bool
	}{
		{
			name:       "success - follower found",
			actorID:    "actor-1",
			followerID: "follower-1",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "actor_id", "follower_id", "state", "created_at", "updated_at"}).
					AddRow("fol-1", "actor-1", "follower-1", "accepted", time.Now(), time.Now())
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_followers WHERE actor_id = $1 AND follower_id = $2`)).
					WithArgs("actor-1", "follower-1").
					WillReturnRows(rows)
			},
			wantNil: false,
			wantErr: false,
		},
		{
			name:       "not found - returns nil without error",
			actorID:    "actor-1",
			followerID: "nonexistent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_followers WHERE actor_id = $1 AND follower_id = $2`)).
					WithArgs("actor-1", "nonexistent").
					WillReturnError(sql.ErrNoRows)
			},
			wantNil: true,
			wantErr: false,
		},
		{
			name:       "database error",
			actorID:    "actor-1",
			followerID: "follower-1",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_followers WHERE actor_id = $1 AND follower_id = $2`)).
					WithArgs("actor-1", "follower-1").
					WillReturnError(assert.AnError)
			},
			wantNil: false,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setupMock(mock)

			follower, err := repo.GetFollower(ctx, tt.actorID, tt.followerID)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.wantNil {
					assert.Nil(t, follower)
				} else {
					assert.NotNil(t, follower)
					assert.Equal(t, tt.actorID, follower.ActorID)
					assert.Equal(t, tt.followerID, follower.FollowerID)
				}
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_GetFollowers(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		actorID   string
		state     string
		limit     int
		offset    int
		setupMock func(sqlmock.Sqlmock)
		wantCount int
		wantTotal int
		wantErr   bool
	}{
		{
			name:    "success - multiple followers",
			actorID: "actor-1",
			state:   "accepted",
			limit:   10,
			offset:  0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "actor_id", "follower_id", "state", "created_at", "updated_at"}).
					AddRow("fol-1", "actor-1", "follower-1", "accepted", time.Now(), time.Now()).
					AddRow("fol-2", "actor-1", "follower-2", "accepted", time.Now(), time.Now())
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_followers WHERE actor_id = $1 AND state = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`)).
					WithArgs("actor-1", "accepted", 10, 0).
					WillReturnRows(rows)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM ap_followers WHERE actor_id = $1 AND state = $2`)).
					WithArgs("actor-1", "accepted").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10))
			},
			wantCount: 2,
			wantTotal: 10,
			wantErr:   false,
		},
		{
			name:    "success - no followers",
			actorID: "actor-2",
			state:   "accepted",
			limit:   10,
			offset:  0,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_followers WHERE actor_id = $1 AND state = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`)).
					WithArgs("actor-2", "accepted", 10, 0).
					WillReturnRows(sqlmock.NewRows([]string{"id", "actor_id", "follower_id", "state", "created_at", "updated_at"}))
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM ap_followers WHERE actor_id = $1 AND state = $2`)).
					WithArgs("actor-2", "accepted").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			},
			wantCount: 0,
			wantTotal: 0,
			wantErr:   false,
		},
		{
			name:    "database error on select",
			actorID: "actor-1",
			state:   "accepted",
			limit:   10,
			offset:  0,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_followers WHERE actor_id = $1 AND state = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`)).
					WithArgs("actor-1", "accepted", 10, 0).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
		{
			name:    "database error on count",
			actorID: "actor-1",
			state:   "accepted",
			limit:   10,
			offset:  0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "actor_id", "follower_id", "state", "created_at", "updated_at"}).
					AddRow("fol-1", "actor-1", "follower-1", "accepted", time.Now(), time.Now())
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_followers WHERE actor_id = $1 AND state = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`)).
					WithArgs("actor-1", "accepted", 10, 0).
					WillReturnRows(rows)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM ap_followers WHERE actor_id = $1 AND state = $2`)).
					WithArgs("actor-1", "accepted").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setupMock(mock)

			followers, total, err := repo.GetFollowers(ctx, tt.actorID, tt.state, tt.limit, tt.offset)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, followers, tt.wantCount)
				assert.Equal(t, tt.wantTotal, total)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_GetFollowing(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		followerID string
		state      string
		limit      int
		offset     int
		setupMock  func(sqlmock.Sqlmock)
		wantCount  int
		wantTotal  int
		wantErr    bool
	}{
		{
			name:       "success - multiple following",
			followerID: "follower-1",
			state:      "accepted",
			limit:      10,
			offset:     0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "actor_id", "follower_id", "state", "created_at", "updated_at"}).
					AddRow("fol-1", "actor-1", "follower-1", "accepted", time.Now(), time.Now()).
					AddRow("fol-2", "actor-2", "follower-1", "accepted", time.Now(), time.Now())
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_followers WHERE follower_id = $1 AND state = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`)).
					WithArgs("follower-1", "accepted", 10, 0).
					WillReturnRows(rows)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM ap_followers WHERE follower_id = $1 AND state = $2`)).
					WithArgs("follower-1", "accepted").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
			},
			wantCount: 2,
			wantTotal: 5,
			wantErr:   false,
		},
		{
			name:       "success - no following",
			followerID: "follower-2",
			state:      "accepted",
			limit:      10,
			offset:     0,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_followers WHERE follower_id = $1 AND state = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`)).
					WithArgs("follower-2", "accepted", 10, 0).
					WillReturnRows(sqlmock.NewRows([]string{"id", "actor_id", "follower_id", "state", "created_at", "updated_at"}))
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM ap_followers WHERE follower_id = $1 AND state = $2`)).
					WithArgs("follower-2", "accepted").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			},
			wantCount: 0,
			wantTotal: 0,
			wantErr:   false,
		},
		{
			name:       "database error on select",
			followerID: "follower-1",
			state:      "accepted",
			limit:      10,
			offset:     0,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_followers WHERE follower_id = $1 AND state = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`)).
					WithArgs("follower-1", "accepted", 10, 0).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
		{
			name:       "database error on count",
			followerID: "follower-1",
			state:      "accepted",
			limit:      10,
			offset:     0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "actor_id", "follower_id", "state", "created_at", "updated_at"}).
					AddRow("fol-1", "actor-1", "follower-1", "accepted", time.Now(), time.Now())
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_followers WHERE follower_id = $1 AND state = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`)).
					WithArgs("follower-1", "accepted", 10, 0).
					WillReturnRows(rows)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM ap_followers WHERE follower_id = $1 AND state = $2`)).
					WithArgs("follower-1", "accepted").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setupMock(mock)

			following, total, err := repo.GetFollowing(ctx, tt.followerID, tt.state, tt.limit, tt.offset)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, following, tt.wantCount)
				assert.Equal(t, tt.wantTotal, total)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestActivityPubRepository_Unit_GetPendingDeliveries(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	tests := []struct {
		name      string
		limit     int
		setupMock func(sqlmock.Sqlmock)
		wantCount int
		wantErr   bool
	}{
		{
			name:  "success - multiple pending deliveries",
			limit: 10,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "activity_id", "inbox_url", "actor_id", "attempts", "max_attempts", "next_attempt", "last_error", "status", "created_at", "updated_at"}).
					AddRow("del-1", "act-1", "https://inbox1.com", "actor-1", 0, 3, now.Add(-1*time.Hour), nil, "pending", now, now).
					AddRow("del-2", "act-2", "https://inbox2.com", "actor-1", 1, 3, now.Add(-30*time.Minute), stringPtr("error"), "pending", now, now)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_delivery_queue WHERE status = 'pending' AND next_attempt <= $1 ORDER BY next_attempt ASC LIMIT $2`)).
					WithArgs(sqlmock.AnyArg(), 10).
					WillReturnRows(rows)
			},
			wantCount: 2,
			wantErr:   false,
		},
		{
			name:  "success - no pending deliveries",
			limit: 10,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_delivery_queue WHERE status = 'pending' AND next_attempt <= $1 ORDER BY next_attempt ASC LIMIT $2`)).
					WithArgs(sqlmock.AnyArg(), 10).
					WillReturnRows(sqlmock.NewRows([]string{"id", "activity_id", "inbox_url", "actor_id", "attempts", "max_attempts", "next_attempt", "last_error", "status", "created_at", "updated_at"}))
			},
			wantCount: 0,
			wantErr:   false,
		},
		{
			name:  "database error",
			limit: 10,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM ap_delivery_queue WHERE status = 'pending' AND next_attempt <= $1 ORDER BY next_attempt ASC LIMIT $2`)).
					WithArgs(sqlmock.AnyArg(), 10).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupAPMockDB(t)
			defer db.Close()
			repo := NewActivityPubRepository(db, nil)
			tt.setupMock(mock)

			deliveries, err := repo.GetPendingDeliveries(ctx, tt.limit)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, deliveries, tt.wantCount)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
