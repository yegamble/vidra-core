package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPersistState_NoOp(t *testing.T) {
	svc := &circuitBreakerService{}
	svc.persistState(context.Background(), &circuitBreaker{})
}

func TestGetDeduplicationStrategy(t *testing.T) {
	ctx := context.Background()

	t.Run("nil modRepo returns keep_latest", func(t *testing.T) {
		svc := &federationService{modRepo: nil}
		assert.Equal(t, StrategyKeepLatest, svc.getDeduplicationStrategy(ctx))
	})

	t.Run("error from modRepo returns keep_latest", func(t *testing.T) {
		modRepo := new(MockInstanceConfigReader)
		modRepo.On("GetInstanceConfig", ctx, "federation_conflict_strategy").Return((*domain.InstanceConfig)(nil), errors.New("not found"))
		svc := &federationService{modRepo: modRepo}
		assert.Equal(t, StrategyKeepLatest, svc.getDeduplicationStrategy(ctx))
	})

	strategies := []struct {
		value    string
		expected DeduplicationStrategy
	}{
		{"original", StrategyKeepOriginal},
		{"merge", StrategyMerge},
		{"manual", StrategyManual},
		{"unknown", StrategyKeepLatest},
	}

	for _, tt := range strategies {
		t.Run("strategy_"+tt.value, func(t *testing.T) {
			modRepo := new(MockInstanceConfigReader)
			val, _ := json.Marshal(tt.value)
			modRepo.On("GetInstanceConfig", ctx, "federation_conflict_strategy").Return(&domain.InstanceConfig{Value: val}, nil)
			svc := &federationService{modRepo: modRepo}
			assert.Equal(t, tt.expected, svc.getDeduplicationStrategy(ctx))
		})
	}
}

func TestRecordHardeningMetric(t *testing.T) {
	ctx := context.Background()

	t.Run("nil hardening is safe", func(t *testing.T) {
		svc := &federationService{hardening: nil}
		err := svc.recordHardeningMetric(ctx, "test_metric", 1, nil, nil, nil)
		assert.NoError(t, err)
	})

	t.Run("records metric with instance", func(t *testing.T) {
		h := new(MockHardeningRepository)
		h.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
			return m.MetricType == "test_metric" && m.MetricValue == 42
		})).Return(nil)

		svc := &federationService{hardening: h}
		inst := "remote.example.com"
		err := svc.recordHardeningMetric(ctx, "test_metric", 42, &inst, nil, nil)
		assert.NoError(t, err)
		h.AssertExpectations(t)
	})
}

func TestResolveKeepOriginal_Errors(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockFederationRepositoryExt)
	mockH := new(MockHardeningRepository)
	svc := NewDeduplicationService(mockRepo, mockH)

	original := &domain.FederatedPost{ID: "orig"}
	duplicate := &domain.FederatedPost{ID: "dup"}

	t.Run("UpdatePostDuplicateOf error", func(t *testing.T) {
		origID := "orig"
		mockRepo.On("UpdatePostDuplicateOf", ctx, "dup", &origID).Return(errors.New("db err")).Once()
		err := svc.ResolveDuplicate(ctx, original, duplicate, StrategyKeepOriginal)
		require.Error(t, err)
	})

	t.Run("UpdatePostCanonical error", func(t *testing.T) {
		origID := "orig"
		mockRepo.On("UpdatePostDuplicateOf", ctx, "dup", &origID).Return(nil).Once()
		mockRepo.On("UpdatePostCanonical", ctx, "dup", false).Return(errors.New("db err")).Once()
		err := svc.ResolveDuplicate(ctx, original, duplicate, StrategyKeepOriginal)
		require.Error(t, err)
	})
}

func TestResolveDuplicate_UnknownStrategy(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockFederationRepositoryExt)
	mockH := new(MockHardeningRepository)
	svc := NewDeduplicationService(mockRepo, mockH)

	err := svc.ResolveDuplicate(ctx, &domain.FederatedPost{}, &domain.FederatedPost{}, "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown deduplication strategy")
}

func TestMergePosts_Errors(t *testing.T) {
	ctx := context.Background()

	t.Run("canonical post not found", func(t *testing.T) {
		mockRepo := new(MockFederationRepositoryExt)
		mockH := new(MockHardeningRepository)
		svc := NewDeduplicationService(mockRepo, mockH)
		mockRepo.On("GetPost", ctx, "canonical").Return((*domain.FederatedPost)(nil), errors.New("not found"))
		err := svc.MergePosts(ctx, "canonical", []string{"dup1"})
		require.Error(t, err)
	})

	t.Run("duplicate post not found skips without error", func(t *testing.T) {
		mockRepo := new(MockFederationRepositoryExt)
		mockH := new(MockHardeningRepository)
		svc := NewDeduplicationService(mockRepo, mockH)
		mockRepo.On("GetPost", ctx, "canonical").Return(&domain.FederatedPost{ID: "canonical"}, nil)
		mockRepo.On("GetPost", ctx, "dup1").Return((*domain.FederatedPost)(nil), errors.New("not found"))
		err := svc.MergePosts(ctx, "canonical", []string{"dup1"})
		require.NoError(t, err)
	})
}

func TestResolveKeepLatest_Errors(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	earlier := now.Add(-time.Hour)
	later := now.Add(time.Hour)

	t.Run("duplicate newer - UpdatePostCanonical original fails", func(t *testing.T) {
		mockRepo := new(MockFederationRepositoryExt)
		mockH := new(MockHardeningRepository)
		svc := NewDeduplicationService(mockRepo, mockH)
		mockRepo.On("UpdatePostCanonical", ctx, "orig", false).Return(fmt.Errorf("db err")).Once()
		err := svc.ResolveDuplicate(ctx, &domain.FederatedPost{ID: "orig", CreatedAt: &earlier}, &domain.FederatedPost{ID: "dup", CreatedAt: &later}, StrategyKeepLatest)
		require.Error(t, err)
	})

	t.Run("uses InsertedAt when CreatedAt nil", func(t *testing.T) {
		mockRepo := new(MockFederationRepositoryExt)
		mockH := new(MockHardeningRepository)
		svc := NewDeduplicationService(mockRepo, mockH)
		origID := "orig"
		mockRepo.On("UpdatePostDuplicateOf", ctx, "dup", &origID).Return(nil)
		mockRepo.On("UpdatePostCanonical", ctx, "dup", false).Return(nil)
		mockH.On("RecordMetric", ctx, mock.Anything).Return(nil)
		err := svc.ResolveDuplicate(ctx, &domain.FederatedPost{ID: "orig", InsertedAt: later}, &domain.FederatedPost{ID: "dup", InsertedAt: earlier}, StrategyKeepLatest)
		require.NoError(t, err)
	})
}

func TestNewAtprotoService_Disabled(t *testing.T) {
	svc := NewAtprotoService(nil, &config.Config{EnableATProto: false}, nil, nil)
	require.NotNil(t, svc)

	err := svc.PublishVideo(context.Background(), &domain.Video{
		Privacy: domain.PrivacyPublic,
		Status:  domain.StatusCompleted,
	})
	assert.NoError(t, err)
}

func TestPublishVideo_NilVideo(t *testing.T) {
	svc := NewAtprotoService(nil, &config.Config{EnableATProto: true}, nil, nil)
	err := svc.PublishVideo(context.Background(), nil)
	assert.NoError(t, err)
}

func TestMoveToDLQ(t *testing.T) {
	ctx := context.Background()
	repo := new(MockHardeningRepository)
	repo.On("MoveToDLQ", ctx, mock.AnythingOfType("*domain.FederationJob"), "test error").Return(nil)
	repo.On("RecordMetric", ctx, mock.Anything).Return(nil)
	repo.On("GetFederationConfig", ctx).Return(&domain.FederationSecurityConfig{}, nil)

	svc := NewFederationHardeningService(repo, nil, &config.Config{})
	svc.moveToDLQ(ctx, "job-123", "test error")
	repo.AssertCalled(t, "MoveToDLQ", ctx, mock.AnythingOfType("*domain.FederationJob"), "test error")
}

func TestPublishVideo_PrivateVideo(t *testing.T) {
	svc := NewAtprotoService(nil, &config.Config{EnableATProto: true}, nil, nil)
	err := svc.PublishVideo(context.Background(), &domain.Video{
		Privacy: domain.PrivacyPrivate,
		Status:  domain.StatusCompleted,
	})
	assert.NoError(t, err)
}
