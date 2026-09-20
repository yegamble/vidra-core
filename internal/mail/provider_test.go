package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"strings"
	"testing"
)

// capture is one request a provider transport made, kept whole so a test can
// assert the shape a vendor's own documentation specifies rather than the shape
// this package happens to send.
type capture struct {
	method      string
	path        string
	auth        string
	contentType string
	accept      string
	body        []byte
}

// providerServer is an httptest stand-in for a vendor API. status and response
// are what it answers with; every request it receives is recorded.
type providerServer struct {
	t        *testing.T
	status   int
	response string
	got      []capture
	srv      *httptest.Server
}

func newProviderServer(t *testing.T, status int, response string) *providerServer {
	t.Helper()
	p := &providerServer{t: t, status: status, response: response}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		p.got = append(p.got, capture{
			method:      r.Method,
			path:        r.URL.Path,
			auth:        r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type"),
			accept:      r.Header.Get("Accept"),
			body:        body,
		})
		// Some vendors authenticate with a bespoke header; record those too.
		if v := r.Header.Get("X-Postmark-Server-Token"); v != "" {
			p.got[len(p.got)-1].auth = "X-Postmark-Server-Token " + v
		}
		if v := r.Header.Get("api-key"); v != "" {
			p.got[len(p.got)-1].auth = "api-key " + v
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(p.status)
		_, _ = io.WriteString(w, p.response)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *providerServer) opts() []transportOption {
	return []transportOption{withBaseURL(p.srv.URL), withHTTPClient(p.srv.Client())}
}

func (p *providerServer) only() capture {
	p.t.Helper()
	if len(p.got) != 1 {
		p.t.Fatalf("got %d requests, want exactly 1", len(p.got))
	}
	return p.got[0]
}

func providerMessage() Message {
	return Message{
		From:    netmail.Address{Name: "Vidra Mail", Address: "no-reply@vidra.test"},
		To:      "ada@example.test",
		ReplyTo: "ops@vidra.test",
		Subject: "Reset your password",
		Text:    "Hi,\n\nOpen https://vidra.example/reset-password/confirm?token=t0k to continue.\n",
	}
}

func decodeJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("request body is not JSON (%v): %s", err, body)
	}
	return m
}

// parseMultipart reads a multipart/form-data body back into field values, so
// the Mailgun assertions are made against what the vendor would actually parse
// rather than against the bytes this package happened to write.
func parseMultipart(t *testing.T, contentType string, body []byte) map[string]string {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content type %q: %v", contentType, err)
	}
	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	form, err := r.ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("read multipart form: %v", err)
	}
	out := make(map[string]string, len(form.Value))
	for k, v := range form.Value {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

func wantReason(t *testing.T, err error, want Reason) {
	t.Helper()
	var se *SendError
	if !errors.As(err, &se) {
		t.Fatalf("err = %T (%v), want *SendError", err, err)
	}
	if se.Reason != want {
		t.Errorf("Reason = %q, want %q (err: %v)", se.Reason, want, err)
	}
}

// --- Resend -----------------------------------------------------------------

// The request shape is asserted against
// https://resend.com/docs/api-reference/emails/send-email: POST /emails,
// Bearer auth, application/json, from/to/subject required, content as text.
func TestResendSendRequestShape(t *testing.T) {
	srv := newProviderServer(t, http.StatusOK, `{"id":"49a3999c-0ce1-4ea6-ab68-afcd6dc2e794"}`)
	tr := newResendTransport("re_test_key", collectTransportOptions(srv.opts()))

	if err := tr.Send(context.Background(), providerMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := srv.only()
	if got.method != http.MethodPost || got.path != "/emails" {
		t.Errorf("%s %s, want POST /emails", got.method, got.path)
	}
	if got.auth != "Bearer re_test_key" {
		t.Errorf("Authorization = %q, want Bearer auth", got.auth)
	}
	if got.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got.contentType)
	}
	body := decodeJSON(t, got.body)
	if body["from"] != `"Vidra Mail" <no-reply@vidra.test>` {
		t.Errorf("from = %v", body["from"])
	}
	to, ok := body["to"].([]any)
	if !ok || len(to) != 1 || to[0] != "ada@example.test" {
		t.Errorf("to = %v, want a one-element array", body["to"])
	}
	if body["subject"] != "Reset your password" {
		t.Errorf("subject = %v", body["subject"])
	}
	if !strings.Contains(body["text"].(string), "reset-password/confirm") {
		t.Errorf("text = %v, want the plain-text body", body["text"])
	}
	// reply_to is snake_case per the docs, and vidra sends no HTML here: the
	// docs make text a first-class content field, so there is nothing to derive.
	if body["reply_to"] != "ops@vidra.test" {
		t.Errorf("reply_to = %v", body["reply_to"])
	}
	if _, present := body["html"]; present {
		t.Error("an HTML part was sent although Resend accepts text alone")
	}
}

func TestResendStatusReasons(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   Reason
	}{
		{http.StatusUnauthorized, `{"name":"missing_api_key","message":"…"}`, ReasonAuthFailed},
		{http.StatusForbidden, `{"name":"validation_error","message":"domain is not verified"}`, ReasonSenderRejected},
		{http.StatusForbidden, `{"name":"suspended_api_key","message":"…"}`, ReasonAuthFailed},
		{http.StatusTooManyRequests, `{"name":"daily_quota_exceeded"}`, ReasonRateLimited},
		{http.StatusUnprocessableEntity, `{"name":"missing_required_field"}`, ReasonRejected},
		{http.StatusInternalServerError, `{"name":"application_error"}`, ReasonProviderUnavailable},
		{http.StatusServiceUnavailable, `{"name":"service_unavailable"}`, ReasonProviderUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			srv := newProviderServer(t, tc.status, tc.body)
			tr := newResendTransport("re_test_key", collectTransportOptions(srv.opts()))
			wantReason(t, tr.Send(context.Background(), providerMessage()), tc.want)
		})
	}
}

// A send-only Resend key cannot read /domains and says so with 401
// restricted_api_key. That is a working configuration, not a broken one: the
// probe must report it verified or the status page condemns the recommended
// setup.
func TestResendProbe(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		body         string
		wantVerified bool
		wantErr      bool
		wantReason   Reason
	}{
		{"ok", http.StatusOK, `{"data":[]}`, true, false, ""},
		{"send-only key", http.StatusUnauthorized, `{"name":"restricted_api_key","message":"This API key is restricted to only send emails"}`, true, false, ""},
		{"bad key", http.StatusUnauthorized, `{"name":"missing_api_key"}`, false, true, ReasonAuthFailed},
		{"inactive key", http.StatusForbidden, `{"name":"restricted_api_key","message":"API key is inactive"}`, false, true, ReasonAuthFailed},
		{"provider down", http.StatusBadGateway, `{"name":"application_error"}`, false, true, ReasonProviderUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newProviderServer(t, tc.status, tc.body)
			tr := newResendTransport("re_test_key", collectTransportOptions(srv.opts()))
			res := tr.Probe(context.Background())
			got := srv.only()
			if got.method != http.MethodGet || got.path != "/domains" {
				t.Errorf("%s %s, want GET /domains — a probe must send nothing", got.method, got.path)
			}
			if res.Verified != tc.wantVerified {
				t.Errorf("Verified = %v, want %v", res.Verified, tc.wantVerified)
			}
			if tc.wantErr {
				wantReason(t, res.Err, tc.wantReason)
			} else if res.Err != nil {
				t.Errorf("Err = %v, want none", res.Err)
			}
		})
	}
}

// --- Postmark ---------------------------------------------------------------

// Asserted against https://postmarkapp.com/developer/api/email-api: POST
// /email, X-Postmark-Server-Token, Content-Type AND Accept application/json,
// PascalCase fields, TextBody alone is documented, MessageStream defaults to
// "outbound".
func TestPostmarkSendRequestShape(t *testing.T) {
	srv := newProviderServer(t, http.StatusOK, `{"To":"ada@example.test","MessageID":"b7bc2f4a","ErrorCode":0,"Message":"OK"}`)
	tr := newPostmarkTransport("", "server-token", collectTransportOptions(srv.opts()))

	if err := tr.Send(context.Background(), providerMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := srv.only()
	if got.method != http.MethodPost || got.path != "/email" {
		t.Errorf("%s %s, want POST /email", got.method, got.path)
	}
	if got.auth != "X-Postmark-Server-Token server-token" {
		t.Errorf("auth header = %q", got.auth)
	}
	if got.contentType != "application/json" || got.accept != "application/json" {
		t.Errorf("Content-Type = %q, Accept = %q, want both application/json", got.contentType, got.accept)
	}
	body := decodeJSON(t, got.body)
	for field, want := range map[string]string{
		"From":          `"Vidra Mail" <no-reply@vidra.test>`,
		"To":            "ada@example.test",
		"Subject":       "Reset your password",
		"ReplyTo":       "ops@vidra.test",
		"MessageStream": "outbound",
	} {
		if body[field] != want {
			t.Errorf("%s = %v, want %q", field, body[field], want)
		}
	}
	if !strings.Contains(body["TextBody"].(string), "reset-password/confirm") {
		t.Errorf("TextBody = %v", body["TextBody"])
	}
	if _, present := body["HtmlBody"]; present {
		t.Error("an HtmlBody was sent although TextBody alone is documented")
	}
}

func TestPostmarkMessageStreamIsConfigurable(t *testing.T) {
	srv := newProviderServer(t, http.StatusOK, `{"ErrorCode":0,"MessageID":"b7bc2f4a"}`)
	tr := newPostmarkTransport("vidra-transactional", "server-token", collectTransportOptions(srv.opts()))
	if err := tr.Send(context.Background(), providerMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if body := decodeJSON(t, srv.only().body); body["MessageStream"] != "vidra-transactional" {
		t.Errorf("MessageStream = %v, want the configured stream", body["MessageStream"])
	}
}

// Postmark answers 422 for most application failures, so the ErrorCode is what
// separates "your token is wrong" from "your sender signature is not
// confirmed". A mapping that read only the status would send every operator to
// the same wrong place.
func TestPostmarkStatusReasons(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   Reason
	}{
		{"bad token", http.StatusUnauthorized, `{"ErrorCode":10,"Message":"No account token"}`, ReasonAuthFailed},
		{"sender signature missing", http.StatusUnprocessableEntity, `{"ErrorCode":400,"Message":"Sender signature not found"}`, ReasonSenderRejected},
		{"sender signature unconfirmed", http.StatusUnprocessableEntity, `{"ErrorCode":401,"Message":"not confirmed"}`, ReasonSenderRejected},
		{"inactive recipient", http.StatusUnprocessableEntity, `{"ErrorCode":406,"Message":"inactive recipient"}`, ReasonRejected},
		{"validation", http.StatusUnprocessableEntity, `{"ErrorCode":300,"Message":"invalid email"}`, ReasonRejected},
		{"rate limited", http.StatusUnprocessableEntity, `{"ErrorCode":429,"Message":"too many"}`, ReasonRateLimited},
		{"server error", http.StatusInternalServerError, `{}`, ReasonProviderUnavailable},
		// A 200 that carries a non-zero ErrorCode is still a failure.
		{"200 with an error code", http.StatusOK, `{"ErrorCode":300,"Message":"invalid email"}`, ReasonRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newProviderServer(t, tc.status, tc.body)
			tr := newPostmarkTransport("", "server-token", collectTransportOptions(srv.opts()))
			wantReason(t, tr.Send(context.Background(), providerMessage()), tc.want)
		})
	}
}

func TestPostmarkProbeReadsTheServer(t *testing.T) {
	srv := newProviderServer(t, http.StatusOK, `{"ID":1,"Name":"vidra"}`)
	tr := newPostmarkTransport("", "server-token", collectTransportOptions(srv.opts()))
	res := tr.Probe(context.Background())
	got := srv.only()
	if got.method != http.MethodGet || got.path != "/server" {
		t.Errorf("%s %s, want GET /server", got.method, got.path)
	}
	if !res.Verified || res.Err != nil {
		t.Errorf("Probe = %+v, want verified", res)
	}
}

// --- Brevo ------------------------------------------------------------------

// Asserted against https://developers.brevo.com/reference/sendtransacemail:
// POST /v3/smtp/email, `api-key` header, JSON, sender/to/subject/htmlContent
// required, 201 on success. Brevo is the one provider that demands HTML, so it
// is the one place textToHTML runs.
func TestBrevoSendRequestShape(t *testing.T) {
	srv := newProviderServer(t, http.StatusCreated, `{"messageId":"<2026…@smtp-relay.brevo.com>"}`)
	tr := newBrevoTransport("xkeysib-test", collectTransportOptions(srv.opts()))

	if err := tr.Send(context.Background(), providerMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := srv.only()
	if got.method != http.MethodPost || got.path != "/v3/smtp/email" {
		t.Errorf("%s %s, want POST /v3/smtp/email", got.method, got.path)
	}
	if got.auth != "api-key xkeysib-test" {
		t.Errorf("auth header = %q, want the api-key header", got.auth)
	}
	if got.contentType != "application/json" {
		t.Errorf("Content-Type = %q", got.contentType)
	}
	body := decodeJSON(t, got.body)
	sender, _ := body["sender"].(map[string]any)
	if sender["email"] != "no-reply@vidra.test" || sender["name"] != "Vidra Mail" {
		t.Errorf("sender = %v, want the structured sender object", body["sender"])
	}
	to, _ := body["to"].([]any)
	if len(to) != 1 {
		t.Fatalf("to = %v, want one recipient object", body["to"])
	}
	if first, _ := to[0].(map[string]any); first["email"] != "ada@example.test" {
		t.Errorf("to[0] = %v", to[0])
	}
	if replyTo, _ := body["replyTo"].(map[string]any); replyTo["email"] != "ops@vidra.test" {
		t.Errorf("replyTo = %v, want the structured object", body["replyTo"])
	}
	if !strings.Contains(body["textContent"].(string), "reset-password/confirm") {
		t.Errorf("textContent = %v", body["textContent"])
	}
	html, _ := body["htmlContent"].(string)
	if html == "" {
		t.Fatal("htmlContent is empty, but Brevo requires it when no templateId is given")
	}
	if !strings.Contains(html, `<a href="https://vidra.example/reset-password/confirm?token=t0k">`) {
		t.Errorf("htmlContent does not linkify the redemption URL:\n%s", html)
	}
}

// Brevo reports a bad key inside its 400 class rather than as a 401, so a
// status-only mapping would tell the operator their request was malformed.
func TestBrevoStatusReasons(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   Reason
	}{
		{"unauthorized in the 400 class", http.StatusBadRequest, `{"code":"unauthorized","message":"Key not found"}`, ReasonAuthFailed},
		{"permission denied", http.StatusBadRequest, `{"code":"permission_denied","message":"…"}`, ReasonAuthFailed},
		{"out of credits", http.StatusBadRequest, `{"code":"not_enough_credits","message":"…"}`, ReasonRateLimited},
		{"plain validation error", http.StatusBadRequest, `{"code":"invalid_parameter","message":"…"}`, ReasonRejected},
		{"401", http.StatusUnauthorized, `{"code":"unauthorized"}`, ReasonAuthFailed},
		{"429", http.StatusTooManyRequests, `{}`, ReasonRateLimited},
		{"500", http.StatusInternalServerError, `{}`, ReasonProviderUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newProviderServer(t, tc.status, tc.body)
			tr := newBrevoTransport("xkeysib-test", collectTransportOptions(srv.opts()))
			wantReason(t, tr.Send(context.Background(), providerMessage()), tc.want)
		})
	}
}

func TestBrevoProbeReadsTheAccount(t *testing.T) {
	srv := newProviderServer(t, http.StatusOK, `{"email":"ops@vidra.test"}`)
	tr := newBrevoTransport("xkeysib-test", collectTransportOptions(srv.opts()))
	res := tr.Probe(context.Background())
	got := srv.only()
	if got.method != http.MethodGet || got.path != "/v3/account" {
		t.Errorf("%s %s, want GET /v3/account", got.method, got.path)
	}
	if !res.Verified || res.Err != nil {
		t.Errorf("Probe = %+v, want verified", res)
	}
}

// --- Mailgun ----------------------------------------------------------------

// Asserted against the Mailgun API reference for POST /v3/{domain}/messages:
// HTTP Basic api:<key>, multipart/form-data, from/to/subject/text, custom MIME
// headers via the h: prefix.
func TestMailgunSendRequestShape(t *testing.T) {
	srv := newProviderServer(t, http.StatusOK, `{"id":"<2026…@vidra.test>","message":"Queued. Thank you."}`)
	tr := newMailgunTransport("mail.vidra.test", RegionUS, "key-test", collectTransportOptions(srv.opts()))

	if err := tr.Send(context.Background(), providerMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := srv.only()
	if got.method != http.MethodPost || got.path != "/v3/mail.vidra.test/messages" {
		t.Errorf("%s %s, want POST /v3/<domain>/messages", got.method, got.path)
	}
	// HTTP Basic with the literal username "api".
	if !strings.HasPrefix(got.auth, "Basic ") {
		t.Fatalf("Authorization = %q, want HTTP Basic", got.auth)
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", got.auth)
	user, pass, ok := req.BasicAuth()
	if !ok || user != "api" || pass != "key-test" {
		t.Errorf("basic auth = %q/%q, want api/<key>", user, pass)
	}
	if !strings.HasPrefix(got.contentType, "multipart/form-data") {
		t.Errorf("Content-Type = %q, want multipart/form-data", got.contentType)
	}
	form := parseMultipart(t, got.contentType, got.body)
	for field, want := range map[string]string{
		"from":       `"Vidra Mail" <no-reply@vidra.test>`,
		"to":         "ada@example.test",
		"subject":    "Reset your password",
		"h:Reply-To": "ops@vidra.test",
	} {
		if form[field] != want {
			t.Errorf("%s = %q, want %q", field, form[field], want)
		}
	}
	if !strings.Contains(form["text"], "reset-password/confirm") {
		t.Errorf("text = %q", form["text"])
	}
	if _, present := form["html"]; present {
		t.Error("an html part was sent although Mailgun accepts text alone")
	}
}

func TestMailgunRegionSelectsTheBaseURL(t *testing.T) {
	us := newMailgunTransport("mail.vidra.test", RegionUS, "k", transportOptions{})
	eu := newMailgunTransport("mail.vidra.test", RegionEU, "k", transportOptions{})
	if us.baseURL != mailgunBaseURLUS {
		t.Errorf("us base = %q, want %q", us.baseURL, mailgunBaseURLUS)
	}
	if eu.baseURL != mailgunBaseURLEU {
		t.Errorf("eu base = %q, want %q", eu.baseURL, mailgunBaseURLEU)
	}
}

func TestMailgunStatusReasons(t *testing.T) {
	cases := []struct {
		status int
		want   Reason
	}{
		{http.StatusUnauthorized, ReasonAuthFailed},
		{http.StatusForbidden, ReasonAuthFailed},
		{http.StatusPaymentRequired, ReasonRateLimited},
		// The send endpoint is scoped by domain, so a 404 is "this domain is
		// not in this region" — the US/EU mix-up, not a missing endpoint.
		{http.StatusNotFound, ReasonSenderRejected},
		{http.StatusBadRequest, ReasonRejected},
		{http.StatusRequestEntityTooLarge, ReasonRejected},
		{http.StatusTooManyRequests, ReasonRateLimited},
		{http.StatusInternalServerError, ReasonProviderUnavailable},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv := newProviderServer(t, tc.status, `{"message":"no"}`)
			tr := newMailgunTransport("mail.vidra.test", RegionUS, "key-test", collectTransportOptions(srv.opts()))
			wantReason(t, tr.Send(context.Background(), providerMessage()), tc.want)
		})
	}
}

// The three probe outcomes are the whole point of the Mailgun probe: a 404 is a
// real misconfiguration (wrong region), while a 401/403 is the RECOMMENDED
// send-only domain key and must never be reported as down.
func TestMailgunProbe(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		wantVerified bool
		wantErr      bool
		wantReason   Reason
		wantInErr    string
	}{
		{"domain visible", http.StatusOK, true, false, "", ""},
		{"wrong region", http.StatusNotFound, false, true, ReasonSenderRejected, "region"},
		{"send-only key", http.StatusUnauthorized, false, false, "", ""},
		{"forbidden key", http.StatusForbidden, false, false, "", ""},
		{"provider down", http.StatusBadGateway, false, true, ReasonProviderUnavailable, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newProviderServer(t, tc.status, `{"message":"…"}`)
			tr := newMailgunTransport("mail.vidra.test", RegionEU, "key-test", collectTransportOptions(srv.opts()))
			res := tr.Probe(context.Background())
			got := srv.only()
			// Pinned to the version the API reference documents for a
			// single-domain read: a silent move back to v3 (DELETE-only today)
			// would make every probe a 404, i.e. a false "wrong region".
			if got.method != http.MethodGet || got.path != "/v4/domains/mail.vidra.test" {
				t.Errorf("%s %s, want GET /v4/domains/<domain>", got.method, got.path)
			}
			if res.Verified != tc.wantVerified {
				t.Errorf("Verified = %v, want %v", res.Verified, tc.wantVerified)
			}
			switch {
			case tc.wantErr:
				wantReason(t, res.Err, tc.wantReason)
				if tc.wantInErr != "" && !strings.Contains(res.Err.Error(), tc.wantInErr) {
					t.Errorf("Err = %q, want it to mention %q", res.Err, tc.wantInErr)
				}
			case res.Err != nil:
				t.Errorf("Err = %v, want none — an unprovable key is not a failure", res.Err)
			}
		})
	}
}

// --- shared HTTP behaviour --------------------------------------------------

// A provider that cannot be reached at all is connect_failed, not a panic and
// not a silent success.
func TestUnreachableProviderIsConnectFailed(t *testing.T) {
	srv := newProviderServer(t, http.StatusOK, `{}`)
	opts := collectTransportOptions([]transportOption{withBaseURL(srv.srv.URL), withHTTPClient(srv.srv.Client())})
	srv.srv.Close() // nothing is listening now
	tr := newResendTransport("re_test_key", opts)
	wantReason(t, tr.Send(context.Background(), providerMessage()), ReasonConnectFailed)
}

// A vendor error body is log-only detail and must be truncated at the source:
// nothing downstream has to remember to bound it.
func TestVendorErrorBodyIsTruncatedIntoTheError(t *testing.T) {
	srv := newProviderServer(t, http.StatusBadRequest, `{"message":"`+strings.Repeat("x", 4096)+`"}`)
	tr := newResendTransport("re_test_key", collectTransportOptions(srv.opts()))
	err := tr.Send(context.Background(), providerMessage())
	var se *SendError
	if !errors.As(err, &se) {
		t.Fatalf("err = %T", err)
	}
	if se.Err == nil {
		t.Fatal("no vendor detail attached for the log")
	}
	if len(se.Err.Error()) > maxErrorDetail+128 {
		t.Errorf("vendor detail is %d bytes, want it truncated near %d", len(se.Err.Error()), maxErrorDetail)
	}
	if !strings.Contains(se.Err.Error(), "truncated") {
		t.Errorf("truncation is not visible in the detail: %q", se.Err)
	}
}

// An oversized response body must not be read whole. The cap is what stops a
// broken endpoint turning a password reset into a memory spike.
func TestResponseBodyIsCapped(t *testing.T) {
	huge := strings.Repeat("y", maxResponseBody*3)
	srv := newProviderServer(t, http.StatusBadRequest, huge)
	tr := newResendTransport("re_test_key", collectTransportOptions(srv.opts()))
	err := tr.Send(context.Background(), providerMessage())
	var se *SendError
	if !errors.As(err, &se) {
		t.Fatalf("err = %T", err)
	}
	if len(se.Err.Error()) > maxErrorDetail+128 {
		t.Errorf("error detail is %d bytes", len(se.Err.Error()))
	}
}

// Refusing a CRLF-carrying address is an invariant of the Transport interface,
// not a property of whichever implementation remembered. A JSON provider would
// carry the break harmlessly all the way to the far side, where it becomes a
// MIME header — so the check belongs to every transport and nothing may reach
// the wire.
func TestEveryTransportRejectsHeaderInjection(t *testing.T) {
	mutations := map[string]func(*Message){
		"recipient":         func(m *Message) { m.To = "a@b.test\r\nBcc: evil@x.test" },
		"from address":      func(m *Message) { m.From.Address = "a@b.test\r\nBcc: evil@x.test" },
		"from display name": func(m *Message) { m.From.Name = "Ada\r\nBcc: evil@x.test" },
		"reply-to":          func(m *Message) { m.ReplyTo = "a@b.test\r\nBcc: evil@x.test" },
		"empty recipient":   func(m *Message) { m.To = "  " },
		// The LIST separators, not just the line breaks. Postmark's To and
		// Mailgun's `to` are documented comma-separated lists, so a second
		// address inside one string field is a second delivery on those two
		// transports and nothing on the other three — a fan-out that differs by
		// provider is exactly the kind of invariant that must live here.
		"comma-separated recipients":       func(m *Message) { m.To = "ada@example.test,evil@x.test" },
		"semicolon-separated recipients":   func(m *Message) { m.To = "ada@example.test;evil@x.test" },
		"recipient that is not an address": func(m *Message) { m.To = "not an address" },
		"comma-separated reply-to":         func(m *Message) { m.ReplyTo = "ops@vidra.test,evil@x.test" },
		"comma-separated sender":           func(m *Message) { m.From.Address = "no-reply@vidra.test,evil@x.test" },
	}
	for _, kind := range Kinds() {
		for name, mutate := range mutations {
			t.Run(kind+"/"+name, func(t *testing.T) {
				srv := newProviderServer(t, http.StatusOK, `{}`)
				opts := srv.opts()
				var tr Transport
				switch kind {
				case KindSMTP:
					// The SMTP transport has no httptest server; point it at a
					// port nothing listens on, so a request that got past the
					// guard would fail loudly rather than quietly succeed.
					tr = newSMTPTransport(smtpTransportConfig{Host: "127.0.0.1", Port: 1, Encryption: EncryptionNone})
				case KindResend:
					tr = newResendTransport("k", collectTransportOptions(opts))
				case KindBrevo:
					tr = newBrevoTransport("k", collectTransportOptions(opts))
				case KindMailgun:
					tr = newMailgunTransport("mail.vidra.test", RegionUS, "k", collectTransportOptions(opts))
				case KindPostmark:
					tr = newPostmarkTransport("", "k", collectTransportOptions(opts))
				default:
					t.Fatalf("no constructor for kind %q", kind)
				}
				m := providerMessage()
				mutate(&m)
				if err := tr.Send(context.Background(), m); err == nil {
					t.Fatal("transport accepted an address carrying CRLF")
				}
				if len(srv.got) != 0 {
					t.Errorf("transport sent %d requests before refusing", len(srv.got))
				}
			})
		}
	}
}

// textToHTML is the single derivation used by the one provider that demands
// HTML. It must escape, preserve the layout and linkify — and never emit an
// attribute a body could break out of.
func TestTextToHTML(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
		deny []string
	}{
		{
			name: "escapes markup",
			text: "Hi <script>alert(1)</script>\n",
			want: []string{"&lt;script&gt;"},
			deny: []string{"<script>"},
		},
		{
			name: "linkifies http and https",
			text: "Open https://vidra.example/a?b=c&d=e and http://plain.test/x\n",
			want: []string{
				`<a href="https://vidra.example/a?b=c&amp;d=e">https://vidra.example/a?b=c&amp;d=e</a>`,
				`<a href="http://plain.test/x">http://plain.test/x</a>`,
			},
		},
		{
			name: "leaves trailing punctuation out of the link",
			text: "See https://vidra.example/a.\n",
			want: []string{`<a href="https://vidra.example/a">https://vidra.example/a</a>.`},
		},
		{
			name: "preserves newlines and indentation",
			text: "Hi,\n\n    https://vidra.example/t\n",
			want: []string{"<pre", "Hi,\n\n    <a href="},
		},
		{
			name: "a quote in a URL cannot break out of the href",
			text: `https://vidra.example/"onmouseover="alert(1)` + "\n",
			deny: []string{`"onmouseover="alert(1)`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := textToHTML(tc.text)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
			for _, deny := range tc.deny {
				if strings.Contains(got, deny) {
					t.Errorf("unescaped %q survived in:\n%s", deny, got)
				}
			}
		})
	}
}

// A 3xx from a vendor host is never followed. Two of the four providers
// authenticate with a BESPOKE header (Brevo's api-key, Postmark's
// X-Postmark-Server-Token) that Go's stdlib does not strip across a redirect,
// so an open redirect anywhere under the vendor's domain would hand the live
// sending credential to the redirect target. These are fixed-host RPC calls:
// the 3xx is classified as a refusal and nothing is re-issued.
func TestProviderClientsNeverFollowARedirect(t *testing.T) {
	for _, kind := range []string{KindResend, KindBrevo, KindMailgun, KindPostmark} {
		t.Run(kind, func(t *testing.T) {
			// The host a redirect would send the credential to. It must see
			// nothing at all.
			sink := newProviderServer(t, http.StatusOK, `{"id":"pwned","MessageID":"pwned"}`)
			var redirector *httptest.Server
			redirector = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, sink.srv.URL+r.URL.Path, http.StatusFound)
			}))
			t.Cleanup(redirector.Close)
			opts := collectTransportOptions([]transportOption{
				withBaseURL(redirector.URL), withHTTPClient(redirector.Client()),
			})

			var tr Transport
			switch kind {
			case KindResend:
				tr = newResendTransport("re_test_key", opts)
			case KindBrevo:
				tr = newBrevoTransport("xkeysib-test", opts)
			case KindMailgun:
				tr = newMailgunTransport("mail.vidra.test", RegionUS, "key-test", opts)
			case KindPostmark:
				tr = newPostmarkTransport("", "server-token", opts)
			}
			err := tr.Send(context.Background(), providerMessage())
			if err == nil {
				t.Fatal("a 302 was treated as a successful send")
			}
			if len(sink.got) != 0 {
				t.Fatalf("the redirect target received %d requests — the sending credential followed the hop", len(sink.got))
			}
		})
	}
}
