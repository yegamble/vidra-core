package usecase

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"athena/internal/domain"
)

type ViewsRepository interface {
	CreateUserView(ctx context.Context, view *domain.UserView) error
	UpdateUserView(ctx context.Context, view *domain.UserView) error
	GetUserViewBySessionAndVideo(ctx context.Context, sessionID, videoID string) (*domain.UserView, error)
	GetVideoAnalytics(ctx context.Context, filter *domain.ViewAnalyticsFilter) (*domain.ViewAnalyticsResponse, error)
	GetDailyVideoStats(ctx context.Context, videoID string, startDate, endDate time.Time) ([]domain.DailyVideoStats, error)
	GetUserEngagementStats(ctx context.Context, userID string, startDate, endDate time.Time) ([]domain.UserEngagementStats, error)
	GetTrendingVideos(ctx context.Context, limit int) ([]domain.TrendingVideo, error)
	UpdateTrendingVideo(ctx context.Context, trending *domain.TrendingVideo) error
	IncrementVideoViews(ctx context.Context, videoID string) error
	GetUniqueViews(ctx context.Context, videoID string, startDate, endDate time.Time) (int64, error)
	CalculateEngagementScore(ctx context.Context, videoID string, hoursBack int) (float64, error)
	AggregateDailyStats(ctx context.Context, date time.Time) error
	CleanupOldViews(ctx context.Context, daysToKeep int) error
	GetViewsByDateRange(ctx context.Context, filter *domain.ViewAnalyticsFilter) ([]domain.UserView, error)
	GetTopVideos(ctx context.Context, startDate, endDate time.Time, limit int) ([]struct {
		VideoID     string  `db:"video_id"`
		TotalViews  int64   `db:"total_views"`
		UniqueViews int64   `db:"unique_views"`
		AvgDuration float64 `db:"avg_duration"`
	}, error)
}

type ViewsService struct {
	viewsRepo ViewsRepository
	videoRepo VideoRepository
}

func NewViewsService(viewsRepo ViewsRepository, videoRepo VideoRepository) *ViewsService {
	return &ViewsService{
		viewsRepo: viewsRepo,
		videoRepo: videoRepo,
	}
}

// TrackView tracks a user view with deduplication and session management
func (s *ViewsService) TrackView(ctx context.Context, userID *string, request *domain.ViewTrackingRequest) error {
	// Validate that the video exists
	video, err := s.videoRepo.GetByID(ctx, request.VideoID)
	if err != nil {
		return fmt.Errorf("failed to verify video exists: %w", err)
	}
	if video == nil {
		return fmt.Errorf("video not found: %s", request.VideoID)
	}

	// Check if this is an existing view session
	existingView, err := s.viewsRepo.GetUserViewBySessionAndVideo(ctx, request.SessionID, request.VideoID)
	if err != nil {
		return fmt.Errorf("failed to check existing view: %w", err)
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

		err = s.viewsRepo.UpdateUserView(ctx, existingView)
		if err != nil {
			return fmt.Errorf("failed to update existing view: %w", err)
		}

		return nil
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
	err = s.viewsRepo.CreateUserView(ctx, view)
	if err != nil {
		return fmt.Errorf("failed to create user view: %w", err)
	}

	// Increment the video's total view count
	err = s.viewsRepo.IncrementVideoViews(ctx, request.VideoID)
	if err != nil {
		// Log this error but don't fail the tracking - it's a secondary operation
		// In a real application, you'd use proper logging here
		fmt.Printf("Warning: failed to increment video view count: %v\n", err)
	}

	return nil
}

// GetVideoAnalytics gets comprehensive analytics for a video
func (s *ViewsService) GetVideoAnalytics(ctx context.Context, filter *domain.ViewAnalyticsFilter) (*domain.ViewAnalyticsResponse, error) {
	return s.viewsRepo.GetVideoAnalytics(ctx, filter)
}

// GetDailyStats gets pre-aggregated daily statistics for a video
func (s *ViewsService) GetDailyStats(ctx context.Context, videoID string, days int) ([]domain.DailyVideoStats, error) {
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -days)

	return s.viewsRepo.GetDailyVideoStats(ctx, videoID, startDate, endDate)
}

// GetUserEngagement gets user-level engagement statistics
func (s *ViewsService) GetUserEngagement(ctx context.Context, userID string, days int) ([]domain.UserEngagementStats, error) {
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -days)

	return s.viewsRepo.GetUserEngagementStats(ctx, userID, startDate, endDate)
}

// GetTrendingVideos gets current trending videos
func (s *ViewsService) GetTrendingVideos(ctx context.Context, limit int) ([]domain.TrendingVideo, error) {
	if limit <= 0 || limit > 100 {
		limit = 50 // Default limit
	}

	return s.viewsRepo.GetTrendingVideos(ctx, limit)
}

// GetTrendingVideosWithDetails gets trending videos with full video details
func (s *ViewsService) GetTrendingVideosWithDetails(ctx context.Context, limit int) (*domain.TrendingVideosResponse, error) {
	trendingVideos, err := s.GetTrendingVideos(ctx, limit)
	if err != nil {
		return nil, err
	}

	response := &domain.TrendingVideosResponse{
		Videos:    make([]domain.TrendingVideoWithDetails, 0, len(trendingVideos)),
		UpdatedAt: time.Now(),
	}

	for _, trending := range trendingVideos {
		video, err := s.videoRepo.GetByID(ctx, trending.VideoID)
		if err != nil {
			// Log error but continue with other videos
			continue
		}
		if video == nil {
			continue
		}

		response.Videos = append(response.Videos, domain.TrendingVideoWithDetails{
			VideoID:         video.ID,
			Title:           video.Title,
			Description:     video.Description,
			ThumbnailURL:    video.ThumbnailPath,
			Duration:        video.Duration,
			Views:           video.Views,
			EngagementScore: trending.EngagementScore,
			VelocityScore:   trending.VelocityScore,
			CreatedAt:       video.CreatedAt,
			TrendingVideo:   &trending,
			Video:           video,
		})
	}

	return response, nil
}

// UpdateTrendingMetrics calculates and updates trending metrics for videos
func (s *ViewsService) UpdateTrendingMetrics(ctx context.Context, videoIDs []string) error {
	now := time.Now()

	for _, videoID := range videoIDs {
		// Calculate engagement scores for different time periods
		hourlyScore, err := s.viewsRepo.CalculateEngagementScore(ctx, videoID, 1)
		if err != nil {
			continue // Log error but continue with other videos
		}

		dailyScore, err := s.viewsRepo.CalculateEngagementScore(ctx, videoID, 24)
		if err != nil {
			continue
		}

		weeklyScore, err := s.viewsRepo.CalculateEngagementScore(ctx, videoID, 168) // 7 days
		if err != nil {
			continue
		}

		// Get view counts for different periods
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

		// Calculate velocity score (rate of change)
		velocityScore := calculateVelocityScore(viewsLastHour, viewsLast24h, viewsLast7d)

		// Determine if trending (threshold-based)
		isTrending := dailyScore > 100.0 || (hourlyScore > 50.0 && velocityScore > 10.0) || weeklyScore > 200.0

		trending := &domain.TrendingVideo{
			VideoID:         videoID,
			ViewsLastHour:   viewsLastHour,
			ViewsLast24h:    viewsLast24h,
			ViewsLast7d:     viewsLast7d,
			EngagementScore: dailyScore,
			VelocityScore:   velocityScore,
			LastUpdated:     now,
			IsTrending:      isTrending,
		}

		err = s.viewsRepo.UpdateTrendingVideo(ctx, trending)
		if err != nil {
			// Log error but continue
			fmt.Printf("Warning: failed to update trending video %s: %v\n", videoID, err)
		}
	}

	return nil
}

// getViewsInPeriod gets view count for a video in a specific time period
func (s *ViewsService) getViewsInPeriod(ctx context.Context, videoID string, period time.Duration) (int64, error) {
	endTime := time.Now()
	startTime := endTime.Add(-period)

	return s.viewsRepo.GetUniqueViews(ctx, videoID, startTime, endTime)
}

// calculateVelocityScore calculates trending velocity based on view patterns
func calculateVelocityScore(hourlyViews, dailyViews, weeklyViews int64) float64 {
	if weeklyViews == 0 {
		return 0.0
	}

	// Calculate view acceleration
	hourlyRate := float64(hourlyViews) * 24 // Project hourly to daily
	dailyRate := float64(dailyViews)
	weeklyRate := float64(weeklyViews) / 7 // Average daily from weekly

	// Velocity is the acceleration of views
	var velocity float64
	if weeklyRate > 0 {
		velocity = (dailyRate - weeklyRate) / weeklyRate * 100
	}

	// Boost for recent surge
	if hourlyRate > dailyRate*0.1 { // If current hour is significant
		velocity += (hourlyRate - dailyRate*0.1) / dailyRate * 50
	}

	// Cap velocity score
	if velocity > 1000.0 {
		velocity = 1000.0
	}
	if velocity < 0 {
		velocity = 0
	}

	return velocity
}

// GetTopVideos gets most viewed videos in a time period
func (s *ViewsService) GetTopVideos(ctx context.Context, days, limit int) ([]struct {
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

	// Convert from db struct to json struct
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
		}{
			VideoID:     dbResult.VideoID,
			TotalViews:  dbResult.TotalViews,
			UniqueViews: dbResult.UniqueViews,
		}
	}

	return results, nil
}

// GetViewHistory gets view history for a user or video
func (s *ViewsService) GetViewHistory(ctx context.Context, filter *domain.ViewAnalyticsFilter) ([]domain.UserView, error) {
	if filter.Limit <= 0 || filter.Limit > 1000 {
		filter.Limit = 100
	}

	return s.viewsRepo.GetViewsByDateRange(ctx, filter)
}

// AggregateStats triggers daily stats aggregation (typically called by cron job)
func (s *ViewsService) AggregateStats(ctx context.Context, date *time.Time) error {
	aggregateDate := time.Now().AddDate(0, 0, -1) // Yesterday by default
	if date != nil {
		aggregateDate = *date
	}

	return s.viewsRepo.AggregateDailyStats(ctx, aggregateDate)
}

// CleanupOldData removes old view data for privacy/performance (typically called by cron job)
func (s *ViewsService) CleanupOldData(ctx context.Context, daysToKeep int) error {
	if daysToKeep <= 0 {
		daysToKeep = 365 // Default to 1 year
	}

	return s.viewsRepo.CleanupOldViews(ctx, daysToKeep)
}

// GenerateFingerprint creates a privacy-compliant fingerprint for deduplication
func GenerateFingerprint(ip, userAgent string) string {
	// Create a hash to protect privacy while enabling deduplication
	hash := sha256.Sum256([]byte(ip + "|" + userAgent))
	return fmt.Sprintf("%x", hash[:16]) // Use first 16 bytes for efficiency
}

// ValidateTrackingRequest validates the incoming tracking request
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

	// Validate other counts are non-negative
	if request.SeekCount < 0 || request.PauseCount < 0 || request.ReplayCount < 0 ||
		request.QualityChanges < 0 || request.BufferEvents < 0 {
		return fmt.Errorf("interaction counts must be non-negative")
	}

	return nil
}

// stringPtrIfNotEmpty returns a pointer to string if s is not empty, otherwise nil
func stringPtrIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
