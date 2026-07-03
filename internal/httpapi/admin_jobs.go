package httpapi

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/jobstatus"
)

// jobStatusProvider is the seam the admin-jobs handler depends on. It is
// satisfied by *jobstatus.Service and faked in tests.
type jobStatusProvider interface {
	Overview(ctx context.Context) (jobstatus.Overview, error)
}

// handleListJobs returns the durable-queue operations snapshot for the admin
// jobs page: per-queue {pending, running, done, failed, oldest_pending_age} plus
// a merged recent-failures list (id/error/attempts — no secrets). Admin-only.
func (s *Server) handleListJobs(c echo.Context) error {
	ov, err := s.jobStatusSvc.Overview(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, ov)
}
