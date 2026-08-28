package httpapi

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/notification"
)

// notificationPrefsResponse is the caller's per-type notification switchboard:
// every known type mapped to whether it is delivered. Types absent from any
// stored preference default to enabled.
type notificationPrefsResponse struct {
	Prefs map[string]bool `json:"prefs"`
}

// updateNotificationPrefsRequest is the PATCH /me/notification-prefs body: a
// partial map of notification type → enabled. Only the types present are
// changed; an unknown type rejects the whole update.
type updateNotificationPrefsRequest struct {
	Prefs map[string]bool `json:"prefs"`
}

func (r updateNotificationPrefsRequest) Validate() []FieldError {
	if len(r.Prefs) == 0 {
		return []FieldError{{Field: "prefs", Message: "at least one notification type is required"}}
	}
	known := notification.KnownTypes()
	for typ := range r.Prefs {
		if !slices.Contains(known, typ) {
			return []FieldError{{
				Field:   "prefs",
				Message: "unknown notification type; known types: " + strings.Join(known, ", "),
			}}
		}
	}
	return nil
}

// handleGetNotificationPrefs returns the caller's notification preferences (all
// known types; absent rows default to enabled). Behind requireAuth.
func (s *Server) handleGetNotificationPrefs(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	prefs, err := s.notifsvc.Prefs(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, notificationPrefsResponse{Prefs: prefs})
}

// handleUpdateNotificationPrefs applies a partial preference update and returns
// the full updated map. Behind requireAuth. An unknown type is 422 and nothing
// is written.
func (s *Server) handleUpdateNotificationPrefs(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in updateNotificationPrefsRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	if err := s.notifsvc.SetPrefs(ctx, userID, in.Prefs); err != nil {
		if errors.Is(err, notification.ErrUnknownType) {
			// Defence in depth: Validate already rejects unknown types.
			return &ValidationError{Fields: []FieldError{{Field: "prefs", Message: "unknown notification type"}}}
		}
		return err
	}
	prefs, err := s.notifsvc.Prefs(ctx, userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, notificationPrefsResponse{Prefs: prefs})
}
