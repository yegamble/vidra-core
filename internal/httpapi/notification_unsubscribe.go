package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/observability"
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

// isUnsubscribeRoute reports whether the matched route is the one-click POST.
func isUnsubscribeRoute(c *echo.Context) bool {
	return c.Request().Method == http.MethodPost && c.Path() == UnsubscribeURLPath
}

// handleNotificationUnsubscribe is the POST target of List-Unsubscribe-Post
// (RFC 8058): a mailbox provider's "unsubscribe" button, pressed with no
// session and no cookies. The signed token in the URL is the only credential.
//
// A valid token and an invalid one (forged, empty, a user since deleted) are
// indistinguishable: both answer 204 with no body, so the endpoint is neither
// an oracle for which accounts exist nor for which tokens verify. (A 429 from
// the per-IP budget remains possible for either; see rateLimit.) A valid token
// turns every email type off through the SAME preference the settings page
// edits, performs no write when everything is already off, and is audited when
// it changed something. It signs no one in: no cookie, no token in the
// response. A real storage failure is the one honest exception (500), so a
// mailbox provider retries rather than believing the user is unsubscribed.
func (s *Server) handleNotificationUnsubscribe(c *echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	if userID, ok := s.unsubscribeSigner.Verify(c.QueryParam("token")); ok {
		changed, err := s.notifsvc.UnsubscribeEmail(c.Request().Context(), userID)
		if err != nil {
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != pgForeignKeyViolation {
				// Neither the token nor the address is logged.
				slog.Default().Warn("one-click unsubscribe failed", "error", err)
				return err
			}
		} else if changed {
			s.auditEvent(c, audit.Event{
				Action: observability.ActionNotificationEmailUnsubscribe, Result: observability.ResultSuccess,
				ActorID: userID.String(), ResourceType: auditResourceUser, ResourceID: userID.String(),
				Metadata: []audit.MetadataField{{Key: "source", Value: "one_click"}},
			})
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
		return c.Redirect(http.StatusFound, base+mail.NotificationSettingsPath)
	}
	return c.String(http.StatusOK, "To stop these emails, turn them off in your notification settings.\n")
}
