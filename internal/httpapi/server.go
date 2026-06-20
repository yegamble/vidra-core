// Package httpapi wires the Echo HTTP server: routing, middleware, and
// request/response handling. Handlers are intentionally thin; application
// logic lives in service packages.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/ratelimit"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/video"
)

// Pinger is satisfied by dependencies that can report liveness (store, cache).
type Pinger interface {
	Ping(ctx context.Context) error
}

// Server holds the Echo instance and its dependencies.
type Server struct {
	echo       *echo.Echo
	cfg        *config.Config
	db         Pinger
	rdb        Pinger
	logger     *slog.Logger
	limiter    *ratelimit.Limiter
	authsvc    *auth.Service
	authTTL    time.Duration
	channelsvc *channel.Service
	videosvc   *video.Service
	media      storage.Backend
}

// uploadRoutePath is the Echo route template for the original-file upload. It is
// exempted from the default body limit (which gets its own larger one).
const uploadRoutePath = "/api/v1/videos/:id/file"

// Option customises the Server during construction.
type Option func(*Server)

// WithRateLimiter mounts fixed-window rate limiting (per client IP) on the API
// surface. When nil or unset, no rate limiting is applied — handy for unit tests.
func WithRateLimiter(l *ratelimit.Limiter) Option {
	return func(s *Server) { s.limiter = l }
}

// WithAuthService mounts the auth endpoints (register/login). ttl is the access
// token lifetime, reported to clients as expires_in. When unset, the auth routes
// are not registered.
func WithAuthService(svc *auth.Service, ttl time.Duration) Option {
	return func(s *Server) {
		s.authsvc = svc
		s.authTTL = ttl
	}
}

// WithChannelService mounts the channel endpoints (create/list-own/get-by-handle).
// When unset, the channel routes are not registered.
func WithChannelService(svc *channel.Service) Option {
	return func(s *Server) { s.channelsvc = svc }
}

// WithVideoService mounts the video endpoints (create draft, get by id). Video
// creation also needs the channel service (for ownership); when either is unset
// the video routes are not registered.
func WithVideoService(svc *video.Service) Option {
	return func(s *Server) { s.videosvc = svc }
}

// WithMediaStorage gives the server the blob backend used to stream stored media
// (the original-file endpoint). It should be the same backend the video service
// writes uploads to. When unset, the streaming route serves 503.
func WithMediaStorage(b storage.Backend) Option {
	return func(s *Server) { s.media = b }
}

// WithLogger overrides the structured logger (default: slog.Default()). Used to
// route request/error/audit logs to a specific destination — and by tests to
// capture audit events.
func WithLogger(l *slog.Logger) Option {
	return func(s *Server) {
		if l != nil {
			s.logger = l
		}
	}
}

// New constructs the HTTP server with middleware and routes registered. db and
// rdb may be nil (e.g. in unit tests); readiness reports them as unconfigured.
// It uses the process-wide slog default logger for request and error logging.
func New(cfg *config.Config, db, rdb Pinger, opts ...Option) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	s := &Server{echo: e, cfg: cfg, db: db, rdb: rdb, logger: slog.Default()}
	for _, opt := range opts {
		opt(s)
	}
	e.HTTPErrorHandler = s.httpErrorHandler

	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(s.requestLogger())
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: cfg.CORSAllowedOrigins,
		AllowMethods: []string{echo.GET, echo.POST, echo.PUT, echo.PATCH, echo.DELETE, echo.OPTIONS},
	}))
	e.Use(requestDeadline(cfg.HTTPRequestTimeout))
	// The default body limit keeps the JSON API small. The original-file upload
	// route is exempted here and gets its own (larger) UploadMaxSize limit at
	// registration, so media uploads have headroom without widening the rest.
	e.Use(middleware.BodyLimitWithConfig(middleware.BodyLimitConfig{
		Skipper: func(c echo.Context) bool {
			return c.Request().Method == http.MethodPost && c.Path() == uploadRoutePath
		},
		Limit: cfg.HTTPBodyLimit,
	}))

	s.routes()
	return s
}

// requestLogger emits one structured slog line per request via Echo's
// RequestLogger middleware. Level escalates with status class so 5xx responses
// surface as errors. Request bodies and headers are never logged.
func (s *Server) requestLogger() echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus:    true,
		LogMethod:    true,
		LogURI:       true,
		LogLatency:   true,
		LogRequestID: true,
		LogError:     true,
		HandleError:  true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			level := slog.LevelInfo
			switch {
			case v.Status >= 500:
				level = slog.LevelError
			case v.Status >= 400:
				level = slog.LevelWarn
			}
			attrs := []any{
				"method", v.Method,
				"uri", v.URI,
				"status", v.Status,
				"latency_ms", v.Latency.Milliseconds(),
				"request_id", v.RequestID,
			}
			if v.Error != nil {
				attrs = append(attrs, "error", v.Error)
			}
			s.logger.Log(c.Request().Context(), level, "request", attrs...)
			return nil
		},
	})
}

// requestDeadline attaches a timeout to each request's context so handlers and
// the DB/Redis/outbound calls they make observe a deadline and abort cleanly.
// It does not forcibly interrupt a handler that ignores its context — the
// server's WriteTimeout is the hard backstop for that. Handlers that honour the
// context should return ctx.Err() (or a wrapped error), which the central error
// handler renders as a 503 envelope.
func requestDeadline(d time.Duration) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ctx, cancel := context.WithTimeout(c.Request().Context(), d)
			defer cancel()
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	}
}

func (s *Server) routes() {
	s.echo.GET("/healthz", s.handleLive)
	s.echo.GET("/readyz", s.handleReady)
	s.echo.GET("/version", s.handleVersion)

	api := s.echo.Group("/api/v1")
	// Rate limiting guards the API surface only; liveness/readiness/version are
	// exempt so orchestrator probes are never throttled.
	if s.limiter != nil {
		api.Use(s.rateLimit(s.limiter))
	}
	api.GET("/nodeinfo", s.handleNodeInfo)
	api.GET("/instance", s.handleInstance)

	if s.authsvc != nil {
		authGroup := api.Group("/auth")
		authGroup.POST("/register", s.handleRegister)
		authGroup.POST("/login", s.handleLogin)
		authGroup.POST("/refresh", s.handleRefresh)
		authGroup.POST("/logout", s.handleLogout)
		authGroup.POST("/password-reset", s.handleRequestPasswordReset)
		authGroup.POST("/password-reset/confirm", s.handleConfirmPasswordReset)
		authGroup.POST("/verify-email", s.handleRequestEmailVerification, s.requireAuth)
		authGroup.POST("/verify-email/confirm", s.handleConfirmEmailVerification)
		authGroup.GET("/me", s.handleMe, s.requireAuth)
		authGroup.PATCH("/me", s.handleUpdateMe, s.requireAuth)
		authGroup.POST("/me/deactivate", s.handleDeactivateAccount, s.requireAuth)
		authGroup.POST("/logout-all", s.handleLogoutAll, s.requireAuth)
	}

	if s.channelsvc != nil {
		api.POST("/channels", s.handleCreateChannel, s.requireAuth)
		api.GET("/channels/:handle", s.handleGetChannel)
		api.PATCH("/channels/:handle", s.handleUpdateChannel, s.requireAuth)
		api.DELETE("/channels/:handle", s.handleDeleteChannel, s.requireAuth)
		api.POST("/channels/:handle/follow", s.handleFollowChannel, s.requireAuth)
		api.DELETE("/channels/:handle/follow", s.handleUnfollowChannel, s.requireAuth)
		api.GET("/me/channels", s.handleListMyChannels, s.requireAuth)
	}

	// Video creation needs both the video and channel services (channel for
	// ownership); the public get applies optional auth so owners can see their
	// own private drafts.
	if s.videosvc != nil && s.channelsvc != nil {
		api.POST("/channels/:handle/videos", s.handleCreateVideo, s.requireAuth)
		api.GET("/channels/:handle/videos", s.handleListChannelVideos, s.optionalAuth)
		api.GET("/videos", s.handleListPublicVideos)
		api.GET("/videos/search", s.handleSearchVideos)
		api.GET("/videos/:id", s.handleGetVideo, s.optionalAuth)
		api.GET("/videos/:id/original", s.handleStreamVideoOriginal, s.optionalAuth)
		api.GET("/videos/:id/thumbnail", s.handleGetVideoThumbnail, s.optionalAuth)
		api.POST("/videos/:id/view", s.handleRecordVideoView, s.optionalAuth)
		api.PATCH("/videos/:id", s.handleUpdateVideo, s.requireAuth)
		api.DELETE("/videos/:id", s.handleDeleteVideo, s.requireAuth)
		api.POST("/videos/:id/file", s.handleUploadVideoFile, s.requireAuth, middleware.BodyLimit(s.cfg.UploadMaxSize))
	}
}

// Handler exposes the underlying http.Handler for tests.
func (s *Server) Handler() *echo.Echo { return s.echo }

// Start begins listening on the configured address. It blocks until the server
// is shut down.
func (s *Server) Start() error {
	return s.echo.Start(s.cfg.HTTPAddr())
}

// Shutdown gracefully drains in-flight requests, bounded by ctx.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.echo.Shutdown(ctx)
}
