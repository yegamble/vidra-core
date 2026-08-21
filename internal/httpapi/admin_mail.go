package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/observability"
)

// testMailer is the narrow, OPTIONAL capability behind POST /admin/mail/test:
// send one probe message and report whether the relay took it. Only *mail.SMTP
// and *auth.CaptureMailer implement it, and the assertion IS the "can this
// deployment be tested" question — exactly the shape bucketChecker uses for the
// object store.
//
// It is deliberately not part of auth.Mailer. That interface has four
// implementations plus a pile of test fakes, and widening it for one admin
// button would mean editing all of them to satisfy a method most of them would
// implement as a no-op — which would also make the 503 below unreachable, since
// every mailer would then claim it could send a test.
type testMailer interface {
	SendTest(ctx context.Context, to string) error
}

// mailTestResponse is the 202 body: the message was handed to the relay. It is
// deliberately not "delivered" — SMTP acceptance is a promise to try, and an
// admin who reads "sent" and finds nothing in their inbox has learned something
// real (the relay accepted it and then dropped it), which is a different
// problem from the relay refusing it.
type mailTestResponse struct {
	Status string `json:"status"`
}

// handleMailTest sends one probe message so an admin can find out whether
// outbound mail works BEFORE a user needs a password reset. Behind
// requireRole(admin) and its own budget (3 per admin per hour).
//
// The recipient is the instance's own effective contact address and the caller
// cannot choose it. That is the whole security design of the endpoint: an
// authenticated-send-to-any-address button is an open relay with an admin
// password in front of it, useful for spam, phishing from the instance's own
// domain, and burning its sending reputation. With a fixed recipient there is
// nothing to abuse — the worst an attacker with an admin session can do is mail
// the operator three times an hour.
//
//	503 — this deployment has no outbound mail path at all.
//	409 — no contact address is set, so there is nowhere to send.
//	502 — the relay refused it (generic text; the relay's own words go to the
//	      server log, never to the response).
//	202 — handed to the relay.
func (s *Server) handleMailTest(c echo.Context) error {
	callerID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}

	sender, configured := s.testMailSender()
	if !configured {
		s.audit(c, observability.ActionAdminMailTest, observability.ResultFailure, callerID.String(), "mail_not_configured")
		return echo.NewHTTPError(http.StatusServiceUnavailable,
			"this instance has no outbound mail path, so there is nothing to test. Set MAIL_ENABLED=true with SMTP_HOST, SMTP_PORT and SMTP_FROM, then restart the api")
	}

	to := strings.TrimSpace(s.effectiveContactEmail())
	if to == "" {
		s.audit(c, observability.ActionAdminMailTest, observability.ResultFailure, callerID.String(), "no_contact_email")
		return echo.NewHTTPError(http.StatusConflict,
			"set the instance contact email first — the test message goes there and nowhere else. It is the contact_email setting on the admin General configuration page")
	}

	if err := sender.SendTest(c.Request().Context(), to); err != nil {
		// The relay's own words are the only useful diagnostic a failed mail
		// test produces, and they go to the operator's SERVER LOG — never to the
		// response body, never to the audit trail. A rejection routinely quotes
		// the recipient back ("550 5.1.1 <ops@example> ... rejected"), which is
		// operator PII: a log line the operator already owns is the right place
		// for it; a JSON body a browser caches is not.
		s.logger.Warn("admin mail test failed",
			"error", err,
			"request_id", c.Response().Header().Get(echo.HeaderXRequestID),
		)
		s.audit(c, observability.ActionAdminMailTest, observability.ResultFailure, callerID.String(), "send_failed")
		return &MailTestFailedError{}
	}

	s.audit(c, observability.ActionAdminMailTest, observability.ResultSuccess, callerID.String(), "sent")
	return c.JSON(http.StatusAccepted, mailTestResponse{Status: "sent"})
}

// testMailSender returns the wired mailer's test capability, and whether this
// deployment has one at all. The dev capture seam counts: it is how the local
// and e2e stacks send, so the button proves the same code path there instead of
// answering 503 on every developer machine.
func (s *Server) testMailSender() (testMailer, bool) {
	if s.contactMailer == nil {
		return nil, false
	}
	sender, ok := s.contactMailer.(testMailer)
	return sender, ok
}
