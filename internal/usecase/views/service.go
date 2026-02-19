package views

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
	viewsRepo    port.ViewsRepository
	videoRepo    port.VideoRepository
	cacheRepo    port.CacheRepository
	viewQueue    chan viewTask
	workerWG     sync.WaitGroup
	closeOnce    sync.Once
	viewCounts   map[string]int64
	viewCountsMu sync.Mutex
	flushStop    chan struct{}
	flushWG      sync.WaitGroup
}

const defaultViewWorkers = 4
const viewFlushInterval = 10 * time.Second

func NewService(viewsRepo port.ViewsRepository, videoRepo port.VideoRepository) *Service {
	return NewServiceWithWorkers(viewsRepo, videoRepo, defaultViewWorkers)
}

func NewServiceWithWorkers(viewsRepo port.ViewsRepository, videoRepo port.VideoRepository, numWorkers int) *Service {
	if numWorkers < 1 {
		numWorkers = defaultViewWorkers
	}
	s := &Service{
		viewsRepo:  viewsRepo,
		videoRepo:  videoRepo,
		viewQueue:  make(chan viewTask, 1000),
		viewCounts: make(map[string]int64),
		flushStop:  make(chan struct{}),
	}
	s.workerWG.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go s.worker()
	}
	s.flushWG.Add(1)
	go s.periodicFlush()
	return s
}

func (s *Service) Close() {
	s.closeOnce.Do(func() {
		close(s.flushStop)
		close(s.viewQueue)
	})
	s.workerWG.Wait()
	s.flushWG.Wait()
	s.flushViewCounts()
}

// SetCacheRepository sets the cache repository for the service
func (s *Service) SetCacheRepository(repo port.CacheRepository) {
	s.cacheRepo = repo
}

func (s *Service) periodicFlush() {
	defer s.flushWG.Done()
	ticker := time.NewTicker(viewFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.flushViewCounts()
		case <-s.flushStop:
			return
		}
	}
}

func (s *Service) flushViewCounts() {
	s.viewCountsMu.Lock()
	if len(s.viewCounts) == 0 {
		s.viewCountsMu.Unlock()
		return
	}
	counts := s.viewCounts
	s.viewCounts = make(map[string]int64)
	s.viewCountsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = s.viewsRepo.BatchIncrementVideoViews(ctx, counts)
}

func (s *Service) bufferViewIncrement(videoID string) {
	s.viewCountsMu.Lock()
	s.viewCounts[videoID]++
	s.viewCountsMu.Unlock()
}

func (s *Service) worker() {
	defer s.workerWG.Done()
	for task := range s.viewQueue {
		s.processViewTask(task)
	}
}

func (s *Service) processViewTask(task viewTask) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	request := task.request
	userID := task.userID

	var existingView *domain.UserView
	var err error
	cacheKey := fmt.Sprintf("view:%s:%s", request.SessionID, request.VideoID)

	// Try cache
	if s.cacheRepo != nil {
		val, err := s.cacheRepo.Get(ctx, cacheKey)
		if err == nil && val != "" {
			var cachedView domain.UserView
			if err := json.Unmarshal([]byte(val), &cachedView); err == nil {
				existingView = &cachedView
			}
		}
	}

	// Fallback to DB if not found in cache
	if existingView == nil {
		existingView, err = s.viewsRepo.GetUserViewBySessionAndVideo(ctx, request.SessionID, request.VideoID)
		if err != nil {
			return
		}
	}

	now := time.Now()

	if existingView != nil {
		existingView.WatchDuration = request.WatchDuration
		existingView.CompletionPercentage = request.CompletionPercentage
		existingView.IsCompleted = request.IsCompleted
		existingView.SeekCount = request.SeekCount
		existingView.PauseCount = request.PauseCount
		existingView.ReplayCount = request.ReplayCount
		existingView.QualityChanges = request.QualityChanges
		existingView.BufferEvents = request.BufferEvents

		_ = s.viewsRepo.UpdateUserView(ctx, existingView)

		// Update cache
		if s.cacheRepo != nil {
			if data, err := json.Marshal(existingView); err == nil {
				_ = s.cacheRepo.Set(ctx, cacheKey, data, 1*time.Hour)
			}
		}
		return
	}

	view := &domain.UserView{
		VideoID:         request.VideoID,
		UserID:          userID,
		SessionID:       request.SessionID,
		FingerprintHash: request.FingerprintHash,

		WatchDuration:        request.WatchDuration,
		VideoDuration:        request.VideoDuration,
		CompletionPercentage: request.CompletionPercentage,
		IsCompleted:          request.IsCompleted,

		SeekCount:      request.SeekCount,
		PauseCount:     request.PauseCount,
		ReplayCount:    request.ReplayCount,
		QualityChanges: request.QualityChanges,

		InitialLoadTime: request.InitialLoadTime,
		BufferEvents:    request.BufferEvents,
		ConnectionType:  request.ConnectionType,
		VideoQuality:    stringPtrIfNotEmpty(request.VideoQuality),

		ReferrerURL:  request.ReferrerURL,
		ReferrerType: request.ReferrerType,
		UTMSource:    request.UTMSource,
		UTMMedium:    request.UTMMedium,
		UTMCampaign:  request.UTMCampaign,

		DeviceType:       request.DeviceType,
		OSName:           request.OSName,
		BrowserName:      request.BrowserName,
		ScreenResolution: request.ScreenResolution,
		IsMobile:         request.IsMobile,

		CountryCode: request.CountryCode,
		RegionCode:  request.RegionCode,
		CityName:    request.CityName,
		Timezone:    request.Timezone,

		IsAnonymous:     request.IsAnonymous,
		TrackingConsent: request.TrackingConsent,
		GDPRConsent:     request.GDPRConsent,

		ViewDate: now.Truncate(24 * time.Hour),
		ViewHour: now.Hour(),
		Weekday:  int(now.Weekday()),

		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.viewsRepo.CreateUserView(ctx, view); err != nil {
		return
	}

	// Update cache with new view
	if s.cacheRepo != nil {
		if data, err := json.Marshal(view); err == nil {
			_ = s.cacheRepo.Set(ctx, cacheKey, data, 1*time.Hour)
		}
	}

	s.bufferViewIncrement(request.VideoID)
}

func (s *Service) TrackView(ctx context.Context, userID *string, request *domain.ViewTrackingRequest) error {
	video, err := s.videoRepo.GetByID(ctx, request.VideoID)
	if err != nil {
		return fmt.Errorf("failed to verify video exists: %w", err)
	}
	if video == nil {
		return fmt.Errorf("video not found: %s", request.VideoID)
	}

	select {
	case s.viewQueue <- viewTask{userID: userID, request: request}:
		return nil
	default:
		return fmt.Errorf("tracking queue full")
	}
}

func (s *Service) GetVideoAnalytics(ctx context.Context, filter *domain.ViewAnalyticsFilter) (*domain.ViewAnalyticsResponse, error) {
	return s.viewsRepo.GetVideoAnalytics(ctx, filter)
}

func (s *Service) GetDailyStats(ctx context.Context, videoID string, days int) ([]domain.DailyVideoStats, error) {
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -days)
	return s.viewsRepo.GetDailyVideoStats(ctx, videoID, startDate, endDate)
}

func (s *Service) GetUserEngagement(ctx context.Context, userID string, days int) ([]domain.UserEngagementStats, error) {
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -days)
	return s.viewsRepo.GetUserEngagementStats(ctx, userID, startDate, endDate)
}

func (s *Service) GetTrendingVideos(ctx context.Context, limit int) ([]domain.TrendingVideo, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.viewsRepo.GetTrendingVideos(ctx, limit)
}

func (s *Service) GetTrendingVideosWithDetails(ctx context.Context, limit int) (*domain.TrendingVideosResponse, error) {
	trendingVideos, err := s.GetTrendingVideos(ctx, limit)
	if err != nil {
		return nil, err
	}

	videoIDs := make([]string, 0, len(trendingVideos))
	for _, tv := range trendingVideos {
		videoIDs = append(videoIDs, tv.VideoID)
	}

	videos, err := s.videoRepo.GetByIDs(ctx, videoIDs)
	if err != nil {
		return nil, err
	}

	videoMap := make(map[string]*domain.Video)
	for _, v := range videos {
		videoMap[v.ID] = v
	}

	response := &domain.TrendingVideosResponse{Videos: make([]domain.TrendingVideoWithDetails, 0, len(trendingVideos)), UpdatedAt: time.Now()}
	for _, tv := range trendingVideos {
		video, exists := videoMap[tv.VideoID]
		if !exists || video == nil {
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

func (s *Service) UpdateTrendingMetrics(ctx context.Context, videoIDs []string) error {
	now := time.Now()

	stats, err := s.viewsRepo.GetBatchTrendingStats(ctx, videoIDs)
	if err != nil {
		return err
	}

	trendingVideos := make([]*domain.TrendingVideo, 0, len(stats))
	for _, stat := range stats {
		velocityScore := calculateVelocityScore(stat.ViewsLastHour, stat.ViewsLast24h, stat.ViewsLast7d)
		isTrending := stat.Score24h > 100.0 || (stat.Score1h > 50.0 && velocityScore > 10.0) || stat.Score7d > 200.0
		tv := &domain.TrendingVideo{
			VideoID:         stat.VideoID,
			ViewsLastHour:   stat.ViewsLastHour,
			ViewsLast24h:    stat.ViewsLast24h,
			ViewsLast7d:     stat.ViewsLast7d,
			EngagementScore: stat.Score24h,
			VelocityScore:   velocityScore,
			LastUpdated:     now,
			IsTrending:      isTrending,
		}
		trendingVideos = append(trendingVideos, tv)
	}

	if len(trendingVideos) > 0 {
		return s.viewsRepo.BatchUpdateTrendingVideos(ctx, trendingVideos)
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
