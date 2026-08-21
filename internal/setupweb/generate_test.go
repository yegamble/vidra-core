package setupweb

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/setup"
)

// fakeProxy stands in for cmd/vidra's renderCaddyfile/renderNginxExample pair.
// It makes the SAME call they do — the managed caddy is skipped for
// VIDRA_TLS_MODE=external and the nginx example is written instead — because the
// file LIST is what these tests assert on, and a stub that always returned both
// would make that assertion meaningless. The decision itself is cmd/vidra's and
// is injected; setup.SkipsManagedCaddy is the one implementation of it.
func fakeProxy(values map[string]string) (caddy, nginx []byte, err error) {
	if strings.TrimSpace(values["PUBLIC_BASE_URL"]) == "" {
		return nil, nil, nil
	}
	if setup.SkipsManagedCaddy(values["VIDRA_TLS_MODE"]) {
		return nil, []byte("# nginx example for " + values["PUBLIC_BASE_URL"] + "\n"), nil
	}
	return []byte("# Caddyfile for " + values["PUBLIC_BASE_URL"] + "\n"), nil, nil
}

func withProxy(o *Options) { o.RenderProxy = fakeProxy }

// validForm is a complete, writable answer set against the fixture template: a
// real domain, a pinned release, local storage, closed registration, no mail.
func validForm() Form {
	return Form{
		TLSMode:      "acme",
		Domain:       "video.example.org",
		AcmeEmail:    "ops@example.org",
		InstanceName: "Ops Video",
		ReleaseTag:   "v0.2.1",
		Storage:      "local",
		Database:     ConnForm{Mode: "local"},
		Redis:        ConnForm{Mode: "local"},
		Features:     &FeatureForm{},
		Registration: &RegistrationForm{},
	}
}

func (w *wizard) review(t *testing.T, form Form) ReviewResponse {
	t.Helper()
	var out ReviewResponse
	if code := w.callJSON(t, "POST", "/api/review", form, &out); code != http.StatusOK {
		t.Fatalf("POST /api/review = %d", code)
	}
	return out
}

func (w *wizard) apply(t *testing.T, req ApplyRequest) (int, ApplyResponse) {
	t.Helper()
	var out ApplyResponse
	code := w.callJSON(t, "POST", "/api/apply", req, &out)
	return code, out
}

// ---------------------------------------------------------------------------
// Review.

func TestReviewWritesNothing(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", withProxy)
	out := w.review(t, validForm())
	if !out.OK {
		t.Fatalf("review not ok: %+v", out)
	}
	for _, path := range []string{w.opts.OutputPath, w.opts.CaddyOutPath, w.opts.NginxOutPath} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("review created %s — it is a DRY run", path)
		}
	}
	// It says what the run WOULD write, by name, mode and purpose.
	if len(out.Files) != 2 {
		t.Fatalf("files = %+v, want the env file and a Caddyfile", out.Files)
	}
	if out.Files[0].Path != w.opts.OutputPath || out.Files[0].Mode != "0600" {
		t.Errorf("first file = %+v, want the env file at 0600", out.Files[0])
	}
	if out.Files[1].Path != w.opts.CaddyOutPath || out.Files[1].Mode != "0644" {
		t.Errorf("second file = %+v, want the Caddyfile at 0644", out.Files[1])
	}
	// Secret NAMES, by provenance — the same four buckets the terminal's report()
	// prints, and never a value.
	if len(out.Secrets.Generated) == 0 {
		t.Error("no generated secrets reported for a first install")
	}
	for _, name := range out.Secrets.Generated {
		if strings.Contains(name, "=") {
			t.Errorf("the generated-secrets list carries a value: %q", name)
		}
	}
	if out.Origin != "https://video.example.org" {
		t.Errorf("origin = %q", out.Origin)
	}
	if strings.Join(out.Profiles, " ") != "core frontend" {
		t.Errorf("profiles = %v", out.Profiles)
	}
	if out.OverwriteRequired {
		t.Error("overwrite_required on a first install")
	}
}

func TestReviewReportsEveryProblemAtOnce(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", withProxy)
	form := validForm()
	// Two independent problems: an s3 backend whose credentials are still the
	// template's <...> placeholders (three keys), and an external Postgres with
	// no connection string.
	form.Storage = "s3"
	form.Database = ConnForm{Mode: "external"}

	out := w.review(t, form)
	if out.OK {
		t.Fatal("review accepted a configuration that would not boot")
	}
	if len(out.Issues) < 2 {
		t.Fatalf("issues = %+v; the whole point of this step is that an operator sees ALL of them at once rather than one per re-run", out.Issues)
	}
	var sawPlaceholder, sawDSN bool
	for _, is := range out.Issues {
		if strings.Contains(is.Msg, "placeholder") {
			sawPlaceholder = true
		}
		if is.Var == "DATABASE_URL" {
			sawDSN = true
		}
	}
	if !sawPlaceholder || !sawDSN {
		t.Errorf("issues = %+v, want both the placeholder credentials and the missing DSN", out.Issues)
	}
	// And each one is attributed to the variable an operator has to fix.
	for _, is := range out.Issues {
		if is.Msg == "" {
			t.Errorf("an issue with no message: %+v", is)
		}
	}
}

func TestReviewSaysWhenAnOverwriteWillBeNeeded(t *testing.T) {
	t.Parallel()
	w := newWizard(t, reRunEnv, withProxy)
	out := w.review(t, validForm())
	if !out.OK || !out.OverwriteRequired {
		t.Errorf("ok = %v, overwrite_required = %v; the env file is on disk", out.OK, out.OverwriteRequired)
	}
	// The secrets it already holds come back as PRESERVED, by name: this is the
	// line that tells an operator their KEKs survive the re-run.
	if strings.Join(out.Secrets.Preserved, ",") == "" {
		t.Error("no preserved secrets reported for a re-run over a file full of them")
	}
}

// ---------------------------------------------------------------------------
// Apply.

func TestApplyWritesTheEnvFilePrivateAndTheProxyReadable(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", withProxy)
	code, out := w.apply(t, ApplyRequest{Form: validForm()})
	if code != http.StatusOK {
		t.Fatalf("apply = %d: %+v", code, out)
	}
	info, err := os.Stat(w.opts.OutputPath)
	if err != nil {
		t.Fatalf("stat env file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("env file mode = %v, want 0600 — it holds every secret the instance has", info.Mode().Perm())
	}
	caddyInfo, err := os.Stat(w.opts.CaddyOutPath)
	if err != nil {
		t.Fatalf("stat Caddyfile: %v", err)
	}
	if caddyInfo.Mode().Perm() != 0o644 {
		t.Errorf("Caddyfile mode = %v, want 0644", caddyInfo.Mode().Perm())
	}
	if _, err := os.Stat(w.opts.NginxOutPath); err == nil {
		t.Error("an nginx example was written for a managed-caddy deployment")
	}
	// The generated file is the one the engine validated: re-reading it and
	// running Check must find nothing.
	b, err := os.ReadFile(w.opts.OutputPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	f, err := setup.ParseEnvFile(b)
	if err != nil {
		t.Fatalf("the generated file does not parse: %v", err)
	}
	if issues := setup.Check(f.Values()); len(issues) > 0 {
		t.Errorf("the written file does not pass Check: %+v", issues)
	}
	if len(out.Written) != 2 {
		t.Errorf("written = %+v", out.Written)
	}
	if out.ClaimURL != "https://video.example.org/setup/claim" {
		t.Errorf("claim_url = %q", out.ClaimURL)
	}
}

func TestApplyInExternalModeWritesTheNginxExampleAndNoCaddyfile(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", withProxy)
	form := validForm()
	form.TLSMode = "external"
	form.AcmeEmail = ""
	code, out := w.apply(t, ApplyRequest{Form: form})
	if code != http.StatusOK {
		t.Fatalf("apply = %d: %+v", code, out)
	}
	if _, err := os.Stat(w.opts.CaddyOutPath); err == nil {
		t.Error("a Caddyfile was generated for a deployment whose managed caddy never starts")
	}
	if _, err := os.Stat(w.opts.NginxOutPath); err != nil {
		t.Errorf("no nginx example for an external terminator: %v", err)
	}
	// The sentence about it is the one thing that stops an operator editing a
	// .example and wondering why nothing changes.
	var sawExample bool
	for _, f := range out.Written {
		if f.Path == w.opts.NginxOutPath && strings.Contains(f.What, "mounted by nothing") {
			sawExample = true
		}
	}
	if !sawExample {
		t.Errorf("the nginx example is not described as an example: %+v", out.Written)
	}
}

func TestApplyRefusesToRewriteAnExistingFileWithoutTheAcknowledgement(t *testing.T) {
	t.Parallel()
	w := newWizard(t, reRunEnv, withProxy)
	before, err := os.ReadFile(w.opts.OutputPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	code, _ := w.apply(t, ApplyRequest{Form: validForm()})
	if code != http.StatusConflict {
		t.Fatalf("apply without an acknowledgement = %d, want 409", code)
	}
	after, err := os.ReadFile(w.opts.OutputPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the refused apply changed the file anyway")
	}
	// And with the acknowledgement it goes through — this gate is INTENT, not
	// safety.
	code, out := w.apply(t, ApplyRequest{Form: validForm(), Overwrite: true})
	if code != http.StatusOK {
		t.Fatalf("apply with the acknowledgement = %d: %+v", code, out)
	}
}

func TestApplyNeverReMintsASecretItAlreadyHas(t *testing.T) {
	t.Parallel()
	w := newWizard(t, reRunEnv, withProxy)
	code, out := w.apply(t, ApplyRequest{Form: validForm(), Overwrite: true})
	if code != http.StatusOK {
		t.Fatalf("apply = %d: %+v", code, out)
	}
	b, err := os.ReadFile(w.opts.OutputPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	f, err := setup.ParseEnvFile(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The KEK is the one that matters: re-minting it orphans every TOTP secret
	// already sealed in the database, and no re-wrap job exists anywhere.
	for key, want := range map[string]string{
		"MFA_KEY_KEK":            "U0VOVElORUwtS0VLLTAwMDAwMDAwMDAwMDAwMDAwMDA=",
		"JWT_SECRET":             "SENTINEL-JWT-000000000000000000000000000000",
		"POSTGRES_PASSWORD":      "SENTINEL-PG-deadbeef",
		"REDIS_PASSWORD":         "SENTINEL-REDIS-deadbeef",
		"SEARCH_INTERNAL_SECRET": "SENTINEL-SEARCH-0000000000000000000000",
	} {
		got, _ := f.Value(key)
		if got != want {
			t.Errorf("%s was replaced: %q", key, got)
		}
	}
	if len(out.Secrets.Generated) != 0 {
		t.Errorf("generated = %v on a re-run over a complete file", out.Secrets.Generated)
	}
}

func TestNothingIsWrittenWhenTheProxyRenderFails(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", func(o *Options) {
		o.RenderProxy = func(map[string]string) ([]byte, []byte, error) {
			return nil, nil, errSyntheticProxy
		}
	})
	code, out := w.apply(t, ApplyRequest{Form: validForm()})
	if code == http.StatusOK {
		t.Fatalf("apply succeeded with a proxy render that failed: %+v", out)
	}
	// THE ORDERING DISCIPLINE: both proxy configs are rendered before anything is
	// written, so an answer the Caddyfile refuses stops the run rather than
	// leaving a rewritten env file beside a proxy config that was never
	// generated.
	if _, err := os.Stat(w.opts.OutputPath); err == nil {
		t.Error("the env file was written even though the proxy config could not be rendered")
	}
}

var errSyntheticProxy = &syntheticError{"the Caddyfile template has no vidra:tls marker"}

type syntheticError struct{ msg string }

func (e *syntheticError) Error() string { return e.msg }

func TestApplyRefusesAConfigurationThatWouldNotBoot(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", withProxy)
	form := validForm()
	form.Database = ConnForm{Mode: "external"} // external with no DSN
	code, out := w.apply(t, ApplyRequest{Form: form})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("apply = %d, want 422", code)
	}
	if out.OK || len(out.Issues) == 0 {
		t.Errorf("no issues reported: %+v", out)
	}
	if _, err := os.Stat(w.opts.OutputPath); err == nil {
		t.Error("a file was written for a configuration that would not boot")
	}
}

// ---------------------------------------------------------------------------
// The two containment properties that belong to the write half.

func TestNoResponseAnywhereEchoesASecretValue(t *testing.T) {
	t.Parallel()
	w := newWizard(t, reRunEnv, func(o *Options) {
		o.RenderProxy = fakeProxy
		o.CheckDomain = func(context.Context, string) DomainReport {
			return DomainReport{Status: "warn", Message: "not checked"}
		}
		o.Doctor = func(context.Context) (DoctorReport, error) {
			return DoctorReport{Root: ".", EnvFile: "env/production.env"}, nil
		}
	})
	form := validForm()
	// Post secrets IN, as an operator would. They must not come back out of any
	// endpoint, including the one that just wrote them to disk.
	form.S3 = S3Form{Endpoint: "e", Region: "r", Bucket: "b", AccessKey: "a", SecretKey: "SENTINEL-S3-deadbeef"}
	form.Mail = &MailForm{Host: "smtp.example.net", Port: "587", Username: "u", Password: "SENTINEL-SMTP-deadbeef", From: "n@example.org"}

	for _, call := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/state", nil},
		{"POST", "/api/validate", ValidateRequest{Field: "domain", Value: "video.example.org", Context: ValidateContext{TLSMode: "acme"}}},
		{"POST", "/api/check-domain", checkDomainRequest{Domain: "video.example.org", TLSMode: "acme"}},
		{"POST", "/api/doctor", nil},
		{"POST", "/api/review", form},
		{"POST", "/api/apply", ApplyRequest{Form: form, Overwrite: true}},
		// After the write, so the state endpoint is asked again over a file that
		// now holds BOTH the planted secrets and the posted ones.
		{"GET", "/api/state", nil},
		{"POST", "/api/review", validForm()},
	} {
		_, raw := w.call(t, call.method, call.path, call.body)
		assertNoSentinel(t, call.method+" "+call.path, raw)
	}
	// And the served page itself carries no value — it is a static document with
	// the token nowhere in it.
	_, page := w.call(t, "GET", "/", nil)
	assertNoSentinel(t, "GET /", page)
	if strings.Contains(string(page), w.Token()) {
		t.Error("the served page embeds the one-time token")
	}
}

func TestTheWireCannotNameAFile(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", withProxy)
	// The four paths this server touches are fixed in Options from the command
	// line. There is no field to carry a fifth, and an attempt to invent one is
	// refused by the decoder rather than ignored — a silently-dropped
	// "output_path" would look to the caller like it had worked.
	evil := filepath.Join(w.dir, "..", "escaped.env")
	code, raw := w.call(t, "POST", "/api/apply", map[string]any{
		"domain":       "video.example.org",
		"output_path":  evil,
		"template":     "/etc/passwd",
		"caddy_out":    evil,
		"nginx_out":    evil,
		"storage":      "local",
		"tls_mode":     "acme",
		"registration": map[string]bool{"enabled": false},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("apply with path fields = %d, want 400: %s", code, raw)
	}
	if _, err := os.Stat(evil); err == nil {
		t.Errorf("%s was created", evil)
	}
	// And a legitimate apply still lands on exactly the configured path.
	if code, out := w.apply(t, ApplyRequest{Form: validForm()}); code != http.StatusOK {
		t.Fatalf("apply = %d: %+v", code, out)
	} else if out.Written[0].Path != w.opts.OutputPath {
		t.Errorf("wrote %q, want %q", out.Written[0].Path, w.opts.OutputPath)
	}
}
