package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/mail"
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
// requireRole(admin) and its own budget (10 per admin per hour).
//
// The recipient is the instance's own effective contact address and the caller
// cannot choose it. That is the whole security design of the endpoint: an
// authenticated-send-to-any-address button is an open relay with an admin
// password in front of it, useful for spam, phishing from the instance's own
// domain, and burning its sending reputation. With a fixed recipient there is
// nothing to abuse — the worst an attacker with an admin session can do is mail
// the operator ten times an hour.
//
//	503 — this deployment has no outbound mail path at all (mail_not_configured).
//	409 — no contact address is set, so there is nowhere to send.
//	502 — the relay refused it. The body carries the machine-readable `reason`
//	      (and `port` for SMTP) so the panel can name the failure; the relay's
//	      own words go to the server log, never to the response.
//	202 — handed to the relay.
func (s *Server) handleMailTest(c echo.Context) error {
	callerID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}

	sender, configured := s.testMailSender()
	if !configured {
		s.audit(c, observability.ActionAdminMailTest, observability.ResultFailure, callerID.String(), "mail_not_configured")
		// Typed, not a bare echo.NewHTTPError: 503 is a 5xx and the central
		// handler scrubs any 5xx it has no stable code for down to "an
		// unexpected error occurred", which would throw away the one sentence
		// that tells the operator what to set.
		return &MailNotConfiguredError{}
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
		// The CLASSIFICATION does cross the boundary, because it is not the
		// relay's words: mail.Reason is a closed vocabulary this codebase
		// defines, and the port is the one the operator configured themselves.
		// Without them the panel can only say "it did not work", and the single
		// most common self-hosting failure — a host blocking outbound 25/465/587
		// — is indistinguishable from a wrong password.
		reason := mail.ReasonOf(err)
		s.audit(c, observability.ActionAdminMailTest, observability.ResultFailure, callerID.String(), "send_failed")
		return &MailTestFailedError{Reason: string(reason), Port: mailFailurePort(err)}
	}

	s.audit(c, observability.ActionAdminMailTest, observability.ResultSuccess, callerID.String(), "sent")
	return c.JSON(http.StatusAccepted, mailTestResponse{Status: "sent"})
}

// testMailSender returns the wired mailer's test capability, and whether this
// deployment has one at all. The dev capture seam counts: it is how the local
// and e2e stacks send, so the button proves the same code path there instead of
// answering 503 on every developer machine.
//
// It asks mailPathConfigured() and not just "is a mailer wired", and the
// difference is load-bearing since the composer became unconditional. cmd/api
// now installs ONE composer whether or not anything is configured — that is what
// makes the transport switchable at runtime — and an unconfigured composer's
// send returns nil, the historical no-op contract. Asserting only the interface
// would therefore answer 202 "sent" on an instance with no mail path at all:
// the silent success the no-op mailer was always capable of, surfaced on the
// one button whose entire purpose is to tell an operator whether mail works.
func (s *Server) testMailSender() (testMailer, bool) {
	if s.contactMailer == nil || !s.mailPathConfigured() {
		return nil, false
	}
	sender, ok := s.contactMailer.(testMailer)
	return sender, ok
}
