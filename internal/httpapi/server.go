// Package httpapi wires the Echo HTTP server: routing, middleware, and
// request/response handling. Handlers are intentionally thin; application
// logic lives in service packages.
package httpapi

import (
	"context"
	"log/slog"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/vidra/vidra-core/internal/config"
)

// Pinger is satisfied by dependencies that can report liveness (store, cache).
type Pinger interface {
	Ping(ctx context.Context) error
}

// Server holds the Echo instance and its dependencies.
type Server struct {
	echo   *echo.Echo
	cfg    *config.Config
	db     Pinger
	rdb    Pinger
	logger *slog.Logger
}

// New constructs the HTTP server with middleware and routes registered. db and
// rdb may be nil (e.g. in unit tests); readiness reports them as unconfigured.
// It uses the process-wide slog default logger for request and error logging.
func New(cfg *config.Config, db, rdb Pinger) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	s := &Server{echo: e, cfg: cfg, db: db, rdb: rdb, logger: slog.Default()}
	e.HTTPErrorHandler = s.httpErrorHandler

	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(s.requestLogger())
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: cfg.CORSAllowedOrigins,
		AllowMethods: []string{echo.GET, echo.POST, echo.PUT, echo.PATCH, echo.DELETE, echo.OPTIONS},
	}))
	e.Use(requestDeadline(cfg.HTTPRequestTimeout))
	e.Use(middleware.BodyLimit(cfg.HTTPBodyLimit))

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
	api.GET("/nodeinfo", s.handleNodeInfo)
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
