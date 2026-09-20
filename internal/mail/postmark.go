package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
)

// postmarkBaseURL is Postmark's single API host.
//
// Verified against https://postmarkapp.com/developer/api/email-api and
// https://postmarkapp.com/developer/api/server-api (2026-09-20): POST
// https://api.postmarkapp.com/email with X-Postmark-Server-Token, Content-Type
// AND Accept application/json; either HtmlBody or TextBody is required, so
// TextBody alone is documented and vidra sends no HTML; MessageStream defaults
// to "outbound" when omitted.
const postmarkBaseURL = "https://api.postmarkapp.com"

// defaultPostmarkStream is Postmark's own default transactional stream. It is
// stored explicitly rather than omitted so the admin panel can show what a
// message will actually be sent on.
const defaultPostmarkStream = "outbound"

type postmarkTransport struct {
	httpSender
	stream string
	token  string
}

func newPostmarkTransport(stream, token string, o transportOptions) *postmarkTransport {
	if stream == "" {
		stream = defaultPostmarkStream
	}
	return &postmarkTransport{httpSender: newHTTPSender(postmarkBaseURL, o), stream: stream, token: token}
}

func (t *postmarkTransport) Kind() string { return KindPostmark }

// postmarkPayload is the documented request body. The field names are
// PascalCase and case-sensitive.
type postmarkPayload struct {
	From          string `json:"From"`
	To            string `json:"To"`
	Subject       string `json:"Subject"`
	TextBody      string `json:"TextBody"`
	ReplyTo       string `json:"ReplyTo,omitempty"`
	MessageStream string `json:"MessageStream"`
}

// postmarkResponse covers both halves of Postmark's envelope: a success carries
// ErrorCode 0, and a failure carries the code that says WHICH failure.
type postmarkResponse struct {
	ErrorCode int    `json:"ErrorCode"`
	Message   string `json:"Message"`
	MessageID string `json:"MessageID"`
}

// Postmark ErrorCode values, from https://postmarkapp.com/developer/api/overview.
const (
	postmarkErrBadToken = 10 // invalid or absent server token, or the wrong token type
	// UNVERIFIED: the sender-signature codes (400 "sender signature not found",
	// 401 "sender signature not confirmed") are widely documented but were NOT
	// visible in the error-code section fetched on 2026-09-20; the codes that
	// WERE visible (10, 300, 402, 403, 406, 429) all match the same list. They
	// are mapped to sender_rejected because that is the failure an operator who
	// has not confirmed their From address actually hits; if the numbers are
	// wrong the failure degrades to the generic `rejected`, never to a false OK.
	postmarkErrSenderSignatureMissing   = 400
	postmarkErrSenderSignatureUnconfirm = 401
	postmarkErrInactiveRecipient        = 406
	postmarkErrRateLimited              = 429
)

func (t *postmarkTransport) Send(ctx context.Context, m Message) error {
	if err := validateMessage(m); err != nil {
		return err
	}
	body, err := json.Marshal(postmarkPayload{
		From:          formatAddress(m.From),
		To:            m.To,
		Subject:       sanitizeHeader(m.Subject),
		TextBody:      m.Text,
		ReplyTo:       sanitizeHeader(m.ReplyTo),
		MessageStream: t.stream,
	})
	if err != nil {
		return &SendError{Reason: ReasonRejected, Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/email", bytes.NewReader(body))
	if err != nil {
		return &SendError{Reason: ReasonRejected, Err: err}
	}
	t.authenticate(req)
	req.Header.Set("Content-Type", "application/json")

	status, respBody, err := t.do(ctx, req)
	if err != nil {
		return &SendError{Reason: transportReason(err), Err: err}
	}
	var parsed postmarkResponse
	_ = json.Unmarshal(respBody, &parsed)
	if status == http.StatusOK && parsed.ErrorCode == 0 {
		return nil
	}
	return &SendError{
		Reason: postmarkReason(status, parsed.ErrorCode),
		Err:    vendorDetail(KindPostmark, status, respBody),
	}
}

// authenticate stamps the server token and the Accept header Postmark's docs
// require on every request.
func (t *postmarkTransport) authenticate(req *http.Request) {
	req.Header.Set("X-Postmark-Server-Token", t.token)
	req.Header.Set("Accept", "application/json")
}

// postmarkReason prefers the ErrorCode: Postmark answers 422 for most
// application failures, so the HTTP status alone cannot tell "your token is
// wrong" from "your sender signature is not confirmed".
func postmarkReason(status, errorCode int) Reason {
	switch errorCode {
	case postmarkErrBadToken:
		return ReasonAuthFailed
	case postmarkErrSenderSignatureMissing, postmarkErrSenderSignatureUnconfirm:
		return ReasonSenderRejected
	case postmarkErrInactiveRecipient:
		return ReasonRejected
	case postmarkErrRateLimited:
		return ReasonRateLimited
	}
	return reasonForStatus(status)
}

// Probe reads the server the token belongs to. It is a GET and sends nothing.
// A Postmark server token is not send-only, so unlike Resend or Mailgun a
// healthy configuration here is fully verifiable.
func (t *postmarkTransport) Probe(ctx context.Context) ProbeResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+"/server", nil)
	if err != nil {
		return ProbeResult{Err: &SendError{Reason: ReasonRejected, Err: err}}
	}
	t.authenticate(req)

	status, body, err := t.do(ctx, req)
	if err != nil {
		return ProbeResult{Err: &SendError{Reason: transportReason(err), Err: err}}
	}
	if status == http.StatusOK {
		return ProbeResult{Verified: true}
	}
	var parsed postmarkResponse
	_ = json.Unmarshal(body, &parsed)
	if status >= 500 {
		return ProbeResult{Err: &SendError{Reason: ReasonProviderUnavailable, Err: vendorDetail(KindPostmark, status, body)}}
	}
	return ProbeResult{Err: &SendError{
		Reason: postmarkReason(status, parsed.ErrorCode),
		Err:    vendorDetail(KindPostmark, status, body),
	}}
}

var _ Transport = (*postmarkTransport)(nil)
