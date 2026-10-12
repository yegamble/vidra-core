package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/notification"
)

// notificationPrefsResponse is the caller's per-type notification switchboard:
// every known type mapped to whether it is delivered in-app (absent stored
// preference = enabled), and every email-capable type mapped to whether it is
// ALSO emailed (absent = off: email is opt-in).
type notificationPrefsResponse struct {
	Prefs map[string]bool `json:"prefs"`
	Email map[string]bool `json:"email"`
}

// updateNotificationPrefsRequest is the PATCH /me/notification-prefs body: a
// partial in-app map and/or a partial email map. Only the types present are
// changed; at least one type must be named, and any type the map does not
// accept rejects the whole update before anything is written.
type updateNotificationPrefsRequest struct {
	Prefs map[string]bool `json:"prefs"`
	Email map[string]bool `json:"email"`
}

func (r updateNotificationPrefsRequest) Validate() []FieldError {
	if len(r.Prefs) == 0 && len(r.Email) == 0 {
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
	offered := notification.EmailTypes()
	for typ := range r.Email {
		if !slices.Contains(offered, typ) {
			return []FieldError{{
				Field:   "email",
				Message: "no email delivery for this notification type; email types: " + strings.Join(offered, ", "),
			}}
		}
	}
	return nil
}

// handleGetNotificationPrefs returns the caller's notification preferences (all
// known types; absent rows default to enabled) and email switches. Behind
// requireAuth.
func (s *Server) handleGetNotificationPrefs(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	return s.writeNotificationPrefs(c, userID)
}

// handleUpdateNotificationPrefs applies a partial preference update and returns
// the full updated maps. Behind requireAuth. An unknown type, or an email
// switch for a type without email, is 422 and nothing is written.
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
	if err := s.applyNotificationPrefs(ctx, userID, in); err != nil {
		// Defence in depth: Validate already rejects both.
		if errors.Is(err, notification.ErrUnknownType) || errors.Is(err, notification.ErrNoEmailForType) {
			return &ValidationError{Fields: []FieldError{{Field: "prefs", Message: err.Error()}}}
		}
		return err
	}
	return s.writeNotificationPrefs(c, userID)
}

func (s *Server) applyNotificationPrefs(ctx context.Context, userID uuid.UUID, in updateNotificationPrefsRequest) error {
	if len(in.Prefs) > 0 {
		if err := s.notifsvc.SetPrefs(ctx, userID, in.Prefs); err != nil {
			return err
		}
	}
	if len(in.Email) > 0 {
		return s.notifsvc.SetEmailPrefs(ctx, userID, in.Email)
	}
	return nil
}

func (s *Server) writeNotificationPrefs(c echo.Context, userID uuid.UUID) error {
	ctx := c.Request().Context()
	prefs, err := s.notifsvc.Prefs(ctx, userID)
	if err != nil {
		return err
	}
	email, err := s.notifsvc.EmailPrefs(ctx, userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, notificationPrefsResponse{Prefs: prefs, Email: email})
}
