package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
)

// resendBaseURL is the only host this transport ever talks to. Resend has one
// global API endpoint — no region selector — so there is nothing for an
// operator to choose and nothing for one to point somewhere else.
//
// Verified against https://resend.com/docs/api-reference/emails/send-email
// (2026-09-20): POST https://api.resend.com/emails, Authorization: Bearer,
// application/json, 200 → {"id": "…"}.
const resendBaseURL = "https://api.resend.com"

type resendTransport struct {
	httpSender
	apiKey string
}

func newResendTransport(apiKey string, o transportOptions) *resendTransport {
	return &resendTransport{httpSender: newHTTPSender(resendBaseURL, o), apiKey: apiKey}
}

func (t *resendTransport) Kind() string { return KindResend }

// resendPayload is the documented request body. `from`, `to` and `subject` are
// required; the content is "either html/text/react", so a text-only body is a
// first-class request and vidra sends no HTML here at all.
type resendPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
	ReplyTo string   `json:"reply_to,omitempty"`
}

// resendError is the documented error envelope (https://resend.com/docs/api-reference/errors).
// `name` is the machine-readable half and the only part worth branching on.
type resendError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

func (t *resendTransport) Send(ctx context.Context, m Message) error {
	if err := validateMessage(m); err != nil {
		return err
	}
	body, err := json.Marshal(resendPayload{
		From:    formatAddress(m.From),
		To:      []string{m.To},
		Subject: sanitizeHeader(m.Subject),
		Text:    m.Text,
		ReplyTo: sanitizeHeader(m.ReplyTo),
	})
	if err != nil {
		return &SendError{Reason: ReasonRejected, Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/emails", bytes.NewReader(body))
	if err != nil {
		return &SendError{Reason: ReasonRejected, Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")

	status, respBody, err := t.do(ctx, req)
	if err != nil {
		return &SendError{Reason: transportReason(err), Err: err}
	}
	if status == http.StatusOK {
		return nil
	}
	return &SendError{Reason: t.sendReason(status, respBody), Err: vendorDetail(KindResend, status, respBody)}
}

// sendReason maps the documented (status, name) pairs. The one that matters is
// 403 validation_error: "unverified domain or testing email restrictions" — the
// single most common Resend failure, and an operator told only "rejected" would
// go looking in the wrong place.
func (t *resendTransport) sendReason(status int, body []byte) Reason {
	name := resendErrorName(body)
	switch {
	case status == http.StatusForbidden && name == "validation_error":
		return ReasonSenderRejected
	case status == http.StatusTooManyRequests:
		// rate_limit_exceeded, daily_quota_exceeded, monthly_quota_exceeded.
		return ReasonRateLimited
	case status == http.StatusServiceUnavailable:
		return ReasonProviderUnavailable
	}
	return reasonForStatus(status)
}

// Probe lists domains, which sends nothing.
//
// A 401 `restricted_api_key` is documented as "key limited to email sending
// only" — that is a PASS: the key cannot read domains precisely because it is a
// valid send-only key, which is the shape a careful operator configures. A 403
// with the same name means something else entirely ("API key is inactive"), so
// the status, not the name alone, decides.
func (t *resendTransport) Probe(ctx context.Context) ProbeResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+"/domains", nil)
	if err != nil {
		return ProbeResult{Err: &SendError{Reason: ReasonRejected, Err: err}}
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	status, body, err := t.do(ctx, req)
	if err != nil {
		return ProbeResult{Err: &SendError{Reason: transportReason(err), Err: err}}
	}
	switch {
	case status == http.StatusOK:
		return ProbeResult{Verified: true}
	case status == http.StatusUnauthorized && resendErrorName(body) == "restricted_api_key":
		return ProbeResult{Verified: true}
	case status >= 500:
		return ProbeResult{Err: &SendError{Reason: ReasonProviderUnavailable, Err: vendorDetail(KindResend, status, body)}}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ProbeResult{Err: &SendError{Reason: ReasonAuthFailed, Err: vendorDetail(KindResend, status, body)}}
	}
	// Anything else: the API answered, so it is reachable, but this did not
	// prove the key. Not an alarm — only a send proves a send.
	return ProbeResult{}
}

func resendErrorName(body []byte) string {
	var e resendError
	if err := json.Unmarshal(body, &e); err != nil {
		return ""
	}
	return e.Name
}

var _ Transport = (*resendTransport)(nil)
