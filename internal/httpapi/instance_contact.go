package httpapi

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// instanceContactRequest is the POST /api/v1/instance/contact body: a visitor's
// message to the instance operator (spec instance-platform-info).
type instanceContactRequest struct {
	FromName  string `json:"from_name"`
	FromEmail string `json:"from_email"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
}

func (r instanceContactRequest) Validate() []FieldError {
	var fes []FieldError
	switch n := len(strings.TrimSpace(r.FromName)); {
	case n == 0:
		fes = append(fes, FieldError{Field: "from_name", Message: "is required"})
	case n > 120:
		fes = append(fes, FieldError{Field: "from_name", Message: "must be at most 120 characters"})
	}
	if err := validateContactEmail(r.FromEmail); err != "" {
		fes = append(fes, FieldError{Field: "from_email", Message: err})
	}
	switch n := len(strings.TrimSpace(r.Subject)); {
	case n == 0:
		fes = append(fes, FieldError{Field: "subject", Message: "is required"})
	case n > 120:
		fes = append(fes, FieldError{Field: "subject", Message: "must be at most 120 characters"})
	}
	switch n := len(strings.TrimSpace(r.Body)); {
	case n < 10:
		fes = append(fes, FieldError{Field: "body", Message: "must be at least 10 characters"})
	case n > 5000:
		fes = append(fes, FieldError{Field: "body", Message: "must be at most 5000 characters"})
	}
	return fes
}

// validateContactEmail requires a minimally well-formed, bounded reply address
// (mirrors the settings-service email rule, but required). Returns "" when
// valid.
func validateContactEmail(v string) string {
	if strings.TrimSpace(v) == "" {
		return "is required"
	}
	if len(v) > 254 {
		return "must be at most 254 characters"
	}
	at := strings.IndexByte(v, '@')
	if at <= 0 || at == len(v)-1 || strings.ContainsAny(v, " \t\r\n") || strings.Contains(v[at+1:], "@") {
		return "must be a valid email address"
	}
	return ""
}

// handleInstanceContact delivers a visitor contact-form message to the
// operator's effective contact email. Public; throttled hard (1 request per IP
// per hour via the dedicated contact limiter). 409 contact_form_disabled when
// the form is unavailable (toggle off, no contact email, or no outbound mail
// path); 202 once the message is handed to the mailer. Neither the message nor
// any address is ever logged (PII).
func (s *Server) handleInstanceContact(c echo.Context) error {
	var in instanceContactRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if !s.contactFormAvailable() {
		return &ContactFormDisabledError{}
	}
	to := strings.TrimSpace(s.effectiveContactEmail())
	if err := s.contactMailer.SendContactForm(c.Request().Context(), to,
		strings.TrimSpace(in.FromName), strings.TrimSpace(in.FromEmail),
		strings.TrimSpace(in.Subject), in.Body); err != nil {
		// The central handler logs + scrubs this to a generic 500; the error text
		// from the mail package never carries addresses or the body.
		return err
	}
	return c.NoContent(http.StatusAccepted)
}
