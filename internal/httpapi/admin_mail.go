package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

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

// mailTestRequest is the OPTIONAL body. Without it, or with a blank To, the
// probe goes to the contact address exactly as before. A To is honoured only
// behind a fresh proof of the caller's own password (or a step-up token).
type mailTestRequest struct {
	To              string `json:"to"`
	CurrentPassword string `json:"current_password"`
	StepUpToken     string `json:"step_up_token"`
}

// maxMailTestRecipientLen is RFC 5321's forward-path limit.
const maxMailTestRecipientLen = 254

func (in mailTestRequest) recipient() string { return strings.TrimSpace(in.To) }

// Validate applies the codebase's one address rule (mail.IsAddrSpec): a single
// bare addr-spec, so a display name, a list or a CRLF can never reach a header.
func (in mailTestRequest) Validate() []FieldError {
	to := in.recipient()
	if to == "" {
		return nil
	}
	if len(to) > maxMailTestRecipientLen || !mail.IsAddrSpec(to) {
		return []FieldError{{Field: "to", Message: "must be one plain email address, like someone@example.org"}}
	}
	return nil
}

// handleMailTest sends one probe message so an admin can find out whether
// outbound mail works BEFORE a user needs a password reset. Behind
// requireRole(admin) and its own budget (10 per admin per hour).
//
// By default the recipient is the instance's own effective contact address. An
// admin may name another one — to check delivery to a particular mailbox, or
// before any contact address exists — but only with a FRESH proof of their own
// password (the same gate as saving the mail settings). A send-to-any-address
// button is otherwise an open relay behind a stolen admin session, useful for
// spam, phishing from the instance's own domain and burning its reputation; the
// fresh proof means a session alone still cannot mail anyone but the operator,
// and the per-admin budget bounds what a password-holder can do.
//
//	503 — this deployment has no outbound mail path at all (mail_not_configured).
//	422 — the named recipient is not one bare address.
//	403 — a named recipient without a valid fresh proof (step_up_required / incorrect password).
//	409 — no recipient named and no contact address set, so there is nowhere to send.
//	502 — the relay refused it. The body carries the machine-readable `reason`
//	      (and `port` for SMTP) so the panel can name the failure; the relay's
//	      own words go to the server log, never to the response.
//	202 — handed to the relay.
func (s *Server) handleMailTest(c *echo.Context) error {
	callerID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}

	var in mailTestRequest
	if err := bindAndValidate(c, &in); err != nil {
		s.audit(c, observability.ActionAdminMailTest, observability.ResultFailure, callerID.String(), "invalid_request")
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

	to, custom := in.recipient(), in.recipient() != ""
	if custom {
		// Refusals are audited inside, by reason code only.
		if err := s.confirmFreshAdminWriter(c, callerID, observability.ActionAdminMailTest, in.CurrentPassword, in.StepUpToken); err != nil {
			return err
		}
	} else {
		to = strings.TrimSpace(s.effectiveContactEmail())
	}
	if to == "" {
		s.audit(c, observability.ActionAdminMailTest, observability.ResultFailure, callerID.String(), "no_contact_email")
		return echo.NewHTTPError(http.StatusConflict,
			"set the instance contact email first, or name a recipient — without one the test message goes to that address. It is the contact_email setting on the admin General configuration page")
	}
	outcome := func(base string) string {
		if custom {
			return base + "_custom_recipient"
		}
		return base
	}

	if err := sender.SendTest(c.Request().Context(), to); err != nil {
		// The relay's own words are the only useful diagnostic a failed mail
		// test produces, and they go to the operator's SERVER LOG — never to the
		// response body, never to the audit trail. A rejection routinely quotes
		// the recipient back ("550 5.1.1 <ops@example> ... rejected"), which is
		// operator PII: a log line the operator already owns is the right place
		// for it; a JSON body a browser caches is not.
		// A caller-named address is not the operator's own, so it is redacted
		// from the relay's words before they reach even the server log.
		logged := err.Error()
		if custom {
			logged = strings.ReplaceAll(logged, to, "<test recipient>")
		}
		s.logger.Warn("admin mail test failed",
			"error", logged,
			"request_id", c.Response().Header().Get(echo.HeaderXRequestID),
		)
		// The CLASSIFICATION does cross the boundary, because it is not the
		// relay's words: mail.Reason is a closed vocabulary this codebase
		// defines, and the port is the one the operator configured themselves.
		// Without them the panel can only say "it did not work", and the single
		// most common self-hosting failure — a host blocking outbound 25/465/587
		// — is indistinguishable from a wrong password.
		reason := mail.ReasonOf(err)
		s.audit(c, observability.ActionAdminMailTest, observability.ResultFailure, callerID.String(), outcome("send_failed"))
		return &MailTestFailedError{Reason: string(reason), Port: mailFailurePort(err)}
	}

	s.audit(c, observability.ActionAdminMailTest, observability.ResultSuccess, callerID.String(), outcome("sent"))
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
