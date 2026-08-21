package setupweb

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureTemplate is internal/setup's own trimmed copy of the meta repo's
// env/production.env.example, read from THERE rather than copied here.
//
// A second copy would drift, and the drift would be silent: this package's whole
// job is to hand the engine a template and print what it did, so a wizard tested
// against a template the engine's own tests do not use is a wizard tested
// against nothing in particular. It carries the shapes that matter — blank
// secrets, <generate: ...> placeholders, <your Spaces access key>, a
// commented-out optional key, MAIL_ENABLED=true beside a blank SMTP_HOST.
const fixtureTemplate = "../setup/testdata/template.env.example"

// wizard is a server on a temp deployment tree. base and client are how the
// call helpers reach it — filled either from an httptest.Server (newWizard) or
// from the server's own running listener (newRunningWizard), so the same helpers
// drive both.
type wizard struct {
	*Server
	base   string
	client *http.Client
	dir    string
	out    string
	opts   Options
}

func newWizard(t *testing.T, existing string, mutate func(*Options)) *wizard {
	t.Helper()
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "production.env.example")
	b, err := os.ReadFile(fixtureTemplate)
	if err != nil {
		t.Fatalf("read %s: %v", fixtureTemplate, err)
	}
	if err := os.WriteFile(tmpl, b, 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	out := filepath.Join(dir, "production.env")
	if existing != "" {
		if err := os.WriteFile(out, []byte(existing), 0o600); err != nil {
			t.Fatalf("write existing env: %v", err)
		}
	}
	opt := Options{
		Listen:        "127.0.0.1:0",
		IdleTimeout:   -1,
		TemplatePath:  tmpl,
		OutputPath:    out,
		CaddyOutPath:  filepath.Join(dir, "Caddyfile.local"),
		NginxOutPath:  filepath.Join(dir, "nginx-external.conf.example"),
		DeployCommand: "vidra deploy",
	}
	if mutate != nil {
		mutate(&opt)
	}
	s, err := New(opt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &wizard{Server: s, base: ts.URL, client: ts.Client(), dir: dir, out: out, opts: opt}
}

// runningWizard is a wizard on its OWN Run loop, not an httptest wrapper around
// the handler — so the idle timer, which Run arms, is actually ticking. It is
// what the idle-versus-install tests need: the interplay between the timer and a
// live deploy is a property of the running server, and an httptest server never
// starts the timer at all.
func newRunningWizard(t *testing.T, mutate func(*Options)) *wizard {
	t.Helper()
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "production.env.example")
	b, err := os.ReadFile(fixtureTemplate)
	if err != nil {
		t.Fatalf("read %s: %v", fixtureTemplate, err)
	}
	if err := os.WriteFile(tmpl, b, 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	out := filepath.Join(dir, "production.env")
	opt := Options{
		Listen:        "127.0.0.1:0",
		TemplatePath:  tmpl,
		OutputPath:    out,
		CaddyOutPath:  filepath.Join(dir, "Caddyfile.local"),
		NginxOutPath:  filepath.Join(dir, "nginx-external.conf.example"),
		DeployCommand: "vidra deploy",
	}
	if mutate != nil {
		mutate(&opt)
	}
	s, err := New(opt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() { _ = s.Run(context.Background()) }()
	t.Cleanup(func() { s.Shutdown("test over") })
	return &wizard{Server: s, base: "http://" + s.Addr(), client: &http.Client{}, dir: dir, out: out, opts: opt}
}

// call is an authenticated request, the way the page makes them.
func (w *wizard) call(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s body: %v", path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, w.base+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set(tokenHeader, w.Token())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := w.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (w *wizard) callJSON(t *testing.T, method, path string, body, into any) int {
	t.Helper()
	code, raw := w.call(t, method, path, body)
	if into != nil {
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("%s %s: response is not JSON (%v): %s", method, path, err, raw)
		}
	}
	return code
}

func (w *wizard) state(t *testing.T) StateResponse {
	t.Helper()
	var st StateResponse
	if code := w.callJSON(t, "GET", "/api/state", nil, &st); code != http.StatusOK {
		t.Fatalf("GET /api/state = %d", code)
	}
	return st
}

// ---------------------------------------------------------------------------
// State.

func TestStateOnAFirstInstall(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", nil)
	st := w.state(t)

	if st.OutputExists {
		t.Error("output_exists is true with no env file on disk")
	}
	// The template's PUBLIC_BASE_URL is an EXAMPLE. Seeding the field with it
	// would let an operator press through the wizard and generate a deployment
	// for example.com.
	if st.Seed.Domain != "" {
		t.Errorf("domain seeded from the template: %q", st.Seed.Domain)
	}
	// The template says s3 next to <your Spaces access key>. An s3 answer with
	// placeholder credentials cannot pass Check, so the wizard must not open on
	// it — same flip the terminal interview makes.
	if st.Seed.Storage != "local" {
		t.Errorf("storage seeded as %q; the template's s3 has placeholder credentials, so local is the only default that can be written", st.Seed.Storage)
	}
	if st.Seed.S3.CredentialsAnswered {
		t.Error("credentials_answered is true for a template full of <...> placeholders")
	}
	if st.Seed.TLSMode != "acme" {
		t.Errorf("tls_mode = %q, want the template's acme", st.Seed.TLSMode)
	}
	if st.Seed.InstanceName != "Example Video" {
		t.Errorf("instance_name = %q", st.Seed.InstanceName)
	}
	if st.Seed.ReleaseTag != "v0.1.0" {
		t.Errorf("release_tag = %q", st.Seed.ReleaseTag)
	}
	// MAIL_ENABLED=true beside a blank SMTP_HOST is a QUESTION, not a
	// configuration — the same test the interview makes before defaulting its
	// SMTP prompt to yes.
	if st.Seed.MailConfigured {
		t.Error("mail_configured is true for a template with a blank SMTP_HOST")
	}
	if len(st.Secrets) != 0 {
		t.Errorf("secrets = %v on a first install", st.Secrets)
	}
	if st.Seed.Database.Mode != "local" || st.Seed.Redis.Mode != "local" {
		t.Errorf("datastore modes = %q/%q, want local/local", st.Seed.Database.Mode, st.Seed.Redis.Mode)
	}
	if st.DeployCommand != "vidra deploy" {
		t.Errorf("deploy_command = %q", st.DeployCommand)
	}
}

// reRunEnv is a deployment that already exists: a real domain, real S3
// credentials, an extra compose profile, and secrets on file.
const reRunEnv = `VIDRA_ENV=production
PUBLIC_BASE_URL=https://video.example.org
NEXT_PUBLIC_API_BASE_URL=https://video.example.org
CORS_ALLOWED_ORIGINS=https://video.example.org
VIDRA_CORE_TAG=v0.2.1
VIDRA_USER_TAG=v0.2.1
VIDRA_SEARCH_TAG=v0.2.1
VIDRA_TLS_MODE=internal
VIDRA_ACME_EMAIL=ops@example.org
VIDRA_COMPOSE_PROFILES=core frontend ipfs captions
VIDRA_EXTERNAL_POSTGRES=false
VIDRA_EXTERNAL_REDIS=true
REDIS_URL=rediss://:hunter2@redis.example.net:6379/0
SEARCH_REDIS_URL=rediss://:hunter2@redis.example.net:6379/1
INSTANCE_NAME=Ops Video
JWT_SECRET=SENTINEL-JWT-000000000000000000000000000000
MFA_KEY_KEK=U0VOVElORUwtS0VLLTAwMDAwMDAwMDAwMDAwMDAwMDA=
POSTGRES_USER=vidra
POSTGRES_PASSWORD=SENTINEL-PG-deadbeef
REDIS_PASSWORD=SENTINEL-REDIS-deadbeef
SEARCH_INTERNAL_SECRET=SENTINEL-SEARCH-0000000000000000000000
STORAGE_BACKEND=s3
STORAGE_S3_ENDPOINT=nyc3.digitaloceanspaces.com
STORAGE_S3_REGION=nyc3
STORAGE_S3_BUCKET=ops-video-media
STORAGE_S3_ACCESS_KEY=DO00REALACCESSKEY
STORAGE_S3_SECRET_KEY=SENTINEL-S3-deadbeef
STORAGE_S3_USE_SSL=true
MAIL_ENABLED=true
SMTP_HOST=smtp.example.net
SMTP_PORT=587
SMTP_USERNAME=postmaster
SMTP_PASSWORD=SENTINEL-SMTP-deadbeef
SMTP_FROM=noreply@example.org
REGISTRATION_ENABLED=true
REGISTRATION_REQUIRE_APPROVAL=true
SEARCH_SERVICE_URL=http://search:8080
`

// sentinels is every planted secret VALUE in reRunEnv. No response body this
// server produces may contain one of them.
var sentinels = []string{
	"SENTINEL-JWT-000000000000000000000000000000",
	"U0VOVElORUwtS0VLLTAwMDAwMDAwMDAwMDAwMDAwMDA=",
	"SENTINEL-PG-deadbeef",
	"SENTINEL-REDIS-deadbeef",
	"SENTINEL-SEARCH-0000000000000000000000",
	"SENTINEL-S3-deadbeef",
	"SENTINEL-SMTP-deadbeef",
	"hunter2",
}

func assertNoSentinel(t *testing.T, what string, body []byte) {
	t.Helper()
	for _, s := range sentinels {
		if bytes.Contains(body, []byte(s)) {
			t.Errorf("%s echoed the secret %q back to the browser:\n%s", what, s, body)
		}
	}
}

func TestStateOnAReRun(t *testing.T) {
	t.Parallel()
	w := newWizard(t, reRunEnv, nil)
	st := w.state(t)

	if !st.OutputExists {
		t.Error("output_exists is false with an env file on disk")
	}
	if st.Seed.Domain != "https://video.example.org" {
		t.Errorf("domain = %q", st.Seed.Domain)
	}
	if st.Seed.TLSMode != "internal" || st.Seed.AcmeEmail != "ops@example.org" {
		t.Errorf("tls seed = %q / %q", st.Seed.TLSMode, st.Seed.AcmeEmail)
	}
	if st.Seed.ReleaseTag != "v0.2.1" || st.Seed.InstanceName != "Ops Video" {
		t.Errorf("release/name = %q / %q", st.Seed.ReleaseTag, st.Seed.InstanceName)
	}
	// The optional components are seeded from the profile list that is running
	// today. Without this, an operator re-running the wizard to change a domain
	// would silently turn ipfs and captions off.
	want := FeatureForm{Captions: true, IPFS: true}
	if st.Seed.Features != want {
		t.Errorf("features = %+v, want %+v", st.Seed.Features, want)
	}
	if st.Seed.Storage != "s3" || !st.Seed.S3.CredentialsAnswered {
		t.Errorf("storage = %q (credentials answered: %v); real keys are on file", st.Seed.Storage, st.Seed.S3.CredentialsAnswered)
	}
	if st.Seed.S3.AccessKey != "DO00REALACCESSKEY" || !st.Seed.S3.SecretKeySet {
		t.Errorf("s3 seed = %+v", st.Seed.S3)
	}
	if st.Seed.Redis.Mode != "external" || !st.Seed.Redis.URLSet {
		t.Errorf("redis seed = %+v, want external with a URL on file", st.Seed.Redis)
	}
	if st.Seed.Database.Mode != "local" || st.Seed.Database.URLSet {
		t.Errorf("database seed = %+v, want local with no URL", st.Seed.Database)
	}
	if !st.Seed.MailConfigured || !st.Seed.Mail.PasswordSet || st.Seed.Mail.Host != "smtp.example.net" {
		t.Errorf("mail seed = %v / %+v", st.Seed.MailConfigured, st.Seed.Mail)
	}
	if !st.Seed.Registration.Enabled || !st.Seed.Registration.RequireApproval {
		t.Errorf("registration seed = %+v", st.Seed.Registration)
	}
	// Names, in the engine's own vocabulary, so an operator can see the KEK will
	// survive.
	wantSecrets := []string{"JWT_SECRET", "MFA_KEY_KEK", "POSTGRES_PASSWORD", "REDIS_PASSWORD", "SEARCH_INTERNAL_SECRET"}
	if strings.Join(st.Secrets, ",") != strings.Join(wantSecrets, ",") {
		t.Errorf("secrets = %v, want %v", st.Secrets, wantSecrets)
	}
}

func TestStateNeverEchoesASecretValue(t *testing.T) {
	t.Parallel()
	w := newWizard(t, reRunEnv, nil)
	_, raw := w.call(t, "GET", "/api/state", nil)
	assertNoSentinel(t, "GET /api/state", raw)
}

func TestStateReportsAnUnreadableTemplateAsAnOperatorSentence(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", nil)
	if err := os.Remove(w.opts.TemplatePath); err != nil {
		t.Fatalf("remove template: %v", err)
	}
	code, raw := w.call(t, "GET", "/api/state", nil)
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", code)
	}
	// Never a raw Go error: this is the same rule internal/doctor states as its
	// second doctrine, and a wizard is the last place an operator should meet
	// "open /x/y: no such file or directory".
	if !strings.Contains(string(raw), "deployment directory") {
		t.Errorf("the message does not say what to do about it: %s", raw)
	}
}

// ---------------------------------------------------------------------------
// Validate.

func TestValidateIsTheEnginesOwnVerdict(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", nil)
	for _, tc := range []struct {
		name       string
		req        ValidateRequest
		wantOK     bool
		normalized string
		errSubstr  string
	}{
		{name: "tls mode", req: ValidateRequest{Field: "tls_mode", Value: "acme-staging"}, wantOK: true, normalized: "acme-staging"},
		{name: "tls mode typo", req: ValidateRequest{Field: "tls_mode", Value: "acmestaging"}, errSubstr: "unsupported TLS mode"},
		// A bare host under an acme mode becomes an https origin; the SAME host
		// under plain-http becomes an http one. One function, two answers, and
		// the mode is the reason the wizard asks it first.
		{name: "domain under acme", req: ValidateRequest{Field: "domain", Value: "video.example.org", Context: ValidateContext{TLSMode: "acme"}}, wantOK: true, normalized: "https://video.example.org"},
		{name: "domain under plain-http", req: ValidateRequest{Field: "domain", Value: "video.lan", Context: ValidateContext{TLSMode: "plain-http"}}, wantOK: true, normalized: "http://video.lan"},
		{name: "http domain under acme", req: ValidateRequest{Field: "domain", Value: "http://video.example.org", Context: ValidateContext{TLSMode: "acme"}}, errSubstr: "http"},
		{name: "domain with a path", req: ValidateRequest{Field: "domain", Value: "video.example.org/videos", Context: ValidateContext{TLSMode: "acme"}}, errSubstr: "path"},
		{name: "acme email", req: ValidateRequest{Field: "acme_email", Value: "ops@example.org"}, wantOK: true, normalized: "ops@example.org"},
		// Empty is VALID: the contact address is optional, and refusing it at a
		// prompt would refuse what the file itself allows.
		{name: "acme email blank", req: ValidateRequest{Field: "acme_email", Value: "  "}, wantOK: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var res ValidateResponse
			if code := w.callJSON(t, "POST", "/api/validate", tc.req, &res); code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			if res.OK != tc.wantOK {
				t.Fatalf("ok = %v, want %v (error: %q)", res.OK, tc.wantOK, res.Error)
			}
			if tc.wantOK {
				if tc.normalized != "" && res.Normalized != tc.normalized {
					t.Errorf("normalized = %q, want %q", res.Normalized, tc.normalized)
				}
				return
			}
			if !strings.Contains(res.Error, tc.errSubstr) {
				t.Errorf("error = %q, want it to mention %q", res.Error, tc.errSubstr)
			}
			// The engine's message, minus its package prefix — the same reduction
			// the terminal's askValid prints under a bad answer.
			if strings.HasPrefix(res.Error, "setup: ") {
				t.Errorf("error keeps the package prefix: %q", res.Error)
			}
		})
	}
}

func TestValidateRefusesAFieldItDoesNotOwn(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", nil)
	// The one thing this endpoint must NOT do is grow a validator of its own for
	// a field the engine has no opinion about — that is how a wizard starts
	// accepting answers Generate then refuses.
	for _, field := range []string{"smtp_host", "s3_bucket", "instance_name", ""} {
		var res map[string]string
		code := w.callJSON(t, "POST", "/api/validate", ValidateRequest{Field: field, Value: "anything"}, &res)
		if code != http.StatusBadRequest {
			t.Errorf("field %q = %d, want 400", field, code)
		}
	}
}

// ---------------------------------------------------------------------------
// The DNS report.

func TestCheckDomainIsSkippedForTheModesTheTerminalSkips(t *testing.T) {
	t.Parallel()
	called := 0
	w := newWizard(t, "", func(o *Options) {
		o.CheckDomain = func(context.Context, string) DomainReport {
			called++
			return DomainReport{Status: "ok", Message: "it resolves here"}
		}
	})
	for _, mode := range []string{"plain-http", "external"} {
		var rep DomainReport
		w.callJSON(t, "POST", "/api/check-domain", checkDomainRequest{Domain: "video.lan", TLSMode: mode}, &rep)
		if rep.Status != "skipped" {
			t.Errorf("mode %s: status = %q, want skipped", mode, rep.Status)
		}
	}
	if called != 0 {
		t.Errorf("the resolver was asked %d time(s) for modes the terminal never asks about", called)
	}
	var rep DomainReport
	w.callJSON(t, "POST", "/api/check-domain", checkDomainRequest{Domain: "video.example.org", TLSMode: "acme"}, &rep)
	if rep.Status != "ok" || called != 1 {
		t.Errorf("acme: status = %q after %d call(s), want ok after 1", rep.Status, called)
	}
}

func TestCheckDomainIsNeverAGate(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", func(o *Options) {
		o.CheckDomain = func(context.Context, string) DomainReport {
			return DomainReport{Status: "fail", Message: "it resolves somewhere else", Fix: "point the record here"}
		}
	})
	var rep DomainReport
	code := w.callJSON(t, "POST", "/api/check-domain", checkDomainRequest{Domain: "video.example.org", TLSMode: "acme"}, &rep)
	// A failing DNS check answers 200 with a finding. It is an ordinary state of
	// a fresh install — the operator may be about to create the record — so it
	// must not arrive as an HTTP error the page would treat as a wall.
	if code != http.StatusOK {
		t.Errorf("status = %d, want 200: this is feedback, never a gate", code)
	}
	if rep.Fix == "" {
		t.Error("the report dropped the fix line")
	}
}
