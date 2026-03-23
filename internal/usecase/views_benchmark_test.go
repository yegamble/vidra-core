package usecase

import (
	"context"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

func BenchmarkTrackView(b *testing.B) {
	// Setup mocks
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	// Remove defer service.Close() to manually control timing

	ctx := context.Background()
	videoID := uuid.New().String()
	userID := uuid.New().String()
	sessionID := uuid.New().String()

	// Simulate slow DB calls
	slowDelay := 10 * time.Millisecond

	// Mock video exists
	video := &domain.Video{ID: videoID, Title: "Test Video"}
	mockVideoRepo.On("GetByID", ctx, videoID).Run(func(args mock.Arguments) {
		time.Sleep(slowDelay)
	}).Return(video, nil)

	// Mock no existing view (new view scenario)
	mockViewsRepo.On("GetUserViewBySessionAndVideo", mock.Anything, sessionID, videoID).Run(func(args mock.Arguments) {
		time.Sleep(slowDelay)
	}).Return(nil, nil)

	// Mock successful view creation
	mockViewsRepo.On("CreateUserView", mock.Anything, mock.AnythingOfType("*domain.UserView")).Run(func(args mock.Arguments) {
		time.Sleep(slowDelay)
	}).Return(nil)

	// Mock successful view count increment
	mockViewsRepo.On("IncrementVideoViews", mock.Anything, videoID).Run(func(args mock.Arguments) {
		time.Sleep(slowDelay)
	}).Return(nil)

	request := &domain.ViewTrackingRequest{
		VideoID:              videoID,
		SessionID:            sessionID,
		FingerprintHash:      "bench_hash",
		WatchDuration:        120,
		VideoDuration:        300,
		CompletionPercentage: 40.0,
		IsCompleted:          false,
		DeviceType:           "mobile",
		CountryCode:          "US",
		TrackingConsent:      true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = service.TrackView(ctx, &userID, request)
	}
	b.StopTimer()
	service.Close()
}
