package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/atproto"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// atprotoStatusView is the public projection of a linked Bluesky account. It
// NEVER exposes the app password (sealed or raw) — only the handle, resolved DID,
// PDS, and posting settings.
type atprotoStatusView struct {
	Handle       string     `json:"handle"`
	DID          string     `json:"did"`
	PDSURL       string     `json:"pds_url"`
	AutoPost     bool       `json:"auto_post"`
	CreatedAt    time.Time  `json:"created_at"`
	LastPostedAt *time.Time `json:"last_posted_at,omitempty"`
}

func newATProtoStatusView(a sqlcgen.AtprotoAccount) atprotoStatusView {
	v := atprotoStatusView{
		Handle:    a.Handle,
		DID:       a.Did,
		PDSURL:    a.PdsUrl,
		AutoPost:  a.AutoPost,
		CreatedAt: a.CreatedAt,
	}
	if a.LastPostedAt.Valid {
		t := a.LastPostedAt.Time
		v.LastPostedAt = &t
	}
	return v
}

// linkATProtoRequest is the PUT /api/v1/me/atproto body. app_password must be a
// Bluesky APP PASSWORD (Settings → App Passwords), never the account's main
// password — the UI copy must say so and the backend never returns it.
type linkATProtoRequest struct {
	Handle      string `json:"handle"`
	AppPassword string `json:"app_password"`
	// PDSURL is optional; empty defaults to https://bsky.social. It must be an
	// https, public origin.
	PDSURL   string `json:"pds_url"`
	AutoPost bool   `json:"auto_post"`
}

func (r linkATProtoRequest) Validate() []FieldError {
	var fes []FieldError
	if strings.TrimSpace(r.Handle) == "" {
		fes = append(fes, FieldError{Field: "handle", Message: "is required"})
	}
	if strings.TrimSpace(r.AppPassword) == "" {
		fes = append(fes, FieldError{Field: "app_password", Message: "is required — use a Bluesky app password, not your main password"})
	}
	return fes
}

// handleLinkATProto links (or re-links) the caller's Bluesky account. It verifies
// the credentials against the PDS (com.atproto.server.createSession) before
// storing the sealed app password, then returns the status view.
func (s *Server) handleLinkATProto(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in linkATProtoRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	acct, err := s.atprotosvc.Link(c.Request().Context(), userID, atproto.LinkInput{
		Handle:      in.Handle,
		AppPassword: in.AppPassword,
		PDSURL:      in.PDSURL,
		AutoPost:    in.AutoPost,
	})
	if err != nil {
		return atprotoError(err)
	}
	return c.JSON(http.StatusOK, newATProtoStatusView(acct))
}

// handleGetATProto returns the caller's linked-account status (never the
// password). 404 when nothing is linked.
func (s *Server) handleGetATProto(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	acct, err := s.atprotosvc.Status(c.Request().Context(), userID)
	if err != nil {
		return atprotoError(err)
	}
	return c.JSON(http.StatusOK, newATProtoStatusView(acct))
}

// handleUnlinkATProto removes the caller's linked Bluesky account. Idempotent.
func (s *Server) handleUnlinkATProto(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if err := s.atprotosvc.Unlink(c.Request().Context(), userID); err != nil {
		return atprotoError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// atprotoError maps atproto service sentinels to HTTP error envelopes. It never
// surfaces a PDS URL, credential, or upstream error body.
func atprotoError(err error) error {
	switch {
	case errors.Is(err, atproto.ErrDisabled):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Bluesky cross-posting is not enabled on this instance")
	case errors.Is(err, atproto.ErrNotLinked):
		return echo.NewHTTPError(http.StatusNotFound, "no linked Bluesky account")
	case errors.Is(err, atproto.ErrMissingCredentials):
		return &ValidationError{Fields: []FieldError{{Field: "app_password", Message: "handle and app password are required"}}}
	case errors.Is(err, atproto.ErrInvalidHandle):
		return &ValidationError{Fields: []FieldError{{Field: "handle", Message: "is not a valid Bluesky handle"}}}
	case errors.Is(err, atproto.ErrInvalidPDS):
		return &ValidationError{Fields: []FieldError{{Field: "pds_url", Message: "must be an https public origin"}}}
	case errors.Is(err, atproto.ErrInvalidCredentials):
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "Bluesky rejected these credentials; check the handle and app password")
	case errors.Is(err, atproto.ErrPDSUnreachable):
		return echo.NewHTTPError(http.StatusBadGateway, "could not reach the Bluesky server; please try again")
	default:
		return err
	}
}
