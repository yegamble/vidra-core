package views

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"athena/internal/domain"
	"athena/internal/port"
)

type viewTask struct {
	userID  *string
	request *domain.ViewTrackingRequest
}

type Service struct {
	viewsRepo port.ViewsRepository
	videoRepo port.VideoRepository
	viewQueue chan viewTask
	workerWG  sync.WaitGroup
	closeOnce sync.Once
}

func NewService(viewsRepo port.ViewsRepository, videoRepo port.VideoRepository) *Service {
	s := &Service{
		viewsRepo: viewsRepo,
		videoRepo: videoRepo,
		viewQueue: make(chan viewTask, 1000), // Buffer size 1000
	}
	s.workerWG.Add(1)
	go s.worker()
	return s
}

// Close gracefully shuts down the service, waiting for pending tasks
func (s *Service) Close() {
	s.closeOnce.Do(func() {
		close(s.viewQueue)
	})
	s.workerWG.Wait()
}

func (s *Service) worker() {
	defer s.workerWG.Done()
	for task := range s.viewQueue {
		s.processViewTask(task)
	}
}

func (s *Service) processViewTask(task viewTask) {
	// Use a background context with timeout for worker operations
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	request := task.request
	userID := task.userID

	// Check if this is an existing view session
	existingView, err := s.viewsRepo.GetUserViewBySessionAndVideo(ctx, request.SessionID, request.VideoID)
	if err != nil {
		// In a real application, we should log this error
		return
	}

	now := time.Now()

	if existingView != nil {
		// Update existing view session
		existingView.WatchDuration = request.WatchDuration
		existingView.CompletionPercentage = request.CompletionPercentage
		existingView.IsCompleted = request.IsCompleted
		existingView.SeekCount = request.SeekCount
		existingView.PauseCount = request.PauseCount
		existingView.ReplayCount = request.ReplayCount
		existingView.QualityChanges = request.QualityChanges
		existingView.BufferEvents = request.BufferEvents

		_ = s.viewsRepo.UpdateUserView(ctx, existingView)
		return
	}

	// Create new view record
	view := &domain.UserView{
		VideoID:         request.VideoID,
		UserID:          userID,
		SessionID:       request.SessionID,
		FingerprintHash: request.FingerprintHash,

		// Engagement metrics
		WatchDuration:        request.WatchDuration,
		VideoDuration:        request.VideoDuration,
		CompletionPercentage: request.CompletionPercentage,
		IsCompleted:          request.IsCompleted,

		// Interaction metrics
		SeekCount:      request.SeekCount,
		PauseCount:     request.PauseCount,
		ReplayCount:    request.ReplayCount,
		QualityChanges: request.QualityChanges,

		// Technical metrics
		InitialLoadTime: request.InitialLoadTime,
		BufferEvents:    request.BufferEvents,
		ConnectionType:  request.ConnectionType,
		VideoQuality:    stringPtrIfNotEmpty(request.VideoQuality),

		// Context and attribution
		ReferrerURL:  request.ReferrerURL,
		ReferrerType: request.ReferrerType,
		UTMSource:    request.UTMSource,
		UTMMedium:    request.UTMMedium,
		UTMCampaign:  request.UTMCampaign,

		// Device and environment
		DeviceType:       request.DeviceType,
		OSName:           request.OSName,
		BrowserName:      request.BrowserName,
		ScreenResolution: request.ScreenResolution,
		IsMobile:         request.IsMobile,

		// Geographic data
		CountryCode: request.CountryCode,
		RegionCode:  request.RegionCode,
		CityName:    request.CityName,
		Timezone:    request.Timezone,

		// Privacy and consent
		IsAnonymous:     request.IsAnonymous,
		TrackingConsent: request.TrackingConsent,
		GDPRConsent:     request.GDPRConsent,

		// Temporal data
		ViewDate: now.Truncate(24 * time.Hour),
		ViewHour: now.Hour(),
		Weekday:  int(now.Weekday()),

		CreatedAt: now,
		UpdatedAt: now,
	}

	// Create the view record
	if err := s.viewsRepo.CreateUserView(ctx, view); err != nil {
		return
	}
	// Increment the video's total view count (best-effort)
	_ = s.viewsRepo.IncrementVideoViews(ctx, request.VideoID)
}

// TrackView tracks a user view with deduplication and session management
func (s *Service) TrackView(ctx context.Context, userID *string, request *domain.ViewTrackingRequest) error {
	// Validate that the video exists
	video, err := s.videoRepo.GetByID(ctx, request.VideoID)
	if err != nil {
		return fmt.Errorf("failed to verify video exists: %w", err)
	}
	if video == nil {
		return fmt.Errorf("video not found: %s", request.VideoID)
	}

	// Submit task to background worker
	select {
	case s.viewQueue <- viewTask{userID: userID, request: request}:
		return nil
	default:
		return fmt.Errorf("tracking queue full")
	}
}

// GetVideoAnalytics gets comprehensive analytics for a video
func (s *Service) GetVideoAnalytics(ctx context.Context, filter *domain.ViewAnalyticsFilter) (*domain.ViewAnalyticsResponse, error) {
	return s.viewsRepo.GetVideoAnalytics(ctx, filter)
}

// GetDailyStats gets pre-aggregated daily statistics for a video
func (s *Service) GetDailyStats(ctx context.Context, videoID string, days int) ([]domain.DailyVideoStats, error) {
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -days)
	return s.viewsRepo.GetDailyVideoStats(ctx, videoID, startDate, endDate)
}

// GetUserEngagement gets user-level engagement statistics
func (s *Service) GetUserEngagement(ctx context.Context, userID string, days int) ([]domain.UserEngagementStats, error) {
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -days)
	return s.viewsRepo.GetUserEngagementStats(ctx, userID, startDate, endDate)
}

// GetTrendingVideos gets current trending videos
func (s *Service) GetTrendingVideos(ctx context.Context, limit int) ([]domain.TrendingVideo, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.viewsRepo.GetTrendingVideos(ctx, limit)
}

// GetTrendingVideosWithDetails gets trending videos with full video details
func (s *Service) GetTrendingVideosWithDetails(ctx context.Context, limit int) (*domain.TrendingVideosResponse, error) {
	trendingVideos, err := s.GetTrendingVideos(ctx, limit)
	if err != nil {
		return nil, err
	}
	response := &domain.TrendingVideosResponse{Videos: make([]domain.TrendingVideoWithDetails, 0, len(trendingVideos)), UpdatedAt: time.Now()}
	for _, tv := range trendingVideos {
		video, err := s.videoRepo.GetByID(ctx, tv.VideoID)
		if err != nil || video == nil {
			continue
		}
		response.Videos = append(response.Videos, domain.TrendingVideoWithDetails{
			VideoID:         video.ID,
			Title:           video.Title,
			Description:     video.Description,
			ThumbnailURL:    video.ThumbnailPath,
			Duration:        video.Duration,
			Views:           video.Views,
			EngagementScore: tv.EngagementScore,
			VelocityScore:   tv.VelocityScore,
			CreatedAt:       video.CreatedAt,
			TrendingVideo:   &tv,
			Video:           video,
		})
	}
	return response, nil
}

// UpdateTrendingMetrics calculates and updates trending metrics for videos
func (s *Service) UpdateTrendingMetrics(ctx context.Context, videoIDs []string) error {
	now := time.Now()
	for _, videoID := range videoIDs {
		hourlyScore, err := s.viewsRepo.CalculateEngagementScore(ctx, videoID, 1)
		if err != nil {
			continue
		}
		dailyScore, err := s.viewsRepo.CalculateEngagementScore(ctx, videoID, 24)
		if err != nil {
			continue
		}
		weeklyScore, err := s.viewsRepo.CalculateEngagementScore(ctx, videoID, 168)
		if err != nil {
			continue
		}
		viewsLastHour, err := s.getViewsInPeriod(ctx, videoID, 1*time.Hour)
		if err != nil {
			continue
		}
		viewsLast24h, err := s.getViewsInPeriod(ctx, videoID, 24*time.Hour)
		if err != nil {
			continue
		}
		viewsLast7d, err := s.getViewsInPeriod(ctx, videoID, 7*24*time.Hour)
		if err != nil {
			continue
		}
		velocityScore := calculateVelocityScore(viewsLastHour, viewsLast24h, viewsLast7d)
		isTrending := dailyScore > 100.0 || (hourlyScore > 50.0 && velocityScore > 10.0) || weeklyScore > 200.0
		tv := &domain.TrendingVideo{
			VideoID:         videoID,
			ViewsLastHour:   viewsLastHour,
			ViewsLast24h:    viewsLast24h,
			ViewsLast7d:     viewsLast7d,
			EngagementScore: dailyScore,
			VelocityScore:   velocityScore,
			LastUpdated:     now,
			IsTrending:      isTrending,
		}
		_ = s.viewsRepo.UpdateTrendingVideo(ctx, tv)
	}
	return nil
}

func (s *Service) getViewsInPeriod(ctx context.Context, videoID string, period time.Duration) (int64, error) {
	endTime := time.Now()
	startTime := endTime.Add(-period)
	return s.viewsRepo.GetUniqueViews(ctx, videoID, startTime, endTime)
}

func calculateVelocityScore(hourlyViews, dailyViews, weeklyViews int64) float64 {
	if weeklyViews == 0 {
		return 0.0
	}
	hourlyRate := float64(hourlyViews) * 24
	dailyRate := float64(dailyViews)
	weeklyRate := float64(weeklyViews) / 7
	var velocity float64
	if weeklyRate > 0 {
		velocity = (dailyRate - weeklyRate) / weeklyRate * 100
	}
	if hourlyRate > dailyRate*0.1 {
		velocity += (hourlyRate - dailyRate*0.1) / dailyRate * 50
	}
	if velocity > 1000.0 {
		velocity = 1000.0
	}
	if velocity < 0 {
		velocity = 0
	}
	return velocity
}

func (s *Service) GetTopVideos(ctx context.Context, days, limit int) ([]struct {
	VideoID     string `json:"video_id"`
	TotalViews  int64  `json:"total_views"`
	UniqueViews int64  `json:"unique_views"`
}, error) {
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -days)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	dbResults, err := s.viewsRepo.GetTopVideos(ctx, startDate, endDate, limit)
	if err != nil {
		return nil, err
	}
	results := make([]struct {
		VideoID     string `json:"video_id"`
		TotalViews  int64  `json:"total_views"`
		UniqueViews int64  `json:"unique_views"`
	}, len(dbResults))
	for i, dbResult := range dbResults {
		results[i] = struct {
			VideoID     string `json:"video_id"`
			TotalViews  int64  `json:"total_views"`
			UniqueViews int64  `json:"unique_views"`
		}{VideoID: dbResult.VideoID, TotalViews: dbResult.TotalViews, UniqueViews: dbResult.UniqueViews}
	}
	return results, nil
}

func (s *Service) GetViewHistory(ctx context.Context, filter *domain.ViewAnalyticsFilter) ([]domain.UserView, error) {
	if filter.Limit <= 0 || filter.Limit > 1000 {
		filter.Limit = 100
	}
	return s.viewsRepo.GetViewsByDateRange(ctx, filter)
}

func (s *Service) AggregateStats(ctx context.Context, date *time.Time) error {
	aggregateDate := time.Now().AddDate(0, 0, -1)
	if date != nil {
		aggregateDate = *date
	}
	return s.viewsRepo.AggregateDailyStats(ctx, aggregateDate)
}

func (s *Service) CleanupOldData(ctx context.Context, daysToKeep int) error {
	if daysToKeep <= 0 {
		daysToKeep = 365
	}
	return s.viewsRepo.CleanupOldViews(ctx, daysToKeep)
}

func GenerateFingerprint(ip, userAgent string) string {
	hash := sha256.Sum256([]byte(ip + "|" + userAgent))
	return fmt.Sprintf("%x", hash[:16])
}

func ValidateTrackingRequest(request *domain.ViewTrackingRequest) error {
	if request.VideoID == "" {
		return fmt.Errorf("video_id is required")
	}
	if request.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	if request.FingerprintHash == "" {
		return fmt.Errorf("fingerprint_hash is required")
	}
	if request.CompletionPercentage < 0 || request.CompletionPercentage > 100 {
		return fmt.Errorf("completion_percentage must be between 0 and 100")
	}
	if request.WatchDuration < 0 {
		return fmt.Errorf("watch_duration must be non-negative")
	}
	if request.VideoDuration < 0 {
		return fmt.Errorf("video_duration must be non-negative")
	}
	if request.SeekCount < 0 || request.PauseCount < 0 || request.ReplayCount < 0 || request.QualityChanges < 0 || request.BufferEvents < 0 {
		return fmt.Errorf("interaction counts must be non-negative")
	}
	return nil
}

func stringPtrIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
