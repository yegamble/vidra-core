package encoding

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
)

func TestProgressAggregator_SingleResolution(t *testing.T) {
	repo := NewMockEncodingRepository()
	jobID := uuid.NewString()
	job := &domain.EncodingJob{
		ID:        jobID,
		VideoID:   uuid.NewString(),
		Status:    domain.EncodingStatusProcessing,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, repo.CreateJob(context.Background(), job))

	agg := newProgressAggregator(jobID, repo, []string{"720p"})

	agg.update(context.Background(), "720p", 50)

	updated, err := repo.GetJob(context.Background(), jobID)
	require.NoError(t, err)
	assert.Equal(t, 50, updated.Progress)
}

func TestProgressAggregator_MultipleResolutions_Average(t *testing.T) {
	repo := NewMockEncodingRepository()
	jobID := uuid.NewString()
	job := &domain.EncodingJob{
		ID:        jobID,
		VideoID:   uuid.NewString(),
		Status:    domain.EncodingStatusProcessing,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, repo.CreateJob(context.Background(), job))

	agg := newProgressAggregator(jobID, repo, []string{"720p", "480p", "360p"})

	agg.update(context.Background(), "720p", 90)
	agg.update(context.Background(), "480p", 60)
	agg.update(context.Background(), "360p", 30)

	updated, err := repo.GetJob(context.Background(), jobID)
	require.NoError(t, err)
	assert.Equal(t, 60, updated.Progress)
}

func TestProgressAggregator_ThrottlesUpdates(t *testing.T) {
	repo := NewMockEncodingRepository()
	jobID := uuid.NewString()
	job := &domain.EncodingJob{
		ID:        jobID,
		VideoID:   uuid.NewString(),
		Status:    domain.EncodingStatusProcessing,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, repo.CreateJob(context.Background(), job))

	agg := newProgressAggregator(jobID, repo, []string{"720p"})

	agg.update(context.Background(), "720p", 1)
	updated, _ := repo.GetJob(context.Background(), jobID)
	assert.Equal(t, 0, updated.Progress, "progress < 5% should not trigger DB update")

	agg.update(context.Background(), "720p", 5)
	updated, _ = repo.GetJob(context.Background(), jobID)
	assert.Equal(t, 5, updated.Progress, "progress >= 5% should trigger DB update")

	agg.update(context.Background(), "720p", 100)
	updated, _ = repo.GetJob(context.Background(), jobID)
	assert.Equal(t, 100, updated.Progress, "100% should always trigger DB update")
}

func TestProgressAggregator_ThreadSafe(t *testing.T) {
	repo := NewMockEncodingRepository()
	jobID := uuid.NewString()
	job := &domain.EncodingJob{
		ID:        jobID,
		VideoID:   uuid.NewString(),
		Status:    domain.EncodingStatusProcessing,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, repo.CreateJob(context.Background(), job))

	resolutions := []string{"720p", "480p", "360p", "240p"}
	agg := newProgressAggregator(jobID, repo, resolutions)

	var wg sync.WaitGroup
	for _, res := range resolutions {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			for pct := 0; pct <= 100; pct += 10 {
				agg.update(context.Background(), r, pct)
			}
		}(res)
	}
	wg.Wait()

	updated, err := repo.GetJob(context.Background(), jobID)
	require.NoError(t, err)
	assert.Equal(t, 100, updated.Progress)
}
