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
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupRedundancyRepositoryTest(t *testing.T) (*RedundancyRepository, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	repo := NewRedundancyRepository(sqlxDB)

	return repo, mock
}

func TestRedundancyRepository_CreateInstancePeer(t *testing.T) {
	tests := []struct {
		name      string
		peer      *domain.InstancePeer
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			peer: &domain.InstancePeer{
				InstanceURL:          "https://peer.example.com",
				InstanceName:         "Peer Instance",
				InstanceHost:         "peer.example.com",
				Software:             "peertube",
				Version:              "5.0.0",
				AutoAcceptRedundancy: true,
				MaxRedundancySizeGB:  100,
				AcceptsNewRedundancy: true,
				IsActive:             true,
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
					AddRow("peer-123", time.Now(), time.Now())
				mock.ExpectQuery(`INSERT INTO instance_peers`).
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "duplicate instance",
			peer: &domain.InstancePeer{
				InstanceURL: "https://peer.example.com",
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`INSERT INTO instance_peers`).
					WillReturnError(&pq.Error{Code: "23505"})
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrInstancePeerAlreadyExists)
			},
		},
		{
			name: "database error",
			peer: &domain.InstancePeer{
				InstanceURL: "https://peer.example.com",
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`INSERT INTO instance_peers`).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.CreateInstancePeer(context.Background(), tt.peer)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, tt.peer.ID)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_GetInstancePeerByID(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			id:   "peer-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "instance_url", "instance_name", "instance_host",
					"software", "version", "auto_accept_redundancy",
					"max_redundancy_size_gb", "accepts_new_redundancy",
					"is_active", "created_at", "updated_at",
					"last_contacted_at", "last_sync_success_at", "last_sync_error",
					"failed_sync_count", "actor_url", "inbox_url",
					"shared_inbox_url", "public_key", "total_videos_stored",
					"total_storage_bytes",
				}).AddRow(
					"peer-123", "https://peer.example.com", "Peer", "peer.example.com",
					"peertube", "5.0.0", true, 100, true, true,
					time.Now(), time.Now(), nil, nil, "", 0,
					"", "", "", "", 0, 0,
				)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM instance_peers WHERE id = $1`)).
					WithArgs("peer-123").
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "not found",
			id:   "nonexistent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM instance_peers WHERE id = $1`)).
					WithArgs("nonexistent").
					WillReturnError(sql.ErrNoRows)
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrInstancePeerNotFound)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			peer, err := repo.GetInstancePeerByID(context.Background(), tt.id)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, peer)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, peer)
				assert.Equal(t, tt.id, peer.ID)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_ListInstancePeers(t *testing.T) {
	tests := []struct {
		name       string
		limit      int
		offset     int
		activeOnly bool
		setupMock  func(sqlmock.Sqlmock)
		wantErr    bool
		wantCount  int
	}{
		{
			name:       "success - all peers",
			limit:      10,
			offset:     0,
			activeOnly: false,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "instance_url", "instance_name", "instance_host",
					"software", "version", "auto_accept_redundancy",
					"max_redundancy_size_gb", "accepts_new_redundancy",
					"is_active", "created_at", "updated_at",
					"last_contacted_at", "last_sync_success_at", "last_sync_error",
					"failed_sync_count", "actor_url", "inbox_url",
					"shared_inbox_url", "public_key", "total_videos_stored",
					"total_storage_bytes",
				}).
					AddRow("p1", "https://p1.com", "P1", "p1.com", "peertube", "5.0", true, 100, true, true, time.Now(), time.Now(), nil, nil, "", 0, "", "", "", "", 0, 0).
					AddRow("p2", "https://p2.com", "P2", "p2.com", "peertube", "5.0", false, 50, false, false, time.Now(), time.Now(), nil, nil, "", 0, "", "", "", "", 0, 0)

				mock.ExpectQuery(`SELECT \* FROM instance_peers`).
					WithArgs(false, 10, 0).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 2,
		},
		{
			name:       "success - active only",
			limit:      5,
			offset:     0,
			activeOnly: true,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "instance_url", "instance_name", "instance_host",
					"software", "version", "auto_accept_redundancy",
					"max_redundancy_size_gb", "accepts_new_redundancy",
					"is_active", "created_at", "updated_at",
					"last_contacted_at", "last_sync_success_at", "last_sync_error",
					"failed_sync_count", "actor_url", "inbox_url",
					"shared_inbox_url", "public_key", "total_videos_stored",
					"total_storage_bytes",
				}).
					AddRow("p1", "https://p1.com", "P1", "p1.com", "peertube", "5.0", true, 100, true, true, time.Now(), time.Now(), nil, nil, "", 0, "", "", "", "", 0, 0)

				mock.ExpectQuery(`SELECT \* FROM instance_peers`).
					WithArgs(true, 5, 0).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			peers, err := repo.ListInstancePeers(context.Background(), tt.limit, tt.offset, tt.activeOnly)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, peers, tt.wantCount)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_UpdateInstancePeer(t *testing.T) {
	tests := []struct {
		name      string
		peer      *domain.InstancePeer
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			peer: &domain.InstancePeer{
				ID:                  "peer-123",
				InstanceName:        "Updated Name",
				MaxRedundancySizeGB: 200,
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"updated_at"}).
					AddRow(time.Now())
				mock.ExpectQuery(`UPDATE instance_peers`).
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "not found",
			peer: &domain.InstancePeer{
				ID: "nonexistent",
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`UPDATE instance_peers`).
					WillReturnError(sql.ErrNoRows)
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrInstancePeerNotFound)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.UpdateInstancePeer(context.Background(), tt.peer)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_DeleteInstancePeer(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			id:   "peer-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM instance_peers WHERE id = $1`)).
					WithArgs("peer-123").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "not found",
			id:   "nonexistent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM instance_peers WHERE id = $1`)).
					WithArgs("nonexistent").
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrInstancePeerNotFound)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.DeleteInstancePeer(context.Background(), tt.id)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_CreateVideoRedundancy(t *testing.T) {
	tests := []struct {
		name       string
		redundancy *domain.VideoRedundancy
		setupMock  func(sqlmock.Sqlmock)
		wantErr    bool
		errCheck   func(error) bool
	}{
		{
			name: "success",
			redundancy: &domain.VideoRedundancy{
				VideoID:          "video-123",
				TargetInstanceID: "peer-123",
				Strategy:         domain.RedundancyStrategyMostViewed,
				Status:           domain.RedundancyStatusPending,
				FileSizeBytes:    1000000,
				Priority:         5,
				AutoResync:       true,
				MaxSyncAttempts:  3,
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
					AddRow("redundancy-123", time.Now(), time.Now())
				mock.ExpectQuery(`INSERT INTO video_redundancy`).
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "duplicate redundancy",
			redundancy: &domain.VideoRedundancy{
				VideoID:          "video-123",
				TargetInstanceID: "peer-123",
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`INSERT INTO video_redundancy`).
					WillReturnError(&pq.Error{Code: "23505"})
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrRedundancyAlreadyExists)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.CreateVideoRedundancy(context.Background(), tt.redundancy)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, tt.redundancy.ID)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_GetVideoRedundancyByID(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			id:   "redundancy-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "video_id", "target_instance_id", "status", "strategy",
					"file_size_bytes", "priority", "auto_resync", "max_sync_attempts",
					"bytes_transferred", "transfer_speed_bps", "sync_attempt_count",
					"created_at", "updated_at", "target_video_url", "target_video_id",
					"checksum_sha256", "checksum_verified_at", "estimated_completion_at",
					"sync_started_at", "last_sync_at", "next_sync_at", "sync_error",
				}).AddRow(
					"redundancy-123", "video-123", "peer-123", "pending", "most_viewed",
					1000000, 5, true, 3, 0, 0, 0,
					time.Now(), time.Now(), "", "", "", nil, nil, nil, nil, nil, "",
				)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM video_redundancy WHERE id = $1`)).
					WithArgs("redundancy-123").
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "not found",
			id:   "nonexistent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM video_redundancy WHERE id = $1`)).
					WithArgs("nonexistent").
					WillReturnError(sql.ErrNoRows)
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrRedundancyNotFound)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			redundancy, err := repo.GetVideoRedundancyByID(context.Background(), tt.id)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, redundancy)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, redundancy)
				assert.Equal(t, tt.id, redundancy.ID)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_ListPendingRedundancies(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		wantCount int
	}{
		{
			name:  "success",
			limit: 10,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "video_id", "target_instance_id", "status", "strategy",
					"file_size_bytes", "priority", "auto_resync", "max_sync_attempts",
					"bytes_transferred", "transfer_speed_bps", "sync_attempt_count",
					"created_at", "updated_at", "target_video_url", "target_video_id",
					"checksum_sha256", "checksum_verified_at", "estimated_completion_at",
					"sync_started_at", "last_sync_at", "next_sync_at", "sync_error",
				}).
					AddRow("r1", "v1", "p1", "pending", "most_viewed", 1000, 5, true, 3, 0, 0, 0, time.Now(), time.Now(), "", "", "", nil, nil, nil, nil, nil, "").
					AddRow("r2", "v2", "p2", "pending", "recent", 2000, 3, false, 3, 0, 0, 1, time.Now(), time.Now(), "", "", "", nil, nil, nil, nil, nil, "")

				mock.ExpectQuery(`SELECT \* FROM video_redundancy`).
					WithArgs(10).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			redundancies, err := repo.ListPendingRedundancies(context.Background(), tt.limit)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, redundancies, tt.wantCount)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_UpdateVideoRedundancy(t *testing.T) {
	tests := []struct {
		name       string
		redundancy *domain.VideoRedundancy
		setupMock  func(sqlmock.Sqlmock)
		wantErr    bool
		errCheck   func(error) bool
	}{
		{
			name: "success",
			redundancy: &domain.VideoRedundancy{
				ID:               "redundancy-123",
				Status:           domain.RedundancyStatusSynced,
				BytesTransferred: 500000,
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"updated_at"}).
					AddRow(time.Now())
				mock.ExpectQuery(`UPDATE video_redundancy`).
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "not found",
			redundancy: &domain.VideoRedundancy{
				ID: "nonexistent",
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`UPDATE video_redundancy`).
					WillReturnError(sql.ErrNoRows)
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrRedundancyNotFound)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.UpdateVideoRedundancy(context.Background(), tt.redundancy)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_DeleteVideoRedundancy(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			id:   "redundancy-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_redundancy WHERE id = $1`)).
					WithArgs("redundancy-123").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "not found",
			id:   "nonexistent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_redundancy WHERE id = $1`)).
					WithArgs("nonexistent").
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrRedundancyNotFound)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.DeleteVideoRedundancy(context.Background(), tt.id)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_CreateRedundancyPolicy(t *testing.T) {
	tests := []struct {
		name      string
		policy    *domain.RedundancyPolicy
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			policy: &domain.RedundancyPolicy{
				Name:                    "High Priority Videos",
				Description:             "Replicate high-view videos",
				Strategy:                domain.RedundancyStrategyMostViewed,
				Enabled:                 true,
				MinViews:                1000,
				TargetInstanceCount:     3,
				MinInstanceCount:        2,
				EvaluationIntervalHours: 24,
				PrivacyTypes:            []string{"public"},
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
					AddRow("policy-123", time.Now(), time.Now())
				mock.ExpectQuery(`INSERT INTO redundancy_policies`).
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "duplicate policy",
			policy: &domain.RedundancyPolicy{
				Name: "Existing Policy",
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`INSERT INTO redundancy_policies`).
					WillReturnError(&pq.Error{Code: "23505"})
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrPolicyAlreadyExists)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.CreateRedundancyPolicy(context.Background(), tt.policy)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, tt.policy.ID)
				assert.NotNil(t, tt.policy.NextEvaluationAt)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_GetRedundancyPolicyByID(t *testing.T) {
	// Note: Success case skipped - sqlmock cannot properly handle PostgreSQL array types (privacy_types field)

	t.Run("not found", func(t *testing.T) {
		repo, mock := setupRedundancyRepositoryTest(t)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM redundancy_policies WHERE id = $1`)).
			WithArgs("nonexistent").
			WillReturnError(sql.ErrNoRows)

		policy, err := repo.GetRedundancyPolicyByID(context.Background(), "nonexistent")

		require.Error(t, err)
		assert.Nil(t, policy)
		assert.True(t, errors.Is(err, domain.ErrPolicyNotFound))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestRedundancyRepository_ListRedundancyPolicies(t *testing.T) {
	// Note: Skipped - sqlmock cannot properly handle PostgreSQL array types (privacy_types field)
	t.Skip("Requires integration test - PostgreSQL array types not compatible with sqlmock")
}

func TestRedundancyRepository_UpdateRedundancyPolicy(t *testing.T) {
	tests := []struct {
		name      string
		policy    *domain.RedundancyPolicy
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			policy: &domain.RedundancyPolicy{
				ID:          "policy-123",
				Description: "Updated description",
				MinViews:    2000,
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"updated_at"}).
					AddRow(time.Now())
				mock.ExpectQuery(`UPDATE redundancy_policies`).
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "not found",
			policy: &domain.RedundancyPolicy{
				ID: "nonexistent",
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`UPDATE redundancy_policies`).
					WillReturnError(sql.ErrNoRows)
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrPolicyNotFound)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.UpdateRedundancyPolicy(context.Background(), tt.policy)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_DeleteRedundancyPolicy(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			id:   "policy-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM redundancy_policies WHERE id = $1`)).
					WithArgs("policy-123").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "not found",
			id:   "nonexistent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM redundancy_policies WHERE id = $1`)).
					WithArgs("nonexistent").
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrPolicyNotFound)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.DeleteRedundancyPolicy(context.Background(), tt.id)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_CreateSyncLog(t *testing.T) {
	tests := []struct {
		name      string
		log       *domain.RedundancySyncLog
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
	}{
		{
			name: "success",
			log: &domain.RedundancySyncLog{
				RedundancyID:        "redundancy-123",
				AttemptNumber:       1,
				StartedAt:           time.Now(),
				BytesTransferred:    500000,
				Success:             true,
				TransferDurationSec: new(int),
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "created_at"}).
					AddRow("log-123", time.Now())
				mock.ExpectQuery(`INSERT INTO redundancy_sync_log`).
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "database error",
			log: &domain.RedundancySyncLog{
				RedundancyID: "redundancy-123",
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`INSERT INTO redundancy_sync_log`).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.CreateSyncLog(context.Background(), tt.log)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, tt.log.ID)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_GetSyncLogsByRedundancyID(t *testing.T) {
	tests := []struct {
		name         string
		redundancyID string
		limit        int
		setupMock    func(sqlmock.Sqlmock)
		wantErr      bool
		wantCount    int
	}{
		{
			name:         "success",
			redundancyID: "redundancy-123",
			limit:        10,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "redundancy_id", "attempt_number", "started_at",
					"completed_at", "bytes_transferred", "transfer_duration_seconds",
					"average_speed_bps", "success", "error_message", "error_type",
					"http_status_code", "retry_after_seconds", "created_at",
				}).
					AddRow("log1", "redundancy-123", 1, time.Now(), nil, 500000, nil, nil, true, "", "", nil, nil, time.Now()).
					AddRow("log2", "redundancy-123", 2, time.Now(), nil, 600000, nil, nil, false, "timeout", "network", nil, nil, time.Now())

				mock.ExpectQuery(`SELECT \* FROM redundancy_sync_log`).
					WithArgs("redundancy-123", 10).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			logs, err := repo.GetSyncLogsByRedundancyID(context.Background(), tt.redundancyID, tt.limit)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, logs, tt.wantCount)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_GetRedundancyStats(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
	}{
		{
			name: "success",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows1 := sqlmock.NewRows([]string{"total", "pending", "syncing", "synced", "failed"}).
					AddRow(100, 20, 10, 60, 10)
				mock.ExpectQuery(`SELECT COUNT\(\*\) as total`).
					WillReturnRows(rows1)

				rows2 := sqlmock.NewRows([]string{"total", "active"}).
					AddRow(10, 8)
				mock.ExpectQuery(`SELECT COUNT\(\*\) as total`).
					WillReturnRows(rows2)

				rows3 := sqlmock.NewRows([]string{"total", "enabled"}).
					AddRow(5, 3)
				mock.ExpectQuery(`SELECT COUNT\(\*\) as total`).
					WillReturnRows(rows3)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			stats, err := repo.GetRedundancyStats(context.Background())

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, stats)
				assert.Contains(t, stats, "redundancies")
				assert.Contains(t, stats, "instances")
				assert.Contains(t, stats, "policies")
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_GetInstancePeerByURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			url:  "https://peer.example.com",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "instance_url", "instance_name", "instance_host",
					"software", "version", "auto_accept_redundancy",
					"max_redundancy_size_gb", "accepts_new_redundancy",
					"is_active", "created_at", "updated_at",
					"last_contacted_at", "last_sync_success_at", "last_sync_error",
					"failed_sync_count", "actor_url", "inbox_url",
					"shared_inbox_url", "public_key", "total_videos_stored",
					"total_storage_bytes",
				}).AddRow(
					"peer-123", "https://peer.example.com", "Peer", "peer.example.com",
					"peertube", "5.0.0", true, 100, true, true,
					time.Now(), time.Now(), nil, nil, "", 0,
					"", "", "", "", 0, 0,
				)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM instance_peers WHERE instance_url = $1`)).
					WithArgs("https://peer.example.com").
					WillReturnRows(rows)
			},
			wantErr: false,
		},
		{
			name: "not found",
			url:  "https://nonexistent.com",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM instance_peers WHERE instance_url = $1`)).
					WithArgs("https://nonexistent.com").
					WillReturnError(sql.ErrNoRows)
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrInstancePeerNotFound)
			},
		},
		{
			name: "database error",
			url:  "https://peer.example.com",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM instance_peers WHERE instance_url = $1`)).
					WithArgs("https://peer.example.com").
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			peer, err := repo.GetInstancePeerByURL(context.Background(), tt.url)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, peer)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, peer)
				assert.Equal(t, tt.url, peer.InstanceURL)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_UpdateInstancePeerContact(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			id:   "peer-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE instance_peers SET last_contacted_at = NOW(), updated_at = NOW() WHERE id = $1`)).
					WithArgs("peer-123").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "not found",
			id:   "nonexistent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE instance_peers SET last_contacted_at = NOW(), updated_at = NOW() WHERE id = $1`)).
					WithArgs("nonexistent").
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrInstancePeerNotFound)
			},
		},
		{
			name: "database error",
			id:   "peer-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE instance_peers SET last_contacted_at = NOW(), updated_at = NOW() WHERE id = $1`)).
					WithArgs("peer-123").
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.UpdateInstancePeerContact(context.Background(), tt.id)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_GetActiveInstancesWithCapacity(t *testing.T) {
	tests := []struct {
		name           string
		videoSizeBytes int64
		setupMock      func(sqlmock.Sqlmock)
		wantErr        bool
		wantCount      int
	}{
		{
			name:           "success - returns active instances with capacity",
			videoSizeBytes: 1000000000,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "instance_url", "instance_name", "instance_host",
					"software", "version", "auto_accept_redundancy",
					"max_redundancy_size_gb", "accepts_new_redundancy",
					"is_active", "created_at", "updated_at",
					"last_contacted_at", "last_sync_success_at", "last_sync_error",
					"failed_sync_count", "actor_url", "inbox_url",
					"shared_inbox_url", "public_key", "total_videos_stored",
					"total_storage_bytes",
				}).
					AddRow("p1", "https://p1.com", "P1", "p1.com", "peertube", "5.0", true, 100, true, true, time.Now(), time.Now(), nil, time.Now(), "", 0, "", "", "", "", 5, 5000000000).
					AddRow("p2", "https://p2.com", "P2", "p2.com", "peertube", "5.0", true, 50, true, true, time.Now(), time.Now(), nil, nil, "", 0, "", "", "", "", 0, 0)

				mock.ExpectQuery(`SELECT \* FROM instance_peers`).
					WithArgs(int64(1000000000)).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 2,
		},
		{
			name:           "success - no instances available",
			videoSizeBytes: 1000000000,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "instance_url", "instance_name", "instance_host",
					"software", "version", "auto_accept_redundancy",
					"max_redundancy_size_gb", "accepts_new_redundancy",
					"is_active", "created_at", "updated_at",
					"last_contacted_at", "last_sync_success_at", "last_sync_error",
					"failed_sync_count", "actor_url", "inbox_url",
					"shared_inbox_url", "public_key", "total_videos_stored",
					"total_storage_bytes",
				})

				mock.ExpectQuery(`SELECT \* FROM instance_peers`).
					WithArgs(int64(1000000000)).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 0,
		},
		{
			name:           "database error",
			videoSizeBytes: 1000000000,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`SELECT \* FROM instance_peers`).
					WithArgs(int64(1000000000)).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			peers, err := repo.GetActiveInstancesWithCapacity(context.Background(), tt.videoSizeBytes)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, peers, tt.wantCount)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_GetVideoRedundanciesByVideoID(t *testing.T) {
	tests := []struct {
		name      string
		videoID   string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		wantCount int
	}{
		{
			name:    "success - multiple redundancies",
			videoID: "video-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "video_id", "target_instance_id", "status", "strategy",
					"file_size_bytes", "priority", "auto_resync", "max_sync_attempts",
					"bytes_transferred", "transfer_speed_bps", "sync_attempt_count",
					"created_at", "updated_at", "target_video_url", "target_video_id",
					"checksum_sha256", "checksum_verified_at", "estimated_completion_at",
					"sync_started_at", "last_sync_at", "next_sync_at", "sync_error",
				}).
					AddRow("r1", "video-123", "p1", "synced", "most_viewed", 1000, 5, true, 3, 1000, 0, 0, time.Now(), time.Now(), "", "", "", nil, nil, nil, nil, nil, "").
					AddRow("r2", "video-123", "p2", "pending", "recent", 1000, 3, false, 3, 0, 0, 0, time.Now(), time.Now(), "", "", "", nil, nil, nil, nil, nil, "")

				mock.ExpectQuery(`SELECT \* FROM video_redundancy`).
					WithArgs("video-123").
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 2,
		},
		{
			name:    "success - no redundancies",
			videoID: "video-456",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "video_id", "target_instance_id", "status", "strategy",
					"file_size_bytes", "priority", "auto_resync", "max_sync_attempts",
					"bytes_transferred", "transfer_speed_bps", "sync_attempt_count",
					"created_at", "updated_at", "target_video_url", "target_video_id",
					"checksum_sha256", "checksum_verified_at", "estimated_completion_at",
					"sync_started_at", "last_sync_at", "next_sync_at", "sync_error",
				})

				mock.ExpectQuery(`SELECT \* FROM video_redundancy`).
					WithArgs("video-456").
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 0,
		},
		{
			name:    "database error",
			videoID: "video-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`SELECT \* FROM video_redundancy`).
					WithArgs("video-123").
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			redundancies, err := repo.GetVideoRedundanciesByVideoID(context.Background(), tt.videoID)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, redundancies, tt.wantCount)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_GetVideoRedundanciesByInstanceID(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		setupMock  func(sqlmock.Sqlmock)
		wantErr    bool
		wantCount  int
	}{
		{
			name:       "success - multiple redundancies",
			instanceID: "peer-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "video_id", "target_instance_id", "status", "strategy",
					"file_size_bytes", "priority", "auto_resync", "max_sync_attempts",
					"bytes_transferred", "transfer_speed_bps", "sync_attempt_count",
					"created_at", "updated_at", "target_video_url", "target_video_id",
					"checksum_sha256", "checksum_verified_at", "estimated_completion_at",
					"sync_started_at", "last_sync_at", "next_sync_at", "sync_error",
				}).
					AddRow("r1", "v1", "peer-123", "synced", "most_viewed", 1000, 5, true, 3, 1000, 0, 0, time.Now(), time.Now(), "", "", "", nil, nil, nil, nil, nil, "").
					AddRow("r2", "v2", "peer-123", "pending", "recent", 2000, 3, false, 3, 0, 0, 0, time.Now(), time.Now(), "", "", "", nil, nil, nil, nil, nil, "")

				mock.ExpectQuery(`SELECT \* FROM video_redundancy`).
					WithArgs("peer-123").
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 2,
		},
		{
			name:       "success - no redundancies",
			instanceID: "peer-456",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "video_id", "target_instance_id", "status", "strategy",
					"file_size_bytes", "priority", "auto_resync", "max_sync_attempts",
					"bytes_transferred", "transfer_speed_bps", "sync_attempt_count",
					"created_at", "updated_at", "target_video_url", "target_video_id",
					"checksum_sha256", "checksum_verified_at", "estimated_completion_at",
					"sync_started_at", "last_sync_at", "next_sync_at", "sync_error",
				})

				mock.ExpectQuery(`SELECT \* FROM video_redundancy`).
					WithArgs("peer-456").
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 0,
		},
		{
			name:       "database error",
			instanceID: "peer-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`SELECT \* FROM video_redundancy`).
					WithArgs("peer-123").
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			redundancies, err := repo.GetVideoRedundanciesByInstanceID(context.Background(), tt.instanceID)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, redundancies, tt.wantCount)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_ListFailedRedundancies(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		wantCount int
	}{
		{
			name:  "success - returns failed redundancies ready for retry",
			limit: 10,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "video_id", "target_instance_id", "status", "strategy",
					"file_size_bytes", "priority", "auto_resync", "max_sync_attempts",
					"bytes_transferred", "transfer_speed_bps", "sync_attempt_count",
					"created_at", "updated_at", "target_video_url", "target_video_id",
					"checksum_sha256", "checksum_verified_at", "estimated_completion_at",
					"sync_started_at", "last_sync_at", "next_sync_at", "sync_error",
				}).
					AddRow("r1", "v1", "p1", "failed", "most_viewed", 1000, 5, true, 5, 500, 0, 2, time.Now(), time.Now(), "", "", "", nil, nil, nil, nil, nil, "timeout").
					AddRow("r2", "v2", "p2", "failed", "recent", 2000, 3, false, 3, 0, 0, 1, time.Now(), time.Now(), "", "", "", nil, nil, nil, nil, nil, "network error")

				mock.ExpectQuery(`SELECT \* FROM video_redundancy`).
					WithArgs(10).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 2,
		},
		{
			name:  "success - no failed redundancies",
			limit: 10,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "video_id", "target_instance_id", "status", "strategy",
					"file_size_bytes", "priority", "auto_resync", "max_sync_attempts",
					"bytes_transferred", "transfer_speed_bps", "sync_attempt_count",
					"created_at", "updated_at", "target_video_url", "target_video_id",
					"checksum_sha256", "checksum_verified_at", "estimated_completion_at",
					"sync_started_at", "last_sync_at", "next_sync_at", "sync_error",
				})

				mock.ExpectQuery(`SELECT \* FROM video_redundancy`).
					WithArgs(10).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 0,
		},
		{
			name:  "database error",
			limit: 10,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`SELECT \* FROM video_redundancy`).
					WithArgs(10).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			redundancies, err := repo.ListFailedRedundancies(context.Background(), tt.limit)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, redundancies, tt.wantCount)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_ListRedundanciesForResync(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		wantCount int
	}{
		{
			name:  "success - returns redundancies needing checksum verification",
			limit: 10,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "video_id", "target_instance_id", "status", "strategy",
					"file_size_bytes", "priority", "auto_resync", "max_sync_attempts",
					"bytes_transferred", "transfer_speed_bps", "sync_attempt_count",
					"created_at", "updated_at", "target_video_url", "target_video_id",
					"checksum_sha256", "checksum_verified_at", "estimated_completion_at",
					"sync_started_at", "last_sync_at", "next_sync_at", "sync_error",
				}).
					AddRow("r1", "v1", "p1", "synced", "most_viewed", 1000, 5, true, 3, 1000, 0, 0, time.Now(), time.Now(), "", "", "abc123", nil, nil, nil, nil, nil, "").
					AddRow("r2", "v2", "p2", "synced", "recent", 2000, 3, true, 3, 2000, 0, 0, time.Now(), time.Now(), "", "", "def456", time.Now().Add(-8*24*time.Hour), nil, nil, nil, nil, "")

				mock.ExpectQuery(`SELECT \* FROM video_redundancy`).
					WithArgs(10).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 2,
		},
		{
			name:  "success - no redundancies need resync",
			limit: 10,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "video_id", "target_instance_id", "status", "strategy",
					"file_size_bytes", "priority", "auto_resync", "max_sync_attempts",
					"bytes_transferred", "transfer_speed_bps", "sync_attempt_count",
					"created_at", "updated_at", "target_video_url", "target_video_id",
					"checksum_sha256", "checksum_verified_at", "estimated_completion_at",
					"sync_started_at", "last_sync_at", "next_sync_at", "sync_error",
				})

				mock.ExpectQuery(`SELECT \* FROM video_redundancy`).
					WithArgs(10).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 0,
		},
		{
			name:  "database error",
			limit: 10,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`SELECT \* FROM video_redundancy`).
					WithArgs(10).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			redundancies, err := repo.ListRedundanciesForResync(context.Background(), tt.limit)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, redundancies, tt.wantCount)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_UpdateRedundancyProgress(t *testing.T) {
	tests := []struct {
		name             string
		id               string
		bytesTransferred int64
		speedBPS         int64
		setupMock        func(sqlmock.Sqlmock)
		wantErr          bool
		errCheck         func(error) bool
	}{
		{
			name:             "success",
			id:               "redundancy-123",
			bytesTransferred: 500000,
			speedBPS:         1000000,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE video_redundancy SET bytes_transferred = $2, transfer_speed_bps = $3, updated_at = NOW() WHERE id = $1`)).
					WithArgs("redundancy-123", int64(500000), int64(1000000)).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name:             "not found",
			id:               "nonexistent",
			bytesTransferred: 500000,
			speedBPS:         1000000,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE video_redundancy SET bytes_transferred = $2, transfer_speed_bps = $3, updated_at = NOW() WHERE id = $1`)).
					WithArgs("nonexistent", int64(500000), int64(1000000)).
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrRedundancyNotFound)
			},
		},
		{
			name:             "database error",
			id:               "redundancy-123",
			bytesTransferred: 500000,
			speedBPS:         1000000,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE video_redundancy SET bytes_transferred = $2, transfer_speed_bps = $3, updated_at = NOW() WHERE id = $1`)).
					WithArgs("redundancy-123", int64(500000), int64(1000000)).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.UpdateRedundancyProgress(context.Background(), tt.id, tt.bytesTransferred, tt.speedBPS)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_CancelRedundanciesByInstanceID(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		setupMock  func(sqlmock.Sqlmock)
		wantErr    bool
	}{
		{
			name:       "success - cancels redundancies",
			instanceID: "peer-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE video_redundancy SET status = 'cancelled', updated_at = NOW() WHERE target_instance_id = $1 AND status IN ('pending', 'syncing')`)).
					WithArgs("peer-123").
					WillReturnResult(sqlmock.NewResult(0, 5))
			},
			wantErr: false,
		},
		{
			name:       "success - no redundancies to cancel",
			instanceID: "peer-456",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE video_redundancy SET status = 'cancelled', updated_at = NOW() WHERE target_instance_id = $1 AND status IN ('pending', 'syncing')`)).
					WithArgs("peer-456").
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr: false,
		},
		{
			name:       "database error",
			instanceID: "peer-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE video_redundancy SET status = 'cancelled', updated_at = NOW() WHERE target_instance_id = $1 AND status IN ('pending', 'syncing')`)).
					WithArgs("peer-123").
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.CancelRedundanciesByInstanceID(context.Background(), tt.instanceID)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_DeleteVideoRedundanciesByVideoID(t *testing.T) {
	tests := []struct {
		name      string
		videoID   string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
	}{
		{
			name:    "success - deletes all redundancies for video",
			videoID: "video-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_redundancy WHERE video_id = $1`)).
					WithArgs("video-123").
					WillReturnResult(sqlmock.NewResult(0, 3))
			},
			wantErr: false,
		},
		{
			name:    "success - no redundancies to delete",
			videoID: "video-456",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_redundancy WHERE video_id = $1`)).
					WithArgs("video-456").
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr: false,
		},
		{
			name:    "database error",
			videoID: "video-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_redundancy WHERE video_id = $1`)).
					WithArgs("video-123").
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.DeleteVideoRedundanciesByVideoID(context.Background(), tt.videoID)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_GetRedundancyPolicyByName(t *testing.T) {
	// Note: Success case skipped - sqlmock cannot properly handle PostgreSQL array types (privacy_types field)

	t.Run("not found", func(t *testing.T) {
		repo, mock := setupRedundancyRepositoryTest(t)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM redundancy_policies WHERE name = $1`)).
			WithArgs("nonexistent").
			WillReturnError(sql.ErrNoRows)

		policy, err := repo.GetRedundancyPolicyByName(context.Background(), "nonexistent")

		require.Error(t, err)
		assert.Nil(t, policy)
		assert.True(t, errors.Is(err, domain.ErrPolicyNotFound))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		repo, mock := setupRedundancyRepositoryTest(t)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM redundancy_policies WHERE name = $1`)).
			WithArgs("test-policy").
			WillReturnError(sql.ErrConnDone)

		policy, err := repo.GetRedundancyPolicyByName(context.Background(), "test-policy")

		require.Error(t, err)
		assert.Nil(t, policy)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestRedundancyRepository_ListPoliciesToEvaluate(t *testing.T) {
	// Note: Skipped - sqlmock cannot properly handle PostgreSQL array types (privacy_types field)
	t.Skip("Requires integration test - PostgreSQL array types not compatible with sqlmock")
}

func TestRedundancyRepository_UpdatePolicyEvaluationTime(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "success",
			id:   "policy-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE redundancy_policies SET last_evaluated_at = NOW(), next_evaluation_at = NOW() + (evaluation_interval_hours || ' hours')::INTERVAL, updated_at = NOW() WHERE id = $1`)).
					WithArgs("policy-123").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name: "not found",
			id:   "nonexistent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE redundancy_policies SET last_evaluated_at = NOW(), next_evaluation_at = NOW() + (evaluation_interval_hours || ' hours')::INTERVAL, updated_at = NOW() WHERE id = $1`)).
					WithArgs("nonexistent").
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, domain.ErrPolicyNotFound)
			},
		},
		{
			name: "database error",
			id:   "policy-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`UPDATE redundancy_policies SET last_evaluated_at = NOW(), next_evaluation_at = NOW() + (evaluation_interval_hours || ' hours')::INTERVAL, updated_at = NOW() WHERE id = $1`)).
					WithArgs("policy-123").
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			err := repo.UpdatePolicyEvaluationTime(context.Background(), tt.id)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					assert.True(t, tt.errCheck(err))
				}
			} else {
				require.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_CleanupOldSyncLogs(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		wantCount int
	}{
		{
			name: "success - deletes old logs",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"cleanup_old_redundancy_logs"}).
					AddRow(42)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT cleanup_old_redundancy_logs()`)).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 42,
		},
		{
			name: "success - no logs to delete",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"cleanup_old_redundancy_logs"}).
					AddRow(0)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT cleanup_old_redundancy_logs()`)).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 0,
		},
		{
			name: "database error",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT cleanup_old_redundancy_logs()`)).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			count, err := repo.CleanupOldSyncLogs(context.Background())

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, count)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_GetVideoRedundancyHealth(t *testing.T) {
	tests := []struct {
		name       string
		videoID    string
		setupMock  func(sqlmock.Sqlmock)
		wantErr    bool
		wantHealth float64
	}{
		{
			name:    "success - high health score",
			videoID: "video-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"get_video_redundancy_health"}).
					AddRow(0.95)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT get_video_redundancy_health($1)`)).
					WithArgs("video-123").
					WillReturnRows(rows)
			},
			wantErr:    false,
			wantHealth: 0.95,
		},
		{
			name:    "success - low health score",
			videoID: "video-456",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"get_video_redundancy_health"}).
					AddRow(0.25)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT get_video_redundancy_health($1)`)).
					WithArgs("video-456").
					WillReturnRows(rows)
			},
			wantErr:    false,
			wantHealth: 0.25,
		},
		{
			name:    "success - zero health (no redundancies)",
			videoID: "video-789",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"get_video_redundancy_health"}).
					AddRow(0.0)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT get_video_redundancy_health($1)`)).
					WithArgs("video-789").
					WillReturnRows(rows)
			},
			wantErr:    false,
			wantHealth: 0.0,
		},
		{
			name:    "database error",
			videoID: "video-123",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT get_video_redundancy_health($1)`)).
					WithArgs("video-123").
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			health, err := repo.GetVideoRedundancyHealth(context.Background(), tt.videoID)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantHealth, health)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRedundancyRepository_CheckInstanceHealth(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		wantCount int
	}{
		{
			name: "success - marks unhealthy instances as inactive",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"check_instance_health"}).
					AddRow(3)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT check_instance_health()`)).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 3,
		},
		{
			name: "success - all instances healthy",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"check_instance_health"}).
					AddRow(0)
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT check_instance_health()`)).
					WillReturnRows(rows)
			},
			wantErr:   false,
			wantCount: 0,
		},
		{
			name: "database error",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT check_instance_health()`)).
					WillReturnError(sql.ErrConnDone)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := setupRedundancyRepositoryTest(t)
			tt.setupMock(mock)

			count, err := repo.CheckInstanceHealth(context.Background())

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, count)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
