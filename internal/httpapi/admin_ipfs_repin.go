package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/observability"
)

// ipfsRepinProvider is the bulk re-pin slice of the mirror service (Tier A F6
// follow-up). Optional: a provider without it answers 501, so existing fakes of
// ipfsMirrorProvider stay valid.
type ipfsRepinProvider interface {
	StartRepin(ctx context.Context) (ipfsmirror.RepinStatus, error)
	RepinStatus(ctx context.Context) (ipfsmirror.RepinStatus, error)
}

// ipfsRepinView is the body of both /admin/ipfs/repin-playback-only routes (schema
// IPFSRepinStatus).
type ipfsRepinView struct {
	Phase              string     `json:"phase"`
	StartedAt          *time.Time `json:"started_at"`
	FinishedAt         *time.Time `json:"finished_at"`
	PauseReason        string     `json:"pause_reason"`
	Scanned            int64      `json:"scanned"`
	Queued             int64      `json:"queued"`
	Done               int64      `json:"done"`
	Skipped            int64      `json:"skipped"`
	Blocked            int64      `json:"blocked"`
	Failed             int64      `json:"failed"`
	InFlight           int        `json:"in_flight"`
	Remaining          int64      `json:"remaining"`
	BytesSavedEstimate int64      `json:"bytes_saved_estimate"`
}

func toIPFSRepinView(st ipfsmirror.RepinStatus) ipfsRepinView {
	return ipfsRepinView{Phase: st.Phase, StartedAt: st.StartedAt, FinishedAt: st.FinishedAt, PauseReason: st.PauseReason,
		Scanned: st.Scanned, Queued: st.Queued, Done: st.Done, Skipped: st.Skipped, Blocked: st.Blocked, Failed: st.Failed,
		InFlight: st.InFlight, Remaining: st.Remaining, BytesSavedEstimate: st.BytesSavedEstimate}
}

func (s *Server) ipfsRepin() (ipfsRepinProvider, error) {
	if !s.ipfsConfigured() {
		return nil, &IPFSDisabledError{}
	}
	p, ok := s.ipfsmirrorsvc.(ipfsRepinProvider)
	if !ok {
		return nil, echo.NewHTTPError(http.StatusNotImplemented, "ipfs mirror subsystem is not wired on this build")
	}
	return p, nil
}

func repinError(err error) error {
	switch {
	case errors.Is(err, ipfsmirror.ErrRepinRunning):
		return echo.NewHTTPError(http.StatusConflict, "a playback-only re-pin is already running")
	case errors.Is(err, ipfsmirror.ErrRepinUnsupported):
		return echo.NewHTTPError(http.StatusNotImplemented, "this storage backend cannot recompute the playback-only set")
	}
	return err
}

// handleStartIPFSRepin starts the bulk re-pin of already-pinned HLS trees to the
// playback-only set. The work runs in the leader's mirror worker, bounded and paused
// by the capacity gates; this only records the request. Admin-only and audited.
func (s *Server) handleStartIPFSRepin(c echo.Context) error {
	p, err := s.ipfsRepin()
	if err != nil {
		return err
	}
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	st, err := p.StartRepin(c.Request().Context())
	if err != nil {
		s.audit(c, observability.ActionIPFSRepin, observability.ResultFailure, userID.String(), "rejected")
		return repinError(err)
	}
	s.audit(c, observability.ActionIPFSRepin, observability.ResultSuccess, userID.String(), fmt.Sprintf("remaining=%d", st.Remaining))
	return c.JSON(http.StatusAccepted, toIPFSRepinView(st))
}

// handleGetIPFSRepin reports the current or last run.
func (s *Server) handleGetIPFSRepin(c echo.Context) error {
	p, err := s.ipfsRepin()
	if err != nil {
		return err
	}
	st, err := p.RepinStatus(c.Request().Context())
	if err != nil {
		return repinError(err)
	}
	return c.JSON(http.StatusOK, toIPFSRepinView(st))
}
