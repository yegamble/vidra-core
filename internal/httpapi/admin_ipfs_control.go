package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/vidra/vidra-core/internal/ipfscontrol"
	"github.com/vidra/vidra-core/internal/observability"
)

type ipfsControlProvider interface {
	Config(context.Context) (ipfscontrol.Document, error)
	Save(context.Context, int64, ipfscontrol.Config, uuid.UUID) (ipfscontrol.Document, error)
	Request(context.Context, string, int64, uuid.UUID, uuid.UUID) (ipfscontrol.HostOperation, error)
	Runtime(context.Context) (ipfscontrol.Runtime, error)
}

func WithIPFSControl(svc ipfsControlProvider) Option {
	return func(s *Server) { s.ipfscontrolsvc = svc }
}
func (s *Server) handleGetIPFSConfig(c echo.Context) error {
	if s.ipfscontrolsvc == nil {
		return echo.NewHTTPError(501, "IPFS control is not configured")
	}
	doc, err := s.ipfscontrolsvc.Config(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, doc)
}
func (s *Server) handleUpdateIPFSConfig(c echo.Context) error {
	if s.ipfscontrolsvc == nil {
		return echo.NewHTTPError(501, "IPFS control is not configured")
	}
	var req struct {
		ExpectedRevision int64               `json:"expected_revision"`
		Config           *ipfscontrol.Config `json:"config"`
	}
	if err := decodeIPFSControlBody(c, &req); err != nil {
		return err
	}
	if req.Config == nil {
		return echo.NewHTTPError(400, "config is required")
	}
	if req.ExpectedRevision < 1 {
		return ipfscontrol.ErrConflict
	}
	if err := req.Config.Validate(); err != nil {
		return &ValidationError{Fields: []FieldError{{Field: "config", Message: err.Error()}}}
	}
	actor, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	doc, err := s.ipfscontrolsvc.Save(c.Request().Context(), req.ExpectedRevision, *req.Config, actor)
	if err != nil {
		s.audit(c, "admin.ipfs.config.update", observability.ResultFailure, actor.String(), "rejected")
		return err
	}
	s.audit(c, "admin.ipfs.config.update", observability.ResultSuccess, actor.String(), "configuration_saved")
	status := http.StatusOK
	if doc.Operation != nil {
		status = http.StatusAccepted
	}
	return c.JSON(status, doc)
}
func (s *Server) handleApplyIPFS(c echo.Context) error   { return s.handleIPFSOperation(c, "apply") }
func (s *Server) handleRestartIPFS(c echo.Context) error { return s.handleIPFSOperation(c, "restart") }
func (s *Server) handleIPFSOperation(c echo.Context, action string) error {
	if s.ipfscontrolsvc == nil {
		return echo.NewHTTPError(501, "IPFS control is not configured")
	}
	var req struct {
		ExpectedRevision int64     `json:"expected_revision"`
		RequestID        uuid.UUID `json:"request_id"`
	}
	if err := decodeIPFSControlBody(c, &req); err != nil {
		return err
	}
	if req.ExpectedRevision < 1 || req.RequestID == uuid.Nil {
		return echo.NewHTTPError(400, "expected_revision and request_id are required")
	}
	actor, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	op, err := s.ipfscontrolsvc.Request(c.Request().Context(), action, req.ExpectedRevision, req.RequestID, actor)
	if err != nil {
		s.audit(c, "admin.ipfs."+action, observability.ResultFailure, actor.String(), "rejected")
		return err
	}
	s.audit(c, "admin.ipfs."+action, observability.ResultSuccess, actor.String(), op.ID)
	return c.JSON(http.StatusAccepted, struct {
		Operation ipfscontrol.HostOperation `json:"operation"`
		Revision  int64                     `json:"revision"`
	}{op, op.ConfigRevision})
}
func decodeIPFSControlBody(c echo.Context, out any) error {
	d := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, 16384))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return echo.NewHTTPError(400, "invalid IPFS control document")
	}
	if d.Decode(new(any)) != io.EOF {
		return echo.NewHTTPError(400, "invalid IPFS control document")
	}
	return nil
}
