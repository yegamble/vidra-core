// Package httpapi wires the Echo HTTP server: routing, middleware, and
// request/response handling. Handlers are intentionally thin; application
// logic lives in service packages.
package httpapi

import (
	"context"

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
	echo *echo.Echo
	cfg  *config.Config
	db   Pinger
	rdb  Pinger
}

// New constructs the HTTP server with middleware and routes registered. db and
// rdb may be nil (e.g. in unit tests); readiness reports them as unconfigured.
func New(cfg *config.Config, db, rdb Pinger) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: cfg.CORSAllowedOrigins,
		AllowMethods: []string{echo.GET, echo.POST, echo.PUT, echo.PATCH, echo.DELETE, echo.OPTIONS},
	}))

	s := &Server{echo: e, cfg: cfg, db: db, rdb: rdb}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.echo.GET("/healthz", s.handleLive)
	s.echo.GET("/readyz", s.handleReady)

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
