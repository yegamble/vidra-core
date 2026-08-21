package main

import (
	"bytes"
	"context"
	"reflect"
	"testing"

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
	)

	terminal := runInterview(t, tmpl, nil, []promptAnswer{
		{match: "TLS (acme", answer: tlsMode},
		{match: "Public domain of this instance", answer: domain},
		{match: "Contact address for Let's Encrypt", answer: acmeEmail},
		{match: "Name of this instance", answer: instanceName},
		{match: "Release tag to deploy", answer: releaseTag},
		{match: "Media storage backend", answer: "s3"},
		{match: "S3 endpoint host", answer: s3Endpoint},
		{match: "S3 region", answer: s3Region},
		{match: "S3 bucket", answer: s3Bucket},
		{match: "S3 access key", answer: s3AccessKey},
		{match: "S3 secret key", answer: s3SecretKey},
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
