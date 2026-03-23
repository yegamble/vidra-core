package video

import (
	redis "github.com/redis/go-redis/v9"

	"vidra-core/internal/config"
	"vidra-core/internal/livestream"
	"vidra-core/internal/repository"
	"vidra-core/internal/scheduler"
	"vidra-core/internal/usecase"
	"vidra-core/internal/usecase/encoding"
	"vidra-core/internal/usecase/upload"
	"vidra-core/internal/usecase/views"
)

// VideoHandlers holds dependencies for video-related handlers
type VideoHandlers struct {
	videoRepo         usecase.VideoRepository
	uploadRepo        usecase.UploadRepository
	encodingRepo      usecase.EncodingRepository
	viewsRepo         *repository.ViewsRepository
	captionRepo       usecase.CaptionRepository
	uploadService     upload.Service
	encodingService   encoding.Service
	viewsService      *views.Service
	importService     any
	streamManager     *livestream.StreamManager
	encodingScheduler *scheduler.EncodingScheduler
	redis             *redis.Client
	jwtSecret         string
	cfg               *config.Config
}

// NewVideoHandlers creates a new video handlers instance
func NewVideoHandlers(
	videoRepo usecase.VideoRepository,
	uploadRepo usecase.UploadRepository,
	encodingRepo usecase.EncodingRepository,
	viewsRepo *repository.ViewsRepository,
	captionRepo usecase.CaptionRepository,
	uploadService upload.Service,
	encodingService encoding.Service,
	viewsService *views.Service,
	importService any,
	streamManager *livestream.StreamManager,
	encodingScheduler *scheduler.EncodingScheduler,
	redisClient *redis.Client,
	jwtSecret string,
	cfg *config.Config,
) *VideoHandlers {
	return &VideoHandlers{
		videoRepo:         videoRepo,
		uploadRepo:        uploadRepo,
		encodingRepo:      encodingRepo,
		viewsRepo:         viewsRepo,
		captionRepo:       captionRepo,
		uploadService:     uploadService,
		encodingService:   encodingService,
		viewsService:      viewsService,
		importService:     importService,
		streamManager:     streamManager,
		encodingScheduler: encodingScheduler,
		redis:             redisClient,
		jwtSecret:         jwtSecret,
		cfg:               cfg,
	}
}
