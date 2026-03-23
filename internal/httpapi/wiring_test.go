package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"vidra-core/internal/chat"
	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/plugin"
	"vidra-core/internal/repository"
	"vidra-core/internal/usecase"
	"vidra-core/internal/usecase/captiongen"
	importuc "vidra-core/internal/usecase/import"
)

type captionGenStub struct{}

func (c *captionGenStub) Run(_ context.Context, _ int) error          { return nil }
func (c *captionGenStub) ProcessNext(_ context.Context) (bool, error) { return false, nil }
func (c *captionGenStub) CreateJob(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ *domain.CreateCaptionGenerationJobRequest) (*domain.CaptionGenerationJob, error) {
	return nil, nil
}
func (c *captionGenStub) RegenerateCaption(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ *string) (*domain.CaptionGenerationJob, error) {
	return nil, nil
}
func (c *captionGenStub) GetJobStatus(_ context.Context, _ uuid.UUID) (*domain.CaptionGenerationJob, error) {
	return nil, nil
}
func (c *captionGenStub) GetJobsByVideo(_ context.Context, _ uuid.UUID) ([]domain.CaptionGenerationJob, error) {
	return nil, nil
}

var _ captiongen.Service = (*captionGenStub)(nil)

type stubChatRepo struct{}

func (s *stubChatRepo) CreateMessage(ctx context.Context, msg *domain.ChatMessage) error {
	return nil
}
func (s *stubChatRepo) GetMessages(ctx context.Context, streamID uuid.UUID, limit, offset int) ([]*domain.ChatMessage, error) {
	return nil, nil
}
func (s *stubChatRepo) GetMessagesSince(ctx context.Context, streamID uuid.UUID, since time.Time) ([]*domain.ChatMessage, error) {
	return nil, nil
}
func (s *stubChatRepo) DeleteMessage(ctx context.Context, messageID uuid.UUID) error { return nil }
func (s *stubChatRepo) GetMessageByID(ctx context.Context, messageID uuid.UUID) (*domain.ChatMessage, error) {
	return nil, nil
}
func (s *stubChatRepo) AddModerator(ctx context.Context, mod *domain.ChatModerator) error {
	return nil
}
func (s *stubChatRepo) RemoveModerator(ctx context.Context, streamID, userID uuid.UUID) error {
	return nil
}
func (s *stubChatRepo) IsModerator(ctx context.Context, streamID, userID uuid.UUID) (bool, error) {
	return false, nil
}
func (s *stubChatRepo) GetModerators(ctx context.Context, streamID uuid.UUID) ([]*domain.ChatModerator, error) {
	return nil, nil
}
func (s *stubChatRepo) BanUser(ctx context.Context, ban *domain.ChatBan) error { return nil }
func (s *stubChatRepo) UnbanUser(ctx context.Context, streamID, userID uuid.UUID) error {
	return nil
}
func (s *stubChatRepo) IsUserBanned(ctx context.Context, streamID, userID uuid.UUID) (bool, error) {
	return false, nil
}
func (s *stubChatRepo) GetBans(ctx context.Context, streamID uuid.UUID) ([]*domain.ChatBan, error) {
	return nil, nil
}
func (s *stubChatRepo) GetBanByID(ctx context.Context, banID uuid.UUID) (*domain.ChatBan, error) {
	return nil, nil
}
func (s *stubChatRepo) CleanupExpiredBans(ctx context.Context) (int, error) { return 0, nil }
func (s *stubChatRepo) GetStreamStats(ctx context.Context, streamID uuid.UUID) (*domain.ChatStreamStats, error) {
	return nil, nil
}
func (s *stubChatRepo) GetMessageCount(ctx context.Context, streamID uuid.UUID) (int, error) {
	return 0, nil
}

type stubRedundancyService struct{}

func (s *stubRedundancyService) ListInstancePeers(_ context.Context, _, _ int, _ bool) ([]*domain.InstancePeer, error) {
	return nil, nil
}
func (s *stubRedundancyService) RegisterInstancePeer(_ context.Context, _ *domain.InstancePeer) error {
	return nil
}
func (s *stubRedundancyService) GetInstancePeer(_ context.Context, _ string) (*domain.InstancePeer, error) {
	return nil, nil
}
func (s *stubRedundancyService) UpdateInstancePeer(_ context.Context, _ *domain.InstancePeer) error {
	return nil
}
func (s *stubRedundancyService) DeleteInstancePeer(_ context.Context, _ string) error { return nil }
func (s *stubRedundancyService) CreateRedundancy(_ context.Context, _, _ string, _ domain.RedundancyStrategy, _ int) (*domain.VideoRedundancy, error) {
	return nil, nil
}
func (s *stubRedundancyService) GetRedundancy(_ context.Context, _ string) (*domain.VideoRedundancy, error) {
	return nil, nil
}
func (s *stubRedundancyService) ListVideoRedundancies(_ context.Context, _ string) ([]*domain.VideoRedundancy, error) {
	return nil, nil
}
func (s *stubRedundancyService) CancelRedundancy(_ context.Context, _ string) error { return nil }
func (s *stubRedundancyService) DeleteRedundancy(_ context.Context, _ string) error { return nil }
func (s *stubRedundancyService) SyncRedundancy(_ context.Context, _ string) error   { return nil }
func (s *stubRedundancyService) ListPolicies(_ context.Context, _ bool) ([]*domain.RedundancyPolicy, error) {
	return nil, nil
}
func (s *stubRedundancyService) CreatePolicy(_ context.Context, _ *domain.RedundancyPolicy) error {
	return nil
}
func (s *stubRedundancyService) GetPolicy(_ context.Context, _ string) (*domain.RedundancyPolicy, error) {
	return nil, nil
}
func (s *stubRedundancyService) UpdatePolicy(_ context.Context, _ *domain.RedundancyPolicy) error {
	return nil
}
func (s *stubRedundancyService) DeletePolicy(_ context.Context, _ string) error { return nil }
func (s *stubRedundancyService) EvaluatePolicies(_ context.Context) (int, error) {
	return 0, nil
}
func (s *stubRedundancyService) GetStats(_ context.Context) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubRedundancyService) GetVideoHealth(_ context.Context, _ string) (float64, error) {
	return 0, nil
}

type stubInstanceDiscovery struct{}

func (s *stubInstanceDiscovery) DiscoverInstance(_ context.Context, _ string) (*domain.InstancePeer, error) {
	return nil, nil
}

type stubVideoCategoryUseCase struct{}

func (s *stubVideoCategoryUseCase) CreateCategory(_ context.Context, _ uuid.UUID, _ *domain.CreateVideoCategoryRequest) (*domain.VideoCategory, error) {
	return nil, nil
}
func (s *stubVideoCategoryUseCase) GetCategoryByID(_ context.Context, _ uuid.UUID) (*domain.VideoCategory, error) {
	return nil, nil
}
func (s *stubVideoCategoryUseCase) GetCategoryBySlug(_ context.Context, _ string) (*domain.VideoCategory, error) {
	return nil, nil
}
func (s *stubVideoCategoryUseCase) ListCategories(_ context.Context, _ domain.VideoCategoryListOptions) ([]*domain.VideoCategory, error) {
	return nil, nil
}
func (s *stubVideoCategoryUseCase) UpdateCategory(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ *domain.UpdateVideoCategoryRequest) error {
	return nil
}
func (s *stubVideoCategoryUseCase) DeleteCategory(_ context.Context, _ uuid.UUID, _ uuid.UUID) error {
	return nil
}
func (s *stubVideoCategoryUseCase) GetDefaultCategory(_ context.Context) (*domain.VideoCategory, error) {
	return nil, nil
}

type stubAnalyticsRepo struct{}

func (s *stubAnalyticsRepo) CreateAnalytics(_ context.Context, _ *domain.StreamAnalytics) error {
	return nil
}
func (s *stubAnalyticsRepo) GetAnalyticsByStream(_ context.Context, _ uuid.UUID, _ *domain.AnalyticsTimeRange) ([]*domain.StreamAnalytics, error) {
	return nil, nil
}
func (s *stubAnalyticsRepo) GetAnalyticsTimeSeries(_ context.Context, _ uuid.UUID, _ *domain.AnalyticsTimeRange) ([]*domain.AnalyticsDataPoint, error) {
	return nil, nil
}
func (s *stubAnalyticsRepo) GetLatestAnalytics(_ context.Context, _ uuid.UUID) (*domain.StreamAnalytics, error) {
	return nil, nil
}
func (s *stubAnalyticsRepo) GetStreamSummary(_ context.Context, _ uuid.UUID) (*domain.StreamStatsSummary, error) {
	return nil, nil
}
func (s *stubAnalyticsRepo) UpdateStreamSummary(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubAnalyticsRepo) CreateOrUpdateSummary(_ context.Context, _ *domain.StreamStatsSummary) error {
	return nil
}
func (s *stubAnalyticsRepo) CreateViewerSession(_ context.Context, _ *domain.AnalyticsViewerSession) error {
	return nil
}
func (s *stubAnalyticsRepo) EndViewerSession(_ context.Context, _ string) error { return nil }
func (s *stubAnalyticsRepo) GetActiveViewers(_ context.Context, _ uuid.UUID) ([]*domain.AnalyticsViewerSession, error) {
	return nil, nil
}
func (s *stubAnalyticsRepo) GetViewerSession(_ context.Context, _ string) (*domain.AnalyticsViewerSession, error) {
	return nil, nil
}
func (s *stubAnalyticsRepo) UpdateSessionEngagement(_ context.Context, _ string, _ int, _, _ bool) error {
	return nil
}
func (s *stubAnalyticsRepo) CleanupOldAnalytics(_ context.Context, _ int) error { return nil }
func (s *stubAnalyticsRepo) GetCurrentViewerCount(_ context.Context, _ uuid.UUID) (int, error) {
	return 0, nil
}
func (s *stubAnalyticsRepo) GetActiveViewersForStreams(_ context.Context, _ []uuid.UUID) (map[uuid.UUID][]*domain.AnalyticsViewerSession, error) {
	return nil, nil
}
func (s *stubAnalyticsRepo) GetCurrentViewerCounts(_ context.Context, _ []uuid.UUID) (map[uuid.UUID]int, error) {
	return nil, nil
}
func (s *stubAnalyticsRepo) BatchCreateAnalytics(_ context.Context, _ []*domain.StreamAnalytics) error {
	return nil
}
func (s *stubAnalyticsRepo) BatchUpdateStreamSummaries(_ context.Context, _ []uuid.UUID) error {
	return nil
}

type stubAnalyticsCollector struct{}

func (s *stubAnalyticsCollector) TrackViewerJoin(_ context.Context, _ uuid.UUID, _ *uuid.UUID, _, _, _ string) error {
	return nil
}
func (s *stubAnalyticsCollector) TrackViewerLeave(_ context.Context, _ string) error { return nil }
func (s *stubAnalyticsCollector) TrackEngagement(_ context.Context, _ string, _ int, _, _ bool) error {
	return nil
}

type stubImportService struct{}

func (s *stubImportService) ImportVideo(_ context.Context, _ *importuc.ImportRequest) (*domain.VideoImport, error) {
	return nil, nil
}

func (s *stubImportService) CancelImport(_ context.Context, _, _ string) error { return nil }

func (s *stubImportService) RetryImport(_ context.Context, _, _ string) error { return nil }

func (s *stubImportService) GetImport(_ context.Context, _, _ string) (*domain.VideoImport, error) {
	return nil, nil
}

func (s *stubImportService) ListUserImports(_ context.Context, _ string, _, _ int) ([]*domain.VideoImport, int, error) {
	return nil, 0, nil
}

func (s *stubImportService) ProcessPendingImports(_ context.Context) error { return nil }

func (s *stubImportService) CleanupOldImports(_ context.Context, _ int) (int64, error) {
	return 0, nil
}

type stubLiveStreamRepo struct{}

func (s *stubLiveStreamRepo) Create(ctx context.Context, stream *domain.LiveStream) error {
	return nil
}
func (s *stubLiveStreamRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error) {
	return nil, nil
}
func (s *stubLiveStreamRepo) GetByStreamKey(ctx context.Context, streamKey string) (*domain.LiveStream, error) {
	return nil, nil
}
func (s *stubLiveStreamRepo) GetByChannelID(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, nil
}
func (s *stubLiveStreamRepo) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, nil
}
func (s *stubLiveStreamRepo) GetActiveStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, nil
}
func (s *stubLiveStreamRepo) CountByChannelID(ctx context.Context, channelID uuid.UUID) (int, error) {
	return 0, nil
}
func (s *stubLiveStreamRepo) Update(ctx context.Context, stream *domain.LiveStream) error {
	return nil
}
func (s *stubLiveStreamRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	return nil
}
func (s *stubLiveStreamRepo) UpdateViewerCount(ctx context.Context, id uuid.UUID, count int) error {
	return nil
}
func (s *stubLiveStreamRepo) Delete(ctx context.Context, id uuid.UUID) error    { return nil }
func (s *stubLiveStreamRepo) EndStream(ctx context.Context, id uuid.UUID) error { return nil }
func (s *stubLiveStreamRepo) GetChannelByStreamID(ctx context.Context, streamID uuid.UUID) (*domain.Channel, error) {
	return nil, nil
}
func (s *stubLiveStreamRepo) UpdateWaitingRoom(ctx context.Context, streamID uuid.UUID, enabled bool, message string) error {
	return nil
}
func (s *stubLiveStreamRepo) ScheduleStream(ctx context.Context, streamID uuid.UUID, scheduledStart *time.Time, scheduledEnd *time.Time, waitingRoomEnabled bool, waitingRoomMessage string) error {
	return nil
}
func (s *stubLiveStreamRepo) CancelSchedule(ctx context.Context, streamID uuid.UUID) error {
	return nil
}
func (s *stubLiveStreamRepo) GetScheduledStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, nil
}
func (s *stubLiveStreamRepo) GetUpcomingStreams(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.LiveStream, error) {
	return nil, nil
}

func collectRoutes(r chi.Router) []string {
	var routes []string
	_ = chi.Walk(r, func(method, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		routes = append(routes, method+" "+route)
		return nil
	})
	return routes
}

func hasRoute(routes []string, target string) bool {
	for _, route := range routes {
		if route == target {
			return true
		}
	}

	return false
}

func hasRoutePrefix(routes []string, prefix string) bool {
	for _, route := range routes {
		if strings.HasPrefix(route, prefix) {
			return true
		}
	}

	return false
}

func buildTestRouter(deps *shared.HandlerDependencies) chi.Router {
	cfg := &config.Config{
		JWTSecret:         "test-secret",
		RateLimitDuration: time.Minute,
		RateLimitRequests: 100,
	}
	r := chi.NewRouter()
	rlManager := middleware.NewRateLimiterManager()
	RegisterRoutesWithDependencies(r, cfg, rlManager, deps)
	return r
}

func TestChatRoutesRegistered_WhenChatServerSet(t *testing.T) {
	chatRepo := &stubChatRepo{}
	streamRepo := &stubLiveStreamRepo{}
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)

	chatServer := chat.NewChatServer(&config.Config{}, chatRepo, streamRepo, nil, logger)

	deps := &shared.HandlerDependencies{
		JWTSecret:        "test-secret",
		RedisPingTimeout: time.Second,
		ChatServer:       chatServer,
		ChatRepo:         chatRepo,
	}

	r := buildTestRouter(deps)
	routes := collectRoutes(r)

	hasChatMessages := false
	hasChatWS := false
	for _, route := range routes {
		if strings.Contains(route, "/chat/messages") {
			hasChatMessages = true
		}
		if strings.Contains(route, "/chat/ws") {
			hasChatWS = true
		}
	}

	assert.True(t, hasChatMessages, "GET /streams/{streamId}/chat/messages route should be registered")
	assert.True(t, hasChatWS, "GET /streams/{streamId}/chat/ws route should be registered")
}

func TestChatRoutesNotRegistered_WhenChatServerNil(t *testing.T) {
	deps := &shared.HandlerDependencies{
		JWTSecret:        "test-secret",
		RedisPingTimeout: time.Second,
	}

	r := buildTestRouter(deps)
	routes := collectRoutes(r)

	for _, route := range routes {
		assert.NotContains(t, route, "/chat/", "No chat routes should exist when ChatServer is nil")
	}
}

func TestPluginRoutesRegistered_WhenPluginManagerSet(t *testing.T) {
	deps := &shared.HandlerDependencies{
		JWTSecret:        "test-secret",
		RedisPingTimeout: time.Second,
		PluginRepo:       &repository.PluginRepository{},
		PluginManager:    plugin.NewManager(t.TempDir()),
	}

	r := buildTestRouter(deps)
	routes := collectRoutes(r)

	hasPluginList := false
	for _, route := range routes {
		if strings.Contains(route, "/admin/plugins") {
			hasPluginList = true
		}
	}

	assert.True(t, hasPluginList, "GET /admin/plugins route should be registered when PluginManager is set")
}

func TestPeerTubeCanonicalRoutesRegistered_WhenDependenciesSet(t *testing.T) {
	deps := &shared.HandlerDependencies{
		JWTSecret:        "test-secret",
		RedisPingTimeout: time.Second,
		RegistrationRepo: &repository.RegistrationRepository{},
		PluginRepo:       &repository.PluginRepository{},
		PluginManager:    plugin.NewManager(t.TempDir()),
		ImportService:    &stubImportService{},
	}

	r := buildTestRouter(deps)
	routes := collectRoutes(r)

	assert.True(t, hasRoute(routes, "GET /api/v1/users/registrations"))
	assert.True(t, hasRoute(routes, "POST /api/v1/users/registrations/{registrationId}/accept"))
	assert.True(t, hasRoute(routes, "GET /api/v1/jobs"))
	assert.True(t, hasRoute(routes, "GET /api/v1/jobs/{state}"))
	assert.True(t, hasRoute(routes, "POST /api/v1/videos/imports/{id}/cancel"))
	assert.True(t, hasRoute(routes, "POST /api/v1/videos/imports/{id}/retry"))
	assert.True(t, hasRoutePrefix(routes, "GET /api/v1/plugins/"))
	assert.True(t, hasRoute(routes, "GET /api/v1/plugins/{npmName}"))
	assert.True(t, hasRoute(routes, "GET /api/v1/video-channels/{channelHandle}/collaborators"))
	assert.True(t, hasRoute(routes, "POST /api/v1/runners/register"))
	assert.True(t, hasRoute(routes, "POST /api/v1/runners/jobs/request"))
}

func TestPeerTubeCanonicalRegistrationRoutesRegistered_WithoutRegistrationRepo(t *testing.T) {
	deps := &shared.HandlerDependencies{
		JWTSecret:        "test-secret",
		RedisPingTimeout: time.Second,
	}

	r := buildTestRouter(deps)
	routes := collectRoutes(r)

	assert.True(t, hasRoute(routes, "GET /api/v1/users/registrations"))
	assert.True(t, hasRoute(routes, "POST /api/v1/users/registrations/{registrationId}/accept"))
	assert.True(t, hasRoute(routes, "POST /api/v1/users/registrations/{registrationId}/reject"))
	assert.True(t, hasRoute(routes, "DELETE /api/v1/users/registrations/{registrationId}"))
}

func TestCaptionGenerationRoutesRegistered_WhenCaptionGenServiceSet(t *testing.T) {
	deps := &shared.HandlerDependencies{
		JWTSecret:         "test-secret",
		RedisPingTimeout:  time.Second,
		CaptionGenService: &captionGenStub{},
	}

	r := buildTestRouter(deps)
	routes := collectRoutes(r)

	hasGenerate := false
	hasJobs := false
	for _, route := range routes {
		if strings.Contains(route, "/captions/generate") {
			hasGenerate = true
		}
		if strings.Contains(route, "/captions/jobs") {
			hasJobs = true
		}
	}

	assert.True(t, hasGenerate, "POST /videos/{id}/captions/generate route should be registered")
	assert.True(t, hasJobs, "GET /videos/{id}/captions/jobs route should be registered")
}

func TestSocialRoutesRegistered_WhenSocialServiceSet(t *testing.T) {
	deps := &shared.HandlerDependencies{
		JWTSecret:        "test-secret",
		RedisPingTimeout: time.Second,
		SocialService:    &usecase.SocialService{},
	}

	r := buildTestRouter(deps)
	routes := collectRoutes(r)

	hasSocialFollow := false
	hasSocialLike := false
	for _, route := range routes {
		if strings.Contains(route, "/social/follow") {
			hasSocialFollow = true
		}
		if strings.Contains(route, "/social/like") {
			hasSocialLike = true
		}
	}

	assert.True(t, hasSocialFollow, "POST /social/follow route should be registered")
	assert.True(t, hasSocialLike, "POST /social/like route should be registered")
}

func TestSocialRoutesNotRegistered_WhenSocialServiceNil(t *testing.T) {
	deps := &shared.HandlerDependencies{
		JWTSecret:        "test-secret",
		RedisPingTimeout: time.Second,
	}

	r := buildTestRouter(deps)
	routes := collectRoutes(r)

	for _, route := range routes {
		assert.NotContains(t, route, "/social/", "No social routes should exist when SocialService is nil")
	}
}

func TestRedundancyRoutesRegistered_WhenRedundancyServiceSet(t *testing.T) {
	deps := &shared.HandlerDependencies{
		JWTSecret:         "test-secret",
		RedisPingTimeout:  time.Second,
		RedundancyService: &stubRedundancyService{},
		InstanceDiscovery: &stubInstanceDiscovery{},
	}

	r := buildTestRouter(deps)
	routes := collectRoutes(r)

	hasInstances := false
	hasPolicies := false
	hasRedundancyStats := false
	for _, route := range routes {
		if strings.Contains(route, "/admin/redundancy/instances") {
			hasInstances = true
		}
		if strings.Contains(route, "/admin/redundancy/policies") {
			hasPolicies = true
		}
		if strings.Contains(route, "/admin/redundancy/stats") {
			hasRedundancyStats = true
		}
	}

	assert.True(t, hasInstances, "GET /api/v1/admin/redundancy/instances route should be registered")
	assert.True(t, hasPolicies, "GET /api/v1/admin/redundancy/policies route should be registered")
	assert.True(t, hasRedundancyStats, "GET /api/v1/admin/redundancy/stats route should be registered")
}

func TestVideoCategoryRoutesRegistered_WhenUseCaseSet(t *testing.T) {
	deps := &shared.HandlerDependencies{
		JWTSecret:            "test-secret",
		RedisPingTimeout:     time.Second,
		VideoCategoryUseCase: &stubVideoCategoryUseCase{},
	}

	r := buildTestRouter(deps)
	routes := collectRoutes(r)

	hasListCategories := false
	hasAdminCategories := false
	for _, route := range routes {
		if strings.Contains(route, "GET /api/v1/categories") {
			hasListCategories = true
		}
		if strings.Contains(route, "/admin/categories") {
			hasAdminCategories = true
		}
	}

	assert.True(t, hasListCategories, "GET /api/v1/categories route should be registered")
	assert.True(t, hasAdminCategories, "Admin category routes should be registered")
}

func TestAnalyticsRoutesRegistered_WhenAnalyticsRepoSet(t *testing.T) {
	deps := &shared.HandlerDependencies{
		JWTSecret:          "test-secret",
		RedisPingTimeout:   time.Second,
		LiveStreamRepo:     &stubLiveStreamRepo{},
		ChannelRepo:        &repository.ChannelRepository{},
		AnalyticsRepo:      &stubAnalyticsRepo{},
		AnalyticsCollector: &stubAnalyticsCollector{},
	}

	r := buildTestRouter(deps)
	routes := collectRoutes(r)

	hasStreamAnalytics := false
	hasViewerJoin := false
	for _, route := range routes {
		if strings.Contains(route, "/analytics/") && strings.Contains(route, "streamId") {
			hasStreamAnalytics = true
		}
		if strings.Contains(route, "/analytics/viewer/join") {
			hasViewerJoin = true
		}
	}

	assert.True(t, hasStreamAnalytics, "GET /api/v1/streams/{streamId}/analytics routes should be registered")
	assert.True(t, hasViewerJoin, "POST /api/v1/analytics/viewer/join route should be registered")
}

func TestWaitingRoomRoutesRegistered_WhenLiveStreamRepoSet(t *testing.T) {
	streamRepo := &stubLiveStreamRepo{}

	deps := &shared.HandlerDependencies{
		JWTSecret:        "test-secret",
		RedisPingTimeout: time.Second,
		LiveStreamRepo:   streamRepo,
	}

	r := buildTestRouter(deps)
	routes := collectRoutes(r)

	hasWaitingRoom := false
	hasSchedule := false
	hasScheduled := false
	for _, route := range routes {
		if strings.Contains(route, "/waiting-room") {
			hasWaitingRoom = true
		}
		if strings.Contains(route, "/schedule") {
			hasSchedule = true
		}
		if strings.Contains(route, "/scheduled") {
			hasScheduled = true
		}
	}

	assert.True(t, hasWaitingRoom, "GET /streams/{streamId}/waiting-room route should be registered")
	assert.True(t, hasSchedule, "POST /streams/{streamId}/schedule route should be registered")
	assert.True(t, hasScheduled, "GET /streams/scheduled route should be registered")
}
