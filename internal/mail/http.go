package mail

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

const (
	// httpSendTimeout bounds one provider call. Sends are synchronous in the
	// request path (a signup waits on one), so the budget is far tighter than
	// the SMTP ceiling: a provider that has not answered in ten seconds has
	// already cost the user their page.
	httpSendTimeout = 10 * time.Second
	// maxResponseBody caps what is read from a provider. Success bodies are a
	// few hundred bytes; the cap exists so a hostile or broken endpoint cannot
	// make an instance read a gigabyte into memory on a password reset.
	maxResponseBody = 64 << 10
	// maxErrorDetail caps the vendor text carried in SendError.Err. Truncating
	// at the source is what makes it safe to log: no caller has to remember.
	maxErrorDetail = 512
)

// httpSender is the plumbing every HTTPS provider transport shares: a guarded
// client, a fixed base URL, a hard budget and a capped response read.
type httpSender struct {
	client  *http.Client
	baseURL string
}

// newHTTPSender builds the sender. Production gets urlsafety's guarded client
// (no proxy env, dial-time address checks, bounded redirects); tests inject
// their own client and an httptest base URL. The base URL is NEVER
// operator-supplied — it comes from a constant chosen by the region enum.
func newHTTPSender(defaultBaseURL string, o transportOptions) httpSender {
	client := o.client
	if client == nil {
		client = urlsafety.NewClient(httpSendTimeout)
	}
	base := defaultBaseURL
	if o.baseURL != "" {
		base = o.baseURL
	}
	return httpSender{client: client, baseURL: strings.TrimRight(base, "/")}
}

// do runs one request under the provider budget and returns the status and at
// most maxResponseBody bytes of body. A transport-level failure (DNS, dial,
// TLS, deadline) comes back as an error; any HTTP status at all comes back as a
// status, because "the provider said 401" is an answer, not a failure to ask.
func (h httpSender) do(ctx context.Context, req *http.Request) (int, []byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// WithTimeout takes the EARLIER of the caller's deadline and ours, so a
	// caller that wants less time keeps it.
	ctx, cancel := context.WithTimeout(ctx, httpSendTimeout)
	defer cancel()

	resp, err := h.client.Do(req.Clone(ctx))
	if err != nil {
		return 0, nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

// transportReason classifies a failure to reach the provider at all. It is the
// HTTP counterpart of an SMTP dial failure — and the reason an operator on a
// host with no outbound HTTPS sees "connect_failed" rather than a stack trace.
func transportReason(err error) Reason {
	if isTimeout(err) {
		return ReasonTimeout
	}
	return ReasonConnectFailed
}

// reasonForStatus is the default status→Reason map every provider starts from;
// each one overrides the codes its own documentation gives a specific meaning.
func reasonForStatus(status int) Reason {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return ReasonAuthFailed
	case status == http.StatusPaymentRequired:
		// The account is out of sending allowance. It is not a transient
		// provider fault and not a malformed request: it is the same shape as a
		// quota, and the operator has to go top up.
		return ReasonRateLimited
	case status == http.StatusTooManyRequests:
		return ReasonRateLimited
	case status == http.StatusRequestTimeout:
		return ReasonTimeout
	case status >= 500:
		return ReasonProviderUnavailable
	case status >= 400:
		return ReasonRejected
	}
	return ReasonRejected
}

// vendorDetail renders a provider's refusal for the LOG: the status plus the
// body, collapsed to one line and truncated. It is never safe for an HTTP
// response — a vendor may echo submitted content straight back.
func vendorDetail(kind string, status int, body []byte) error {
	text := strings.TrimSpace(string(body))
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > maxErrorDetail {
		text = text[:maxErrorDetail] + "…(truncated)"
	}
	if text == "" {
		return errors.New(kind + ": HTTP " + strconv.Itoa(status))
	}
	return fmt.Errorf("%s: HTTP %d: %s", kind, status, text)
}

// urlRE matches the http(s) URLs vidra's own message bodies carry (redemption
// links, the moderation queue). The character class stops at whitespace and at
// the delimiters that would break out of an href.
var urlRE = regexp.MustCompile(`https?://[^\s<>"']+`)

// textToHTML renders a plain-text body as HTML for the one provider family that
// will not accept text alone. It is deliberately the ONLY such conversion in
// the package: two of them would drift, and the drift would only ever be
// visible in somebody's inbox.
//
// It escapes first, preserves the layout with <pre> (the bodies use indentation
// to set links apart, which <br> would collapse) and linkifies http(s) URLs so
// a reader gets the same clickable link the text half offers.
func textToHTML(text string) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><body><pre style="font:inherit;white-space:pre-wrap;word-wrap:break-word">`)
	last := 0
	for _, loc := range urlRE.FindAllStringIndex(text, -1) {
		raw := text[loc[0]:loc[1]]
		// Trailing sentence punctuation is prose, not URL: a link that swallows
		// the full stop after it is a link that 404s.
		trimmed := strings.TrimRight(raw, ".,;:!?)]}'\"")
		b.WriteString(html.EscapeString(text[last:loc[0]]))
		esc := html.EscapeString(trimmed)
		b.WriteString(`<a href="` + esc + `">` + esc + `</a>`)
		b.WriteString(html.EscapeString(raw[len(trimmed):]))
		last = loc[1]
	}
	b.WriteString(html.EscapeString(text[last:]))
	b.WriteString("</pre></body></html>")
	return b.String()
}
