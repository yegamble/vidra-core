package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
)

// brevoBaseURL is Brevo's single API host.
//
// Verified against https://developers.brevo.com/reference/sendtransacemail and
// https://developers.brevo.com/reference/getaccount (2026-09-20): POST
// https://api.brevo.com/v3/smtp/email with an `api-key` header and JSON;
// sender, to, subject and htmlContent are required when no templateId is given;
// success is 201 with {"messageId": "…"}; failures are a 400 class carrying
// {"code": "...", "message": "..."}.
const brevoBaseURL = "https://api.brevo.com"

type brevoTransport struct {
	httpSender
	apiKey string
}

func newBrevoTransport(apiKey string, o transportOptions) *brevoTransport {
	return &brevoTransport{httpSender: newHTTPSender(brevoBaseURL, o), apiKey: apiKey}
}

func (t *brevoTransport) Kind() string { return KindBrevo }

type brevoAddress struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// brevoPayload is the documented request body. htmlContent is required here —
// Brevo is the ONLY transport vidra sends HTML to, and the HTML is derived from
// the same plain text by textToHTML rather than written twice.
type brevoPayload struct {
	Sender      brevoAddress   `json:"sender"`
	To          []brevoAddress `json:"to"`
	Subject     string         `json:"subject"`
	TextContent string         `json:"textContent"`
	HTMLContent string         `json:"htmlContent"`
	ReplyTo     *brevoAddress  `json:"replyTo,omitempty"`
}

type brevoError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (t *brevoTransport) Send(ctx context.Context, m Message) error {
	payload := brevoPayload{
		Sender:      brevoAddress{Email: m.From.Address, Name: sanitizeHeader(m.From.Name)},
		To:          []brevoAddress{{Email: m.To}},
		Subject:     sanitizeHeader(m.Subject),
		TextContent: m.Text,
		HTMLContent: textToHTML(m.Text),
	}
	if m.ReplyTo != "" {
		payload.ReplyTo = &brevoAddress{Email: sanitizeHeader(m.ReplyTo)}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return &SendError{Reason: ReasonRejected, Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/v3/smtp/email", bytes.NewReader(body))
	if err != nil {
		return &SendError{Reason: ReasonRejected, Err: err}
	}
	req.Header.Set("api-key", t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	status, respBody, err := t.do(ctx, req)
	if err != nil {
		return &SendError{Reason: transportReason(err), Err: err}
	}
	if status == http.StatusCreated || status == http.StatusOK || status == http.StatusAccepted {
		return nil
	}
	return &SendError{Reason: brevoReason(status, respBody), Err: vendorDetail(KindBrevo, status, respBody)}
}

// brevoReason reads the body code before the status, because Brevo reports
// authentication failures inside its 400 class rather than as a 401: a bad API
// key mapped by status alone would read as "malformed request" and send the
// operator to rewrite a form that was fine.
func brevoReason(status int, body []byte) Reason {
	var e brevoError
	if err := json.Unmarshal(body, &e); err == nil {
		switch e.Code {
		case "unauthorized", "permission_denied", "reseller_permission_denied":
			return ReasonAuthFailed
		case "not_enough_credits":
			// The allowance is spent. Same operator action as a rate limit:
			// wait for the daily reset or buy more.
			return ReasonRateLimited
		}
	}
	return reasonForStatus(status)
}

// Probe reads the account the key belongs to. It is a GET and sends nothing.
func (t *brevoTransport) Probe(ctx context.Context) ProbeResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+"/v3/account", nil)
	if err != nil {
		return ProbeResult{Err: &SendError{Reason: ReasonRejected, Err: err}}
	}
	req.Header.Set("api-key", t.apiKey)
	req.Header.Set("Accept", "application/json")

	status, body, err := t.do(ctx, req)
	if err != nil {
		return ProbeResult{Err: &SendError{Reason: transportReason(err), Err: err}}
	}
	switch {
	case status == http.StatusOK:
		return ProbeResult{Verified: true}
	case status >= 500:
		return ProbeResult{Err: &SendError{Reason: ReasonProviderUnavailable, Err: vendorDetail(KindBrevo, status, body)}}
	}
	return ProbeResult{Err: &SendError{Reason: brevoReason(status, body), Err: vendorDetail(KindBrevo, status, body)}}
}

var _ Transport = (*brevoTransport)(nil)
