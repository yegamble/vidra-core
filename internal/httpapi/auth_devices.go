package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// Recognised devices and the new-device sign-in notice (see auth/devices.go).
// The device is identified by a long-lived random vidra_device cookie, scoped
// like the refresh cookie to /api/v1/auth, httpOnly and SameSite=Lax.
const (
	deviceCookieName = "vidra_device"
	deviceCookieTTL  = 400 * 24 * time.Hour
)

// withDevice reads the request's device cookie (minting a token when absent),
// returns a request context carrying it for the service, and the token so the
// caller can set the cookie once the sign-in has succeeded.
func (s *Server) withDevice(c echo.Context) string {
	tok := ""
	if ck, err := c.Cookie(deviceCookieName); err == nil && ck != nil {
		tok = strings.TrimSpace(ck.Value)
	}
	if tok == "" {
		tok, _ = auth.NewDeviceToken()
	}
	if tok != "" {
		req := c.Request()
		c.SetRequest(req.WithContext(auth.WithDeviceToken(req.Context(), tok)))
	}
	return tok
}

func (s *Server) setDeviceCookie(c echo.Context, tok string) {
	if tok != "" {
		s.writeStateCookie(c, deviceCookieName, refreshCookiePath, tok, deviceCookieTTL)
	}
}

type deviceItem struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	FirstSeen time.Time `json:"first_seen_at"`
	LastSeen  time.Time `json:"last_seen_at"`
	Current   bool      `json:"current"`
}

type devicesResponse struct {
	Devices        []deviceItem `json:"devices"`
	NewDeviceEmail bool         `json:"new_device_email"`
}

// handleListDevices lists the caller's recognised devices (GET /auth/devices).
func (s *Server) handleListDevices(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	tok := ""
	if ck, err := c.Cookie(deviceCookieName); err == nil && ck != nil {
		tok = strings.TrimSpace(ck.Value)
	}
	ctx := c.Request().Context()
	rows, err := s.authsvc.KnownDevices(ctx, userID, tok, c.Request().UserAgent())
	if err != nil {
		return err
	}
	out := devicesResponse{Devices: make([]deviceItem, 0, len(rows)), NewDeviceEmail: s.authsvc.NewDeviceEmailEnabled(ctx, userID)}
	for _, r := range rows {
		out.Devices = append(out.Devices, deviceItem{ID: r.ID.String(), Label: r.Label, FirstSeen: r.FirstSeen, LastSeen: r.LastSeen, Current: r.Current})
	}
	return c.JSON(http.StatusOK, out)
}

// handleForgetDevice removes one recognised device (DELETE /auth/devices/:id).
// 404 for an id that is not the caller's. It does not end sessions.
func (s *Server) handleForgetDevice(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "device not found")
	if err != nil {
		return err
	}
	if err := s.authsvc.ForgetDevice(c.Request().Context(), userID, id); err != nil {
		if errors.Is(err, auth.ErrDeviceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "device not found")
		}
		return err
	}
	s.audit(c, observability.ActionDeviceForget, observability.ResultSuccess, userID.String(), "")
	return c.NoContent(http.StatusNoContent)
}

type securityPrefsRequest struct {
	NewDeviceEmail *bool `json:"new_device_email"`
}

func (r securityPrefsRequest) Validate() []FieldError {
	if r.NewDeviceEmail == nil {
		return []FieldError{{Field: "new_device_email", Message: "is required"}}
	}
	return nil
}

// handleUpdateSecurityPrefs sets the new-device email opt-out
// (PATCH /auth/security-prefs).
func (s *Server) handleUpdateSecurityPrefs(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in securityPrefsRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.authsvc.SetNewDeviceEmailEnabled(c.Request().Context(), userID, *in.NewDeviceEmail); err != nil {
		return err
	}
	state := "off"
	if *in.NewDeviceEmail {
		state = "on"
	}
	s.auditEvent(c, audit.Event{
		Action: observability.ActionSecurityPrefsUpdate, Result: observability.ResultSuccess,
		ActorID: userID.String(), ResourceType: auditResourceUser, ResourceID: userID.String(),
		Metadata: []audit.MetadataField{{Key: "outcome", Value: "new_device_email_" + state}},
	})
	return c.JSON(http.StatusOK, map[string]bool{"new_device_email": *in.NewDeviceEmail})
}
