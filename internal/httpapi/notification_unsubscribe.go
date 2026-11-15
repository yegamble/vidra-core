package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"
)

// notificationUnsubscribePath is where the RFC 8058 List-Unsubscribe header
// points, relative to the API base.
const notificationUnsubscribePath = "/notifications/unsubscribe"

// UnsubscribeURLPath is the full path (API base included) of the one-click
// unsubscribe endpoint. cmd/api builds the List-Unsubscribe header from it, so
// the header and the route cannot drift.
const UnsubscribeURLPath = apiBasePath + notificationUnsubscribePath

// pgForeignKeyViolation is the SQLSTATE for a write that names a row that no
// longer exists (here: a user deleted after the mail went out).
const pgForeignKeyViolation = "23503"

// handleNotificationUnsubscribe is the POST target of List-Unsubscribe-Post
// (RFC 8058): a mailbox provider's "unsubscribe" button, pressed with no
// session and no cookies. The signed token in the URL is the only credential.
//
// Every request — good token, forged token, empty, a user since deleted —
// answers the same 204 with no body, so the endpoint is neither an oracle for
// which accounts exist nor for which tokens verify. A good token turns every
// email type off through the SAME preference the settings page edits (so the
// page reflects it) and is idempotent. It signs no one in: no cookie, no token
// in the response. A real storage failure is the one honest exception (500), so
// a mailbox provider retries rather than believing the user is unsubscribed.
func (s *Server) handleNotificationUnsubscribe(c *echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	if userID, ok := s.unsubscribeSigner.Verify(c.QueryParam("token")); ok {
		if err := s.notifsvc.UnsubscribeEmail(c.Request().Context(), userID); err != nil {
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != pgForeignKeyViolation {
				// Neither the token nor the address is logged.
				slog.Default().Warn("one-click unsubscribe failed", "error", err)
				return err
			}
		}
	}
	return c.NoContent(http.StatusNoContent)
}

// handleNotificationUnsubscribePage answers a GET of the same URL. Mail
// scanners and link previewers GET every link in a message, so a GET must never
// change state: it sends a person to the notification settings (or, with no
// public origin configured, says where to find them). The token is NOT carried
// onto the redirect.
func (s *Server) handleNotificationUnsubscribePage(c *echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	if base := strings.TrimRight(s.cfg.PublicBaseURL, "/"); base != "" {
		return c.Redirect(http.StatusFound, base+"/settings/notifications")
	}
	return c.String(http.StatusOK, "To stop these emails, turn them off in your notification settings.\n")
}
