package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// livenessResponse is returned by GET /healthz.
type livenessResponse struct {
	Status string `json:"status"`
}

// componentStatus reports the health of a single dependency.
type componentStatus struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// readinessResponse is returned by GET /readyz.
type readinessResponse struct {
	Status     string                     `json:"status"`
	Components map[string]componentStatus `json:"components"`
}

// handleLive reports that the process is up and serving. It performs no
// dependency checks so an orchestrator can distinguish "process alive" from
// "ready to serve traffic".
func (s *Server) handleLive(c echo.Context) error {
	return c.JSON(http.StatusOK, livenessResponse{Status: "ok"})
}

// handleReady reports whether all critical dependencies are reachable. Returns
// 503 if any configured dependency is unhealthy.
func (s *Server) handleReady(c echo.Context) error {
	ctx := c.Request().Context()
	components := map[string]componentStatus{}
	ready := true

	check := func(name string, p Pinger) {
		if p == nil {
			components[name] = componentStatus{Status: "not_configured"}
			return
		}
		if err := p.Ping(ctx); err != nil {
			ready = false
			components[name] = componentStatus{Status: "down", Error: err.Error()}
			return
		}
		components[name] = componentStatus{Status: "ok"}
	}

	check("postgres", s.db)
	check("redis", s.rdb)

	resp := readinessResponse{Components: components}
	if ready {
		resp.Status = "ok"
		return c.JSON(http.StatusOK, resp)
	}
	resp.Status = "degraded"
	return c.JSON(http.StatusServiceUnavailable, resp)
}

// nodeInfoResponse is a minimal NodeInfo-style discovery document. It will be
// expanded toward the NodeInfo 2.1 schema as federation lands (PT-REST-OPENAPI).
type nodeInfoResponse struct {
	Version  string `json:"version"`
	Software struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"software"`
	Instance struct {
		Name string `json:"name"`
	} `json:"instance"`
}

// handleNodeInfo returns basic instance discovery metadata.
func (s *Server) handleNodeInfo(c echo.Context) error {
	var resp nodeInfoResponse
	resp.Version = "0.1.0"
	resp.Software.Name = "vidra"
	resp.Software.Version = "0.1.0"
	resp.Instance.Name = s.cfg.InstanceName
	return c.JSON(http.StatusOK, resp)
}
