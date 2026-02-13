package redundancy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ==================== SyncRedundancy Tests ====================

func TestSyncRedundancy(t *testing.T) {
	t.Run("success flow completes sync and updates status", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r := validVideoRedundancy()
		r.Status = domain.RedundancyStatusPending
		r.SyncAttemptCount = 0

		redundancyRepo.On("GetVideoRedundancyByID", mock.Anything, "redundancy-1").Return(r, nil)
		// MarkSyncing update
		redundancyRepo.On("UpdateVideoRedundancy", mock.Anything, mock.MatchedBy(func(vr *domain.VideoRedundancy) bool {
			return vr.Status == domain.RedundancyStatusSyncing
		})).Return(nil).Once()
		// performSync calls
		videoRepo.On("GetByID", mock.Anything, "video-1").Return(validVideo(), nil)
		redundancyRepo.On("GetInstancePeerByID", mock.Anything, "peer-1").Return(validInstancePeer(), nil)
		redundancyRepo.On("UpdateInstancePeerContact", mock.Anything, "peer-1").Return(nil)
		redundancyRepo.On("UpdateRedundancyProgress", mock.Anything, "redundancy-1", mock.AnythingOfType("int64"), mock.AnythingOfType("int64")).Return(nil)
		// sync log
		redundancyRepo.On("CreateSyncLog", mock.Anything, mock.Anything).Return(nil)
		// Final update (synced)
		redundancyRepo.On("UpdateVideoRedundancy", mock.Anything, mock.MatchedBy(func(vr *domain.VideoRedundancy) bool {
			return vr.Status == domain.RedundancyStatusSynced
		})).Return(nil).Once()

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		err := svc.SyncRedundancy(context.Background(), "redundancy-1")
		assert.NoError(t, err)

		redundancyRepo.AssertExpectations(t)
		videoRepo.AssertExpectations(t)
	})

	t.Run("already syncing returns error", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r := validVideoRedundancy()
		r.Status = domain.RedundancyStatusSyncing

		redundancyRepo.On("GetVideoRedundancyByID", mock.Anything, "redundancy-1").Return(r, nil)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		err := svc.SyncRedundancy(context.Background(), "redundancy-1")
		assert.ErrorIs(t, err, domain.ErrRedundancyInProgress)
	})

	t.Run("max attempts exceeded returns error", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r := validVideoRedundancy()
		r.Status = domain.RedundancyStatusFailed
		r.SyncAttemptCount = 5
		r.MaxSyncAttempts = 5

		redundancyRepo.On("GetVideoRedundancyByID", mock.Anything, "redundancy-1").Return(r, nil)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		err := svc.SyncRedundancy(context.Background(), "redundancy-1")
		assert.ErrorIs(t, err, domain.ErrRedundancyMaxAttempts)
	})

	t.Run("sync failure marks redundancy as failed", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r := validVideoRedundancy()
		r.Status = domain.RedundancyStatusPending
		r.SyncAttemptCount = 0

		redundancyRepo.On("GetVideoRedundancyByID", mock.Anything, "redundancy-1").Return(r, nil)
		redundancyRepo.On("UpdateVideoRedundancy", mock.Anything, mock.MatchedBy(func(vr *domain.VideoRedundancy) bool {
			return vr.Status == domain.RedundancyStatusSyncing
		})).Return(nil).Once()
		// performSync fails: video not found
		videoRepo.On("GetByID", mock.Anything, "video-1").Return(nil, errors.New("video not found"))
		// sync log saved
		redundancyRepo.On("CreateSyncLog", mock.Anything, mock.MatchedBy(func(l *domain.RedundancySyncLog) bool {
			return !l.Success && l.ErrorMessage != ""
		})).Return(nil)
		// Final update (failed)
		redundancyRepo.On("UpdateVideoRedundancy", mock.Anything, mock.MatchedBy(func(vr *domain.VideoRedundancy) bool {
			return vr.Status == domain.RedundancyStatusFailed
		})).Return(nil).Once()

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		err := svc.SyncRedundancy(context.Background(), "redundancy-1")
		assert.NoError(t, err)

		redundancyRepo.AssertExpectations(t)
	})

	t.Run("redundancy not found returns error", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		redundancyRepo.On("GetVideoRedundancyByID", mock.Anything, "nonexistent").Return(nil, domain.ErrRedundancyNotFound)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		err := svc.SyncRedundancy(context.Background(), "nonexistent")
		assert.Error(t, err)
	})
}

// ==================== performSync Tests ====================

func TestPerformSync(t *testing.T) {
	t.Run("video not found returns error", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r := validVideoRedundancy()
		syncLog := &domain.RedundancySyncLog{
			RedundancyID:  r.ID,
			AttemptNumber: 1,
			StartedAt:     time.Now(),
		}

		videoRepo.On("GetByID", mock.Anything, "video-1").Return(nil, errors.New("not found"))

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		err := svc.performSync(context.Background(), r, syncLog)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get video")
	})

	t.Run("instance not found returns error", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r := validVideoRedundancy()
		syncLog := &domain.RedundancySyncLog{
			RedundancyID:  r.ID,
			AttemptNumber: 1,
			StartedAt:     time.Now(),
		}

		videoRepo.On("GetByID", mock.Anything, "video-1").Return(validVideo(), nil)
		redundancyRepo.On("GetInstancePeerByID", mock.Anything, "peer-1").Return(nil, errors.New("not found"))

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		err := svc.performSync(context.Background(), r, syncLog)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get instance")
	})
}

// ==================== transferVideo Tests ====================

func TestTransferVideo(t *testing.T) {
	t.Run("progress update failure returns error", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r := validVideoRedundancy()
		video := validVideo()
		syncLog := &domain.RedundancySyncLog{
			RedundancyID:  r.ID,
			AttemptNumber: 1,
			StartedAt:     time.Now(),
		}

		redundancyRepo.On("UpdateRedundancyProgress", mock.Anything, r.ID, mock.AnythingOfType("int64"), mock.AnythingOfType("int64")).Return(errors.New("progress update failed"))

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		checksum, bytesTransferred, err := svc.transferVideo(context.Background(), video, "http://target/api/v1/redundancy/receive", r, syncLog)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update progress")
		assert.NotEmpty(t, checksum)
		assert.Equal(t, video.FileSize, bytesTransferred)
	})

	t.Run("success returns checksum and bytes transferred", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r := validVideoRedundancy()
		video := validVideo()
		syncLog := &domain.RedundancySyncLog{
			RedundancyID:  r.ID,
			AttemptNumber: 1,
			StartedAt:     time.Now(),
		}

		redundancyRepo.On("UpdateRedundancyProgress", mock.Anything, r.ID, mock.AnythingOfType("int64"), mock.AnythingOfType("int64")).Return(nil)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		checksum, bytesTransferred, err := svc.transferVideo(context.Background(), video, "http://target/api/v1/redundancy/receive", r, syncLog)
		assert.NoError(t, err)
		assert.NotEmpty(t, checksum)
		assert.Equal(t, video.FileSize, bytesTransferred)
		require.NotNil(t, syncLog.TransferDurationSec)
		require.NotNil(t, syncLog.AverageSpeedBPS)
	})
}

// ==================== ProcessPendingRedundancies Tests ====================

func TestProcessPendingRedundancies(t *testing.T) {
	t.Run("processes batch successfully", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r1 := validVideoRedundancy()
		r1.ID = "r-1"
		r1.Status = domain.RedundancyStatusPending
		r1.SyncAttemptCount = 0

		redundancyRepo.On("ListPendingRedundancies", mock.Anything, 10).Return([]*domain.VideoRedundancy{r1}, nil)
		// SyncRedundancy mock chain for r-1
		redundancyRepo.On("GetVideoRedundancyByID", mock.Anything, "r-1").Return(r1, nil)
		redundancyRepo.On("UpdateVideoRedundancy", mock.Anything, mock.Anything).Return(nil)
		videoRepo.On("GetByID", mock.Anything, "video-1").Return(validVideo(), nil)
		redundancyRepo.On("GetInstancePeerByID", mock.Anything, "peer-1").Return(validInstancePeer(), nil)
		redundancyRepo.On("UpdateInstancePeerContact", mock.Anything, "peer-1").Return(nil)
		redundancyRepo.On("UpdateRedundancyProgress", mock.Anything, "r-1", mock.AnythingOfType("int64"), mock.AnythingOfType("int64")).Return(nil)
		redundancyRepo.On("CreateSyncLog", mock.Anything, mock.Anything).Return(nil)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		processed, err := svc.ProcessPendingRedundancies(context.Background(), 10)
		assert.NoError(t, err)
		assert.Equal(t, 1, processed)
	})

	t.Run("handles partial failures", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r1 := validVideoRedundancy()
		r1.ID = "r-1"
		r1.Status = domain.RedundancyStatusPending

		redundancyRepo.On("ListPendingRedundancies", mock.Anything, 10).Return([]*domain.VideoRedundancy{r1}, nil)
		redundancyRepo.On("GetVideoRedundancyByID", mock.Anything, "r-1").Return(nil, errors.New("not found"))

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		processed, err := svc.ProcessPendingRedundancies(context.Background(), 10)
		assert.NoError(t, err)
		assert.Equal(t, 0, processed)
	})

	t.Run("list error propagated", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		redundancyRepo.On("ListPendingRedundancies", mock.Anything, 10).Return(nil, errors.New("db error"))

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		processed, err := svc.ProcessPendingRedundancies(context.Background(), 10)
		assert.Error(t, err)
		assert.Equal(t, 0, processed)
	})
}

// ==================== ProcessFailedRedundancies Tests ====================

func TestProcessFailedRedundancies(t *testing.T) {
	t.Run("skips non-retryable redundancy", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r1 := validVideoRedundancy()
		r1.ID = "r-1"
		r1.Status = domain.RedundancyStatusFailed
		r1.SyncAttemptCount = 5
		r1.MaxSyncAttempts = 5

		redundancyRepo.On("ListFailedRedundancies", mock.Anything, 10).Return([]*domain.VideoRedundancy{r1}, nil)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		processed, err := svc.ProcessFailedRedundancies(context.Background(), 10)
		assert.NoError(t, err)
		assert.Equal(t, 0, processed)
	})

	t.Run("retries retryable redundancy", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r1 := validVideoRedundancy()
		r1.ID = "r-1"
		r1.Status = domain.RedundancyStatusFailed
		r1.SyncAttemptCount = 2
		r1.MaxSyncAttempts = 5

		redundancyRepo.On("ListFailedRedundancies", mock.Anything, 10).Return([]*domain.VideoRedundancy{r1}, nil)
		redundancyRepo.On("GetVideoRedundancyByID", mock.Anything, "r-1").Return(r1, nil)
		redundancyRepo.On("UpdateVideoRedundancy", mock.Anything, mock.Anything).Return(nil)
		videoRepo.On("GetByID", mock.Anything, "video-1").Return(validVideo(), nil)
		redundancyRepo.On("GetInstancePeerByID", mock.Anything, "peer-1").Return(validInstancePeer(), nil)
		redundancyRepo.On("UpdateInstancePeerContact", mock.Anything, "peer-1").Return(nil)
		redundancyRepo.On("UpdateRedundancyProgress", mock.Anything, "r-1", mock.AnythingOfType("int64"), mock.AnythingOfType("int64")).Return(nil)
		redundancyRepo.On("CreateSyncLog", mock.Anything, mock.Anything).Return(nil)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		processed, err := svc.ProcessFailedRedundancies(context.Background(), 10)
		assert.NoError(t, err)
		assert.Equal(t, 1, processed)
	})

	t.Run("list error propagated", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		redundancyRepo.On("ListFailedRedundancies", mock.Anything, 10).Return(nil, errors.New("db error"))

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		processed, err := svc.ProcessFailedRedundancies(context.Background(), 10)
		assert.Error(t, err)
		assert.Equal(t, 0, processed)
	})
}

// ==================== VerifyRedundancyChecksums Tests ====================

func TestVerifyRedundancyChecksums(t *testing.T) {
	t.Run("verifies batch successfully", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r1 := validVideoRedundancy()
		r1.ID = "r-1"
		r1.Status = domain.RedundancyStatusSynced

		redundancyRepo.On("ListRedundanciesForResync", mock.Anything, 10).Return([]*domain.VideoRedundancy{r1}, nil)
		redundancyRepo.On("UpdateVideoRedundancy", mock.Anything, mock.MatchedBy(func(vr *domain.VideoRedundancy) bool {
			return vr.ID == "r-1" && vr.ChecksumVerifiedAt != nil
		})).Return(nil)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		verified, err := svc.VerifyRedundancyChecksums(context.Background(), 10)
		assert.NoError(t, err)
		assert.Equal(t, 1, verified)
	})

	t.Run("handles update error gracefully", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		r1 := validVideoRedundancy()
		r1.ID = "r-1"
		r1.Status = domain.RedundancyStatusSynced

		redundancyRepo.On("ListRedundanciesForResync", mock.Anything, 10).Return([]*domain.VideoRedundancy{r1}, nil)
		redundancyRepo.On("UpdateVideoRedundancy", mock.Anything, mock.Anything).Return(errors.New("db error"))

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		verified, err := svc.VerifyRedundancyChecksums(context.Background(), 10)
		assert.NoError(t, err)
		assert.Equal(t, 0, verified)
	})

	t.Run("list error propagated", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		redundancyRepo.On("ListRedundanciesForResync", mock.Anything, 10).Return(nil, errors.New("db error"))

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		verified, err := svc.VerifyRedundancyChecksums(context.Background(), 10)
		assert.Error(t, err)
		assert.Equal(t, 0, verified)
	})
}

// ==================== EvaluatePolicies Tests ====================

func TestEvaluatePolicies(t *testing.T) {
	t.Run("creates redundancies based on policy", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		policy := validPolicy()
		policy.TargetInstanceCount = 1

		video := validVideo()
		instance := validInstancePeer()

		redundancyRepo.On("ListPoliciesToEvaluate", mock.Anything).Return([]*domain.RedundancyPolicy{policy}, nil)
		videoRepo.On("GetVideosForRedundancy", mock.Anything, policy.Strategy, 100).Return([]*domain.Video{video}, nil)
		redundancyRepo.On("GetActiveInstancesWithCapacity", mock.Anything, int64(0)).Return([]*domain.InstancePeer{instance}, nil)
		redundancyRepo.On("GetVideoRedundanciesByVideoID", mock.Anything, video.ID).Return([]*domain.VideoRedundancy{}, nil)
		redundancyRepo.On("CreateVideoRedundancy", mock.Anything, mock.MatchedBy(func(vr *domain.VideoRedundancy) bool {
			return vr.VideoID == video.ID && vr.TargetInstanceID == instance.ID
		})).Return(nil)
		redundancyRepo.On("UpdatePolicyEvaluationTime", mock.Anything, policy.ID).Return(nil)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		created, err := svc.EvaluatePolicies(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, 1, created)

		redundancyRepo.AssertExpectations(t)
	})

	t.Run("handles no active instances", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		policy := validPolicy()

		redundancyRepo.On("ListPoliciesToEvaluate", mock.Anything).Return([]*domain.RedundancyPolicy{policy}, nil)
		videoRepo.On("GetVideosForRedundancy", mock.Anything, policy.Strategy, 100).Return([]*domain.Video{validVideo()}, nil)
		redundancyRepo.On("GetActiveInstancesWithCapacity", mock.Anything, int64(0)).Return([]*domain.InstancePeer{}, nil)
		redundancyRepo.On("UpdatePolicyEvaluationTime", mock.Anything, policy.ID).Return(nil)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		created, err := svc.EvaluatePolicies(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, 0, created)
	})

	t.Run("list policies error propagated", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		redundancyRepo.On("ListPoliciesToEvaluate", mock.Anything).Return(nil, errors.New("db error"))

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		created, err := svc.EvaluatePolicies(context.Background())
		assert.Error(t, err)
		assert.Equal(t, 0, created)
	})
}

// ==================== evaluatePolicy Tests ====================

func TestEvaluatePolicy(t *testing.T) {
	t.Run("video already has enough redundancy skips it", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		policy := validPolicy()
		policy.TargetInstanceCount = 1

		video := validVideo()
		instance := validInstancePeer()

		existingRedundancy := validVideoRedundancy()
		existingRedundancy.Status = domain.RedundancyStatusSynced

		videoRepo.On("GetVideosForRedundancy", mock.Anything, policy.Strategy, 100).Return([]*domain.Video{video}, nil)
		redundancyRepo.On("GetActiveInstancesWithCapacity", mock.Anything, int64(0)).Return([]*domain.InstancePeer{instance}, nil)
		redundancyRepo.On("GetVideoRedundanciesByVideoID", mock.Anything, video.ID).Return([]*domain.VideoRedundancy{existingRedundancy}, nil)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		created, err := svc.evaluatePolicy(context.Background(), policy)
		assert.NoError(t, err)
		assert.Equal(t, 0, created)
	})

	t.Run("instance lacks capacity skips it", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		policy := validPolicy()
		policy.TargetInstanceCount = 1

		video := validVideo()
		video.FileSize = 200 * 1024 * 1024 * 1024 // 200GB

		instance := validInstancePeer()
		instance.MaxRedundancySizeGB = 1 // Only 1GB

		videoRepo.On("GetVideosForRedundancy", mock.Anything, policy.Strategy, 100).Return([]*domain.Video{video}, nil)
		redundancyRepo.On("GetActiveInstancesWithCapacity", mock.Anything, int64(0)).Return([]*domain.InstancePeer{instance}, nil)
		redundancyRepo.On("GetVideoRedundanciesByVideoID", mock.Anything, video.ID).Return([]*domain.VideoRedundancy{}, nil)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		created, err := svc.evaluatePolicy(context.Background(), policy)
		assert.NoError(t, err)
		assert.Equal(t, 0, created)
	})

	t.Run("existing redundancy on same instance skips it", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		policy := validPolicy()
		policy.TargetInstanceCount = 2

		video := validVideo()
		instance := validInstancePeer()

		existingRedundancy := validVideoRedundancy()
		existingRedundancy.Status = domain.RedundancyStatusPending
		existingRedundancy.TargetInstanceID = instance.ID

		videoRepo.On("GetVideosForRedundancy", mock.Anything, policy.Strategy, 100).Return([]*domain.Video{video}, nil)
		redundancyRepo.On("GetActiveInstancesWithCapacity", mock.Anything, int64(0)).Return([]*domain.InstancePeer{instance}, nil)
		redundancyRepo.On("GetVideoRedundanciesByVideoID", mock.Anything, video.ID).Return([]*domain.VideoRedundancy{existingRedundancy}, nil)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		created, err := svc.evaluatePolicy(context.Background(), policy)
		assert.NoError(t, err)
		assert.Equal(t, 0, created)
	})

	t.Run("no active instances returns error", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		policy := validPolicy()

		videoRepo.On("GetVideosForRedundancy", mock.Anything, policy.Strategy, 100).Return([]*domain.Video{validVideo()}, nil)
		redundancyRepo.On("GetActiveInstancesWithCapacity", mock.Anything, int64(0)).Return([]*domain.InstancePeer{}, nil)

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)
		created, err := svc.evaluatePolicy(context.Background(), policy)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no active instances available")
		assert.Equal(t, 0, created)
	})
}

// ==================== calculatePriority Tests ====================

func TestCalculatePriority(t *testing.T) {
	tests := []struct {
		name     string
		video    *domain.Video
		strategy domain.RedundancyStrategy
		want     int
	}{
		{
			name: "trending strategy uses views divided by 100",
			video: &domain.Video{
				Views:      5000,
				UploadDate: time.Now(),
			},
			strategy: domain.RedundancyStrategyTrending,
			want:     50,
		},
		{
			name: "recent strategy uses recency",
			video: &domain.Video{
				Views:      100,
				UploadDate: time.Now().Add(-48 * time.Hour),
			},
			strategy: domain.RedundancyStrategyRecent,
			want:     998,
		},
		{
			name: "most viewed strategy uses raw views",
			video: &domain.Video{
				Views:      42,
				UploadDate: time.Now(),
			},
			strategy: domain.RedundancyStrategyMostViewed,
			want:     42,
		},
		{
			name: "unknown strategy returns 0",
			video: &domain.Video{
				Views:      1000,
				UploadDate: time.Now(),
			},
			strategy: domain.RedundancyStrategyManual,
			want:     0,
		},
		{
			name: "negative priority clamped to 0 for very old video",
			video: &domain.Video{
				Views:      100,
				UploadDate: time.Now().Add(-2000 * 24 * time.Hour),
			},
			strategy: domain.RedundancyStrategyRecent,
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculatePriority(tt.video, tt.strategy)
			assert.Equal(t, tt.want, result)
		})
	}
}

// ==================== categorizeError Tests ====================

func TestCategorizeError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil error returns empty string", err: nil, want: ""},
		{name: "timeout error returns network", err: errors.New("connection timeout occurred"), want: "network"},
		{name: "connection error returns network", err: errors.New("connection refused"), want: "network"},
		{name: "auth error returns auth", err: errors.New("authentication failed"), want: "auth"},
		{name: "permission error returns auth", err: errors.New("permission denied"), want: "auth"},
		{name: "403 error returns auth", err: errors.New("HTTP 403 forbidden"), want: "auth"},
		{name: "401 error returns auth", err: errors.New("HTTP 401 unauthorized"), want: "auth"},
		{name: "storage error returns storage", err: errors.New("storage full"), want: "storage"},
		{name: "disk error returns storage", err: errors.New("disk write error"), want: "storage"},
		{name: "space error returns storage", err: errors.New("no space left on device"), want: "storage"},
		{name: "checksum error returns checksum", err: errors.New("checksum mismatch"), want: "checksum"},
		{name: "hash error returns checksum", err: errors.New("hash verification failed"), want: "checksum"},
		{name: "unknown error returns unknown", err: errors.New("something unexpected happened"), want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := categorizeError(tt.err)
			assert.Equal(t, tt.want, result)
		})
	}
}

// ==================== TransferVideoHTTP Tests ====================

func TestTransferVideoHTTP(t *testing.T) {
	t.Run("success with 200 OK", func(t *testing.T) {
		sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "GET", r.Method)
			assert.Empty(t, r.Header.Get("Range"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("video data"))
		}))
		defer sourceServer.Close()

		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)

		svc := NewService(redundancyRepo, videoRepo, sourceServer.Client())

		r := validVideoRedundancy()
		r.BytesTransferred = 0

		err := svc.TransferVideoHTTP(context.Background(), sourceServer.URL+"/video.mp4", "http://target/receive", r)
		assert.NoError(t, err)
	})

	t.Run("partial content resume sets Range header", func(t *testing.T) {
		sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rangeHdr := r.Header.Get("Range")
			assert.Equal(t, "bytes=1024-", rangeHdr)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("remaining data"))
		}))
		defer sourceServer.Close()

		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)

		svc := NewService(redundancyRepo, videoRepo, sourceServer.Client())

		r := validVideoRedundancy()
		r.BytesTransferred = 1024

		err := svc.TransferVideoHTTP(context.Background(), sourceServer.URL+"/video.mp4", "http://target/receive", r)
		assert.NoError(t, err)
	})

	t.Run("unexpected status code returns error", func(t *testing.T) {
		sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer sourceServer.Close()

		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)

		svc := NewService(redundancyRepo, videoRepo, sourceServer.Client())

		r := validVideoRedundancy()
		r.BytesTransferred = 0

		err := svc.TransferVideoHTTP(context.Background(), sourceServer.URL+"/video.mp4", "http://target/receive", r)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected status code: 403")
	})

	t.Run("http client error returns error", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)
		httpDoer := new(MockHTTPDoer)

		httpDoer.On("Do", mock.Anything).Return(nil, errors.New("network error"))

		svc := newTestService(redundancyRepo, videoRepo, httpDoer)

		r := validVideoRedundancy()
		r.BytesTransferred = 0

		err := svc.TransferVideoHTTP(context.Background(), "http://source/video.mp4", "http://target/receive", r)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch video")
	})

	t.Run("body read error returns transfer error", func(t *testing.T) {
		redundancyRepo := new(MockRedundancyRepository)
		videoRepo := new(MockVideoRepository)

		errReader := &errorReadCloser{err: fmt.Errorf("read error")}
		mockHTTPDoer := new(MockHTTPDoer)
		mockHTTPDoer.On("Do", mock.Anything).Return(&http.Response{
			StatusCode: http.StatusOK,
			Body:       errReader,
		}, nil)

		svc := newTestService(redundancyRepo, videoRepo, mockHTTPDoer)

		r := validVideoRedundancy()
		r.BytesTransferred = 0

		err := svc.TransferVideoHTTP(context.Background(), "http://source/video.mp4", "http://target/receive", r)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to transfer video")
	})
}

// errorReadCloser is a helper that always returns an error on Read.
type errorReadCloser struct {
	err error
}

func (e *errorReadCloser) Read(_ []byte) (int, error) {
	return 0, e.err
}

func (e *errorReadCloser) Close() error {
	return nil
}

// Ensure imports are used
var _ = require.NoError
var _ = httptest.NewServer
var _ = fmt.Sprintf
