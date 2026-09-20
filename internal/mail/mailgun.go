package mail

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
)

// Mailgun's two regional API hosts. The region is an ENUM in the settings
// document, never a URL: these two constants are the only hosts this transport
// can reach, which is what keeps an admin-editable mail config from being an
// SSRF surface.
//
// Verified against
// https://documentation.mailgun.com/docs/mailgun/user-manual/sending-messages/send-http
// and the API reference for POST /v3/{domain_name}/messages (2026-09-20): HTTP
// Basic with username "api", multipart/form-data, fields from/to/subject/text
// (at least one body field required), custom MIME headers via an `h:` prefix,
// 200 → {"id": "…", "message": "…"}, documented errors 400/401/402/403/404/413/429/500.
const (
	mailgunBaseURLUS = "https://api.mailgun.net"
	mailgunBaseURLEU = "https://api.eu.mailgun.net"
)

type mailgunTransport struct {
	httpSender
	domain string
	apiKey string
	region Region
}

func newMailgunTransport(domain string, region Region, apiKey string, o transportOptions) *mailgunTransport {
	base := mailgunBaseURLUS
	if region == RegionEU {
		base = mailgunBaseURLEU
	}
	return &mailgunTransport{
		httpSender: newHTTPSender(base, o),
		domain:     domain,
		apiKey:     apiKey,
		region:     region,
	}
}

func (t *mailgunTransport) Kind() string { return KindMailgun }

func (t *mailgunTransport) Send(ctx context.Context, m Message) error {
	if err := validateMessage(m); err != nil {
		return err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fields := [][2]string{
		{"from", formatAddress(m.From)},
		{"to", m.To},
		{"subject", sanitizeHeader(m.Subject)},
		{"text", m.Text},
	}
	if m.ReplyTo != "" {
		// UNVERIFIED: the API reference documents the `h:` prefix for custom
		// MIME headers generally (h:X-Custom-Header) but does not spell out
		// h:Reply-To on the send-endpoint page. Reply-To is an ordinary MIME
		// header, so the prefix applies; if it did not, the failure is a
		// missing Reply-To on contact-form mail, never a failed send.
		fields = append(fields, [2]string{"h:Reply-To", sanitizeHeader(m.ReplyTo)})
	}
	for _, f := range fields {
		if err := w.WriteField(f[0], f[1]); err != nil {
			return &SendError{Reason: ReasonRejected, Err: err}
		}
	}
	if err := w.Close(); err != nil {
		return &SendError{Reason: ReasonRejected, Err: err}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/v3/"+t.domain+"/messages", bytes.NewReader(buf.Bytes()))
	if err != nil {
		return &SendError{Reason: ReasonRejected, Err: err}
	}
	req.SetBasicAuth("api", t.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

	status, respBody, err := t.do(ctx, req)
	if err != nil {
		return &SendError{Reason: transportReason(err), Err: err}
	}
	if status == http.StatusOK {
		return nil
	}
	return &SendError{Reason: t.sendReason(status), Err: vendorDetail(KindMailgun, status, respBody)}
}

// sendReason maps Mailgun's documented send failures. 404 is the one worth
// singling out: the message endpoint is scoped by domain, so a 404 means the
// sending domain does not exist IN THIS REGION — overwhelmingly a US/EU
// mix-up, and unrecognisable as such from a generic "rejected".
func (t *mailgunTransport) sendReason(status int) Reason {
	if status == http.StatusNotFound {
		return ReasonSenderRejected
	}
	return reasonForStatus(status)
}

// Probe reads the sending domain's record. It sends nothing.
//
// The three outcomes each mean something different to an operator:
//   - 200: the key can see the domain — configuration proven.
//   - 404: the domain is not in THIS region. Mailgun's US and EU stacks are
//     separate accounts as far as the API is concerned, and picking the wrong
//     one is the single most common Mailgun misconfiguration. That is a real
//     failure, reported as such.
//   - 401/403: a domain-scoped SENDING key cannot read the domains API. That is
//     the recommended kind of key, so calling it `down` would tell operators
//     their working configuration is broken. Unverifiable, not failed.
//
// DEVIATION from the architecture sketch, which named GET /v3/domains/<domain>:
// the current API reference documents the single-domain read as
// GET /v4/domains/{name} (v3 remains only for DELETE). The three outcomes above
// are unchanged by the version.
// UNVERIFIED: that the v4 path is served on the EU host as well. If it were
// not, a probe on an EU instance reports "unverifiable", never a false failure.
func (t *mailgunTransport) Probe(ctx context.Context) ProbeResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+"/v4/domains/"+t.domain, nil)
	if err != nil {
		return ProbeResult{Err: &SendError{Reason: ReasonRejected, Err: err}}
	}
	req.SetBasicAuth("api", t.apiKey)
	req.Header.Set("Accept", "application/json")

	status, body, err := t.do(ctx, req)
	if err != nil {
		return ProbeResult{Err: &SendError{Reason: transportReason(err), Err: err}}
	}
	switch {
	case status == http.StatusOK:
		return ProbeResult{Verified: true, Encrypted: true}
	case status == http.StatusNotFound:
		return ProbeResult{Err: &SendError{
			Reason: ReasonSenderRejected,
			Err: errors.New("mailgun: domain " + t.domain + " not found in the " + string(t.region) +
				" region — a domain created in the other region is invisible here"),
		}}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		// Send-only domain key: reachable, unprovable. Only a test send settles it.
		return ProbeResult{Encrypted: true}
	case status >= 500:
		return ProbeResult{Err: &SendError{Reason: ReasonProviderUnavailable, Err: vendorDetail(KindMailgun, status, body)}}
	}
	return ProbeResult{Encrypted: true}
}

var _ Transport = (*mailgunTransport)(nil)
