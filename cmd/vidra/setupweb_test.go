package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/doctor"
	"github.com/vidra/vidra-core/internal/preflight"
	"github.com/vidra/vidra-core/internal/setup"
	"github.com/vidra/vidra-core/internal/setupweb"
)

// THE PARITY TEST.
//
// `vidra setup` and `vidra setup --web` ask the same questions in two places,
// and the wizard duplicates the QUESTION TEXT — that duplication was accepted
// deliberately (a web form does not read like a terminal prompt, and neither
// wants the other's wording). What was NOT accepted is a second behaviour, and
// this file is what makes that stick: the two front ends assemble the same
// setup.Answers from the same answers, and produce the same file from the same
// seeds. Everything downstream — validation, resolution, secret preservation,
// the bytes on disk — is one engine, so parity HERE is parity everywhere.
//
// It lives in cmd/vidra rather than in internal/setupweb because the thing being
// matched is interview(), which is here. A parity test that could not call the
// terminal interview would be a parity test in name only.

// seqReader is a deterministic entropy source, so two Generate runs over the
// same answers produce byte-identical files and a difference in the OUTPUT can
// only mean a difference in the ANSWERS.
type seqReader struct{ n byte }

func (r *seqReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.n
		r.n++
	}
	return len(p), nil
}

func parseEnv(t *testing.T, content string) *setup.EnvFile {
	t.Helper()
	f, err := setup.ParseEnvFile([]byte(content))
	if err != nil {
		t.Fatalf("parse fixture env: %v", err)
	}
	return f
}

// stubDNS silences the interview's one-line domain feedback. It reaches a
// nameserver and an IP echo service otherwise, and a parity test whose result
// depends on the network it runs on proves nothing about either front end.
func stubDNS(t *testing.T) {
	t.Helper()
	restore := checkDomain
	checkDomain = func(context.Context, preflight.DomainRequest) preflight.DomainResult {
		return preflight.DomainResult{Status: preflight.StatusWarn, Message: "not checked in tests"}
	}
	t.Cleanup(func() { checkDomain = restore })
}

// runInterview drives the terminal interview with answers keyed by QUESTION,
// seeding the feature answers first exactly as runSetup does.
func runInterview(t *testing.T, tmpl, existing *setup.EnvFile, script []promptAnswer) setup.Answers {
	t.Helper()
	var a setup.Answers
	// runSetup's line, and the reason it exists: Answers.Features is
	// AUTHORITATIVE, so a front end that did not seed it would silently turn
	// every optional component off on a re-run.
	a.Features = setup.FeaturesFromProfiles(effective(tmpl, existing, "VIDRA_COMPOSE_PROFILES"))

	var out bytes.Buffer
	var asked []string
	in := &prompter{t: t, out: &out, script: script, asked: &asked}
	if err := interview(streams{in: in, out: &out, err: &bytes.Buffer{}}, tmpl, existing, &a); err != nil {
		t.Fatalf("interview: %v\ntranscript:\n%s", err, out.String())
	}
	return a
}

// TestWebAnswersMatchTheTerminalInterview is the strict half: every question
// answered explicitly, in both front ends, and the assembled setup.Answers
// compared field by field.
func TestWebAnswersMatchTheTerminalInterview(t *testing.T) {
	stubDNS(t)
	tmpl := parseEnv(t, cliTemplate)

	// One set of answers, spelled once. The terminal reads them off the prompts;
	// the form below carries the same strings.
	const (
		tlsMode      = "acme"
		domain       = "video.example.org"
		acmeEmail    = "ops@example.org"
		instanceName = "Ops Video"
		releaseTag   = "v0.2.1"
		s3Endpoint   = "nyc3.digitaloceanspaces.com"
		s3Region     = "nyc3"
		s3Bucket     = "ops-video-media"
		s3AccessKey  = "DO00REALACCESSKEY"
		s3SecretKey  = "s3-secret-value"
		databaseURL  = "postgres://vidra:pw@db.example.net:25060/defaultdb?sslmode=require"
		smtpHost     = "smtp.example.net"
		smtpPort     = "2525"
		smtpUser     = "postmaster"
		smtpPassword = "smtp-secret-value"
		smtpFrom     = "noreply@example.org"
		// The migration block: a source in the s3 shape, because that is the branch
		// with the most fields in it and the one a parity slip would hide in.
		ptSourceURL   = "postgres://readonly:pw@10.0.0.5:5432/peertube_prod?sslmode=require"
		ptS3Endpoint  = "s3.eu-central-003.backblazeb2.com"
		ptS3Region    = "eu-central-003"
		ptS3Bucket    = "peertube-source-media"
		ptS3AccessKey = "003SOURCEACCESSKEY"
		ptS3Secret    = "peertube-source-secret-value"
	)

	terminal := runInterview(t, tmpl, nil, []promptAnswer{
		{match: "TLS (acme", answer: tlsMode},
		{match: "Public domain of this instance", answer: domain},
		{match: "Contact address for Let's Encrypt", answer: acmeEmail},
		{match: "Name of this instance", answer: instanceName},
		{match: "Release tag to deploy", answer: releaseTag},
		{match: "Media storage backend", answer: "s3"},
		// `once` on all five, because the SOURCE instance's block below asks the
		// same five questions with "Source " on the front — and these entries are a
		// substring of those prompts. Dropping each after it has answered is what
		// keeps this script keyed by question rather than quietly answering the
		// migration's endpoint with the instance's own.
		{match: "S3 endpoint host", answer: s3Endpoint, once: true},
		{match: "S3 region", answer: s3Region, once: true},
		{match: "S3 bucket", answer: s3Bucket, once: true},
		{match: "S3 access key", answer: s3AccessKey, once: true},
		{match: "S3 secret key", answer: s3SecretKey, once: true},
		{match: "Use an external/managed PostgreSQL", answer: "y"},
		{match: "Managed PostgreSQL connection string", answer: databaseURL},
		{match: "Use an external/managed Redis", answer: "n"},
		{match: "Configure optional components", answer: "y"},
		{match: "ClamAV", answer: "y"},
		{match: "Whisper", answer: "n"},
		{match: "RTMP", answer: "y"},
		{match: "OpenTelemetry", answer: "n"},
		{match: "IPFS node", answer: "y"},
		{match: "Configure SMTP now", answer: "y"},
		{match: "SMTP host", answer: smtpHost},
		{match: "SMTP port", answer: smtpPort},
		{match: "SMTP username", answer: smtpUser},
		{match: "SMTP password", answer: smtpPassword},
		{match: "From address", answer: smtpFrom},
		{match: "Open registration to the public now", answer: "y"},
		{match: "Require an admin to approve each signup", answer: "y"},
		{match: "Migrate from an existing PeerTube instance", answer: "y"},
		{match: "Source PeerTube database DSN", answer: ptSourceURL},
		{match: "Where the source instance's media lives", answer: "s3"},
		{match: "Source S3 endpoint host", answer: ptS3Endpoint},
		{match: "Source S3 region", answer: ptS3Region},
		{match: "Source S3 bucket", answer: ptS3Bucket},
		{match: "Source S3 access key", answer: ptS3AccessKey},
		{match: "Source S3 secret key", answer: ptS3Secret},
		{match: "Media handling for an import", answer: "reference"},
		{match: "Collision handling for an import", answer: "rename"},
	})

	web := setupweb.BuildAnswers(setupweb.Form{
		TLSMode:      tlsMode,
		Domain:       domain,
		AcmeEmail:    acmeEmail,
		InstanceName: instanceName,
		ReleaseTag:   releaseTag,
		Storage:      "s3",
		S3: setupweb.S3Form{
			Endpoint:  s3Endpoint,
			Region:    s3Region,
			Bucket:    s3Bucket,
			AccessKey: s3AccessKey,
			SecretKey: s3SecretKey,
		},
		Database:     setupweb.ConnForm{Mode: "external", URL: databaseURL},
		Redis:        setupweb.ConnForm{Mode: "local"},
		Features:     &setupweb.FeatureForm{Scan: true, Media: true, IPFS: true},
		Mail:         &setupweb.MailForm{Host: smtpHost, Port: smtpPort, Username: smtpUser, Password: smtpPassword, From: smtpFrom},
		Registration: &setupweb.RegistrationForm{Enabled: true, RequireApproval: true},
		PeerTube: &setupweb.PeerTubeForm{
			Enabled:   true,
			SourceURL: ptSourceURL,
			Storage:   "s3",
			S3: setupweb.S3Form{
				Endpoint:  ptS3Endpoint,
				Region:    ptS3Region,
				Bucket:    ptS3Bucket,
				AccessKey: ptS3AccessKey,
				SecretKey: ptS3Secret,
			},
			MediaMode:   "reference",
			ConflictPol: "rename",
		},
	}, tmpl, nil)

	if !reflect.DeepEqual(terminal, web) {
		t.Errorf("the two front ends assembled different answers from the same input:\nterminal: %+v\nweb:      %+v", terminal, web)
	}
}

// parityExistingEnv is a deployment that already exists, for the press-enter-
// through case: a real origin, secrets on file, an extra compose profile, and a
// registration policy that is NOT the template's.
const parityExistingEnv = `VIDRA_ENV=production
PUBLIC_BASE_URL=https://video.example.org
INSTANCE_NAME=Ops Video
VIDRA_CORE_TAG=v0.2.1
VIDRA_USER_TAG=v0.2.1
VIDRA_SEARCH_TAG=v0.2.1
JWT_SECRET=cGFyaXR5LWp3dC1zZWNyZXQtdGhhdC1pcy1sb25nLWVub3VnaA==
MFA_KEY_KEK=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=
POSTGRES_PASSWORD=0123456789abcdef0123456789abcdef
REDIS_PASSWORD=fedcba9876543210fedcba9876543210
VIDRA_COMPOSE_PROFILES=core frontend ipfs
VIDRA_EXTERNAL_POSTGRES=false
VIDRA_EXTERNAL_REDIS=false
VIDRA_TLS_MODE=acme
VIDRA_ACME_EMAIL=ops@example.org
STORAGE_BACKEND=local
REGISTRATION_ENABLED=true
REGISTRATION_REQUIRE_APPROVAL=true
`

// TestWebSeedsMatchTheInterviewDefaults is the other half, and it is the one an
// operator actually meets: a RE-RUN where nothing is retyped.
//
// The terminal answers it by pressing enter through every prompt, which takes
// each bracketed default. The wizard answers it by posting the form back exactly
// as GET /api/state seeded it. If setupweb.SeedFor and the interview's defaults
// disagree about a single field, the two produce different deployments from the
// same instruction — which is precisely the drift the exported seed rules
// (setup.Effective, setup.S3CredentialsAnswered) exist to prevent.
//
// It compares the GENERATED VALUES rather than the Answers struct, because the
// two structs legitimately differ here: pressing enter on a masked secret prompt
// re-states the current value into the answer, while the wizard's blank field
// means "unanswered" and lets the engine fall through to the same value. Same
// deployment, different route to it — and the deployment is the contract.
func TestWebSeedsMatchTheInterviewDefaults(t *testing.T) {
	stubDNS(t)
	tmpl := parseEnv(t, cliTemplate)
	existing := setup.MergeSources(parseEnv(t, parityExistingEnv))

	// Enter, all the way down. The catch-all answers every prompt with "", which
	// is what a bare newline gives ask() and askYesNo().
	terminal := runInterview(t, tmpl, existing, []promptAnswer{{answer: ""}})

	seed := setupweb.SeedFor(tmpl, existing)
	web := setupweb.BuildAnswers(setupweb.Form{
		TLSMode:      seed.TLSMode,
		Domain:       seed.Domain,
		AcmeEmail:    seed.AcmeEmail,
		InstanceName: seed.InstanceName,
		ReleaseTag:   seed.ReleaseTag,
		Storage:      seed.Storage,
		S3: setupweb.S3Form{
			Endpoint:  seed.S3.Endpoint,
			Region:    seed.S3.Region,
			Bucket:    seed.S3.Bucket,
			AccessKey: seed.S3.AccessKey,
			// Left blank on purpose: the page renders a secret already on file as
			// "kept — leave blank to keep", and never as a value to post back.
		},
		Database: setupweb.ConnForm{Mode: seed.Database.Mode},
		Redis:    setupweb.ConnForm{Mode: seed.Redis.Mode},
		// nil: the operator did not touch the optional components, so the seeded
		// profile list stands. This is the field where a bool cannot say "leave it
		// alone" and the ipfs profile would otherwise be dropped.
		Features: nil,
		Mail:     nil,
		// Always sent, and the approval flag is carried even while registration is
		// being left closed — the interview keeps it the same way, so a re-run that
		// re-opens signups finds the policy the operator last chose.
		Registration: &setupweb.RegistrationForm{Enabled: seed.Registration.Enabled, RequireApproval: seed.Registration.RequireApproval},
		// Also always sent, and also with both secrets left blank: the page renders
		// a source DSN already on file as "kept — leave blank to keep it", exactly
		// like the S3 secret above. The gate is the seed, so pressing Next through a
		// deployment that is not migrating leaves it not migrating.
		PeerTube: &setupweb.PeerTubeForm{
			Enabled:     seed.PeerTube.Enabled,
			Storage:     seed.PeerTube.Storage,
			LocalRoot:   seed.PeerTube.LocalRoot,
			S3:          setupweb.S3Form{Endpoint: seed.PeerTube.S3Endpoint, Region: seed.PeerTube.S3Region, Bucket: seed.PeerTube.S3Bucket, AccessKey: seed.PeerTube.S3AccessKey},
			MediaMode:   seed.PeerTube.MediaMode,
			ConflictPol: seed.PeerTube.ConflictPol,
		},
	}, tmpl, existing)

	terminalRes, err := setup.Generate(setup.Request{Template: tmpl, Existing: existing, Answers: terminal, Rand: &seqReader{}})
	if err != nil {
		t.Fatalf("generate from the terminal's answers: %v", err)
	}
	webRes, err := setup.Generate(setup.Request{Template: tmpl, Existing: existing, Answers: web, Rand: &seqReader{}})
	if err != nil {
		t.Fatalf("generate from the wizard's answers: %v", err)
	}
	if !reflect.DeepEqual(terminalRes.Values, webRes.Values) {
		for k, want := range terminalRes.Values {
			if got := webRes.Values[k]; got != want {
				t.Errorf("%s: terminal wrote %q, the wizard wrote %q", k, want, got)
			}
		}
		for k := range webRes.Values {
			if _, ok := terminalRes.Values[k]; !ok {
				t.Errorf("%s: the wizard wrote it and the terminal did not", k)
			}
		}
	}
	if !bytes.Equal(terminalRes.Content, webRes.Content) {
		t.Error("the two front ends rendered different files from the same seeds")
	}
	// And the profile the operator never mentioned is still there.
	if got := webRes.Values["VIDRA_COMPOSE_PROFILES"]; got != "core frontend ipfs" {
		t.Errorf("VIDRA_COMPOSE_PROFILES = %q; a re-run that touched nothing dropped a component", got)
	}
}

// ---------------------------------------------------------------------------
// The wiring: what cmd/vidra adds to internal/setupweb, tested at the real seam.

func TestSetupWebRefusesANonLoopbackListen(t *testing.T) {
	h := newHarness(t)
	err := h.run("setup", "--template", h.template, "--web", "--listen", "0.0.0.0:8321")
	if err == nil {
		t.Fatal("the wizard accepted a public bind address")
	}
	// The refusal is the containment model's first rule, and it has to arrive
	// with the way OUT attached: an operator on a droplet who is told "no" and
	// nothing else reaches for a firewall rule next.
	for _, want := range []string{"loopback only", "ssh -L"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q:\n%s", want, err)
		}
	}
}

func TestSetupWebRefusesTheTerminalOnlyFlags(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct{ flag, args string }{
		{"--non-interactive", "--non-interactive"},
		{"--yes", "--yes"},
		{"--yes-i-know", "--yes-i-know"},
		{"--from", "--from=" + h.template},
		{"--rotate", "--rotate=JWT_SECRET"},
	} {
		// Every one of them has an answer INSIDE the wizard, so a flag that
		// silently did nothing would be an operator believing they had
		// pre-authorised something.
		err := h.run("setup", "--template", h.template, "--web", tc.args)
		if err == nil {
			t.Errorf("%s was accepted beside --web", tc.flag)
			continue
		}
		if !strings.Contains(err.Error(), tc.flag) {
			t.Errorf("%s: refusal does not name the flag: %v", tc.flag, err)
		}
	}
}

func TestSetupWebRefusesAMissingTemplateBeforeOpeningABrowser(t *testing.T) {
	h := newHarness(t)
	// The same mistake in both front ends — running from the wrong directory —
	// and it belongs on the command line rather than as a red banner in a browser
	// the operator has only just opened.
	err := h.run("setup", "--template", filepath.Join(h.dir, "nope.env.example"), "--web")
	if err == nil {
		t.Fatal("the wizard started without a template")
	}
	if !strings.Contains(err.Error(), "nope.env.example") {
		t.Errorf("error does not name the template: %v", err)
	}
}

func TestWizardBannerNamesTheOneTimeLinkAndTheTunnel(t *testing.T) {
	srv, err := setupweb.New(setupweb.Options{Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Shutdown("test over")

	var out bytes.Buffer
	printWizardBanner(streams{out: &out, err: io.Discard}, srv, "env/production.env")
	got := out.String()
	if !strings.Contains(got, srv.URL()) {
		t.Errorf("the banner does not print the opening link:\n%s", got)
	}
	if !strings.Contains(got, srv.Token()) {
		t.Errorf("the link carries no token:\n%s", got)
	}
	_, port, _ := net.SplitHostPort(srv.Addr())
	// The port the OS actually granted, not the one that was asked for: with
	// --listen 127.0.0.1:0 those differ, and a banner naming the wrong one is a
	// tunnel that forwards to nothing.
	if !strings.Contains(got, "ssh -L "+port+":127.0.0.1:"+port) {
		t.Errorf("the tunnel instruction does not name the bound port %s:\n%s", port, got)
	}
	// It names the FILE the wizard is about to write, not the address it is on:
	// the sentence is about what the operator is authorising by leaving this
	// window open.
	if !strings.Contains(got, "It writes env/production.env") {
		t.Errorf("the banner does not name the env file it will write:\n%s", got)
	}
	for _, want := range []string{"ONE-TIME", "Keep this terminal open", "Ctrl-C"} {
		if !strings.Contains(got, want) {
			t.Errorf("the banner is missing %q:\n%s", want, got)
		}
	}
}

// deployStub is a deploy.sh that reports everything the wizard's install path is
// supposed to give it: both output streams, whether stdin is readable, the
// ENV_FILE it was handed, and a specific exit code.
const deployStub = `#!/usr/bin/env bash
echo "[deploy] starting"
echo "[deploy] a line on stderr" >&2
if read -r line; then
  echo "[deploy] READ FROM STDIN: $line"
else
  echo "[deploy] stdin is at EOF"
fi
echo "[deploy] ENV_FILE=$ENV_FILE"
exit 7
`

func TestRunDeployForWizardClosesStdinAndStreamsBothOutputs(t *testing.T) {
	dir := fakeDeployment(t, defaultEnv)
	write(t, filepath.Join(dir, "deploy", "deploy.sh"), deployStub)

	var out bytes.Buffer
	code, err := runDeployForWizard(context.Background(),
		wrapperFlags{repo: dir, envFile: "env/production.env", explicit: true}, &out)
	if err != nil {
		t.Fatalf("runDeployForWizard: %v", err)
	}
	// A non-zero exit is a RESULT, not an error: the script printed its own
	// reason and the wizard shows the code.
	if code != 7 {
		t.Errorf("exit code = %d, want the script's own 7", code)
	}
	got := out.String()
	// STDIN IS CLOSED. There is nobody at a terminal, so a script that blocked on
	// a prompt would hang the wizard behind a spinner with nothing to read; at
	// EOF a `read` fails at once and the script dies with its own message.
	if !strings.Contains(got, "stdin is at EOF") {
		t.Errorf("the script could read stdin — a prompt would hang the wizard:\n%s", got)
	}
	// Both streams, interleaved into one log: deploy.sh logs progress to stdout
	// and refusals to stderr, and an operator watching an install needs them in
	// the order they happened.
	for _, want := range []string{"[deploy] starting", "[deploy] a line on stderr", "[deploy] ENV_FILE=env/production.env"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from the stream:\n%s", want, got)
		}
	}
}

func TestRunDeployForWizardSaysWhenTheScriptIsNotThere(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	_, err := runDeployForWizard(context.Background(),
		wrapperFlags{repo: dir, envFile: "env/production.env", explicit: true}, &out)
	if err == nil {
		t.Fatal("a missing deploy.sh was reported as a successful deploy")
	}
	// A deploy that could not be STARTED has no log to read, and the wizard says
	// so differently from one that ran and failed.
	if !strings.Contains(err.Error(), "deploy.sh") {
		t.Errorf("error does not name the missing script: %v", err)
	}
}

func TestDeployCommandShowsTheEnvFileOnlyWhenItIsNotTheDefault(t *testing.T) {
	if got := deployCommand(defaultEnvFile); got != "vidra deploy" {
		t.Errorf("deployCommand(default) = %q", got)
	}
	if got := deployCommand("env/staging.env"); got != "vidra deploy --env env/staging.env" {
		t.Errorf("deployCommand(staging) = %q", got)
	}
}

func TestDoctorAndStatusAreFlattenedWithWordsNotIntegers(t *testing.T) {
	rep := doctorReport(doctor.Report{
		Root: "/srv/vidra", EnvFile: "env/production.env",
		Results: []doctor.Result{
			{Check: "compose version", Section: doctor.SectionStack, Finding: doctor.Finding{Status: doctor.StatusOK, Detail: "2.29.1"}},
			{Check: "disk space", Section: doctor.SectionState, Finding: doctor.Finding{Status: doctor.StatusFail, Detail: "2% free", Fix: "prune images"}},
		},
	})
	if rep.OK != 1 || rep.Fail != 1 {
		t.Errorf("counts = %+v", rep)
	}
	// The word, not the iota: a page switching on 0/1/2 would break silently the
	// day a fourth outcome is added.
	if rep.Results[0].Status != "ok" || rep.Results[1].Status != "fail" {
		t.Errorf("statuses = %q / %q", rep.Results[0].Status, rep.Results[1].Status)
	}
	if rep.Results[0].Section != "docker & compose" {
		t.Errorf("section = %q", rep.Results[0].Section)
	}

	lines := statusLines(statusReport{lines: []statusLine{
		{source: "api", check: "/readyz", status: preflight.StatusWarn, detail: "nothing is listening", fix: "vidra deploy"},
		// The verbatim `compose ps` table is deliberately dropped: the Success
		// step is a summary.
		{source: "containers", check: "compose ps", status: preflight.StatusOK, detail: "7 container(s)", block: "NAME  STATUS\napi   Up"},
	}})
	if len(lines) != 2 || lines[0].Status != "warn" || lines[1].Status != "ok" {
		t.Fatalf("lines = %+v", lines)
	}
	if lines[1].Detail != "7 container(s)" {
		t.Errorf("detail = %q", lines[1].Detail)
	}
}
