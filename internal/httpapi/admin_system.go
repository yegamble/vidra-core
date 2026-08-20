package httpapi

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/version"
)

// systemSoftware describes the running build for the admin status page.
type systemSoftware struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
}

// systemRateLimits is the effective, NON-SECRET rate-limit configuration
// surfaced read-only on the admin status page. Rate limits are a deploy-time
// capacity decision (config, not a runtime admin endpoint — see
// product-decisions §3): exposing them here lets an operator confirm what is in
// force without adding a runtime knob that a compromised admin could use to
// silently disable protection. No secret is involved (these are plain counts and
// a window), so it is safe to report.
type systemRateLimits struct {
	Enabled       bool  `json:"enabled"`
	Requests      int   `json:"requests"`       // general per-IP budget over the window
	AuthRequests  int   `json:"auth_requests"`  // stricter budget for auth endpoints
	WindowSeconds int64 `json:"window_seconds"` // the shared fixed window
}

// systemStatusResponse is the admin-facing operational snapshot: build info, the
// runtime environment, process uptime, an overall health flag, per-dependency
// component status, and the effective (non-secret) rate-limit config. It reports
// only operational metadata — never secrets/PII.
type systemStatusResponse struct {
	Status        string                     `json:"status"` // "ok" | "degraded"
	Software      systemSoftware             `json:"software"`
	Environment   string                     `json:"environment"`
	UptimeSeconds int64                      `json:"uptime_seconds"`
	Components    map[string]componentStatus `json:"components"`
	RateLimits    systemRateLimits           `json:"rate_limits"`
}

// handleSystemStatus returns an operational snapshot for the admin dashboard.
// Behind requireRole(admin). Always 200 (even when degraded) so the admin can see
// the degraded state, unlike the /readyz probe which 503s.
func (s *Server) handleSystemStatus(c echo.Context) error {
	components, healthy := s.systemComponents(c.Request().Context())
	status := "ok"
	if !healthy {
		status = "degraded"
	}
	return c.JSON(http.StatusOK, systemStatusResponse{
		Status: status,
		Software: systemSoftware{
			Name:      "vidra",
			Version:   version.Version,
			Commit:    version.Commit,
			BuildDate: version.Date,
			GoVersion: version.GoVersion(),
		},
		Environment:   s.cfg.Environment,
		UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		Components:    components,
		RateLimits: systemRateLimits{
			Enabled:       s.cfg.RateLimitEnabled,
			Requests:      s.cfg.RateLimitRequests,
			AuthRequests:  s.cfg.AuthRateLimitRequests,
			WindowSeconds: int64(s.cfg.RateLimitWindow.Seconds()),
		},
	})
}
