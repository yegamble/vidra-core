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

// systemStatusResponse is the admin-facing operational snapshot: build info, the
// runtime environment, process uptime, an overall health flag, and per-dependency
// component status. It reports only operational metadata — never secrets/PII.
type systemStatusResponse struct {
	Status        string                     `json:"status"` // "ok" | "degraded"
	Software      systemSoftware             `json:"software"`
	Environment   string                     `json:"environment"`
	UptimeSeconds int64                      `json:"uptime_seconds"`
	Components    map[string]componentStatus `json:"components"`
}

// handleSystemStatus returns an operational snapshot for the admin dashboard.
// Behind requireRole(admin). Always 200 (even when degraded) so the admin can see
// the degraded state, unlike the /readyz probe which 503s.
func (s *Server) handleSystemStatus(c echo.Context) error {
	components, healthy := s.componentHealth(c.Request().Context())
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
	})
}
