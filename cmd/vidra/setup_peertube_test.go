package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The PeerTube-source block of `vidra setup`.
//
// The bug it fixes was one of ABSENCE: thirteen PEERTUBE_* keys reached the api
// container through vidra-core's compose anchor and appeared in no env template,
// so this command could not write them and `vidra setup --check` could not
// validate them. An operator migrating from PeerTube had to hand-edit a file
// this command regenerates.

// cliPeerTubeTemplate is the fixture plus the block env/production.env.example
// now ships, so these tests exercise the ordinary path — the template defines
// the keys — rather than internal/setup's append-to-an-older-template one.
const cliPeerTubeTemplate = cliTemplate + `
PEERTUBE_IMPORT_ENABLED=false
PEERTUBE_SOURCE_DATABASE_URL=
PEERTUBE_SOURCE_STORAGE_BACKEND=local
PEERTUBE_SOURCE_STORAGE_LOCAL_ROOT=
PEERTUBE_SOURCE_S3_ENDPOINT=
PEERTUBE_SOURCE_S3_BUCKET=
PEERTUBE_SOURCE_S3_ACCESS_KEY=
PEERTUBE_SOURCE_S3_SECRET_KEY=
PEERTUBE_SOURCE_S3_REGION=
PEERTUBE_SOURCE_S3_USE_SSL=true
PEERTUBE_SOURCE_S3_FORCE_PATH_STYLE=false
PEERTUBE_IMPORT_CONFLICT_POLICY=skip
PEERTUBE_IMPORT_MEDIA_MODE=copy
`

func peerTubeHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	if err := os.WriteFile(h.template, []byte(cliPeerTubeTemplate), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	return h
}

// interviewArgs is setupArgs without --non-interactive: the same answers as
// flags, so the only questions left are the ones a test is scripting.
func (h *harness) interviewArgs(extra ...string) []string {
	return append(append([]string{
		"setup", "--template", h.template,
		"--domain", "video.example.org", "--release-tag", "v0.1.1", "--storage", "local",
		"--acme-email", "ops@example.org",
	}, h.caddyArgs()...), extra...)
}

func TestSetupWritesThePeerTubeSourceFromFlags(t *testing.T) {
	h := peerTubeHarness(t)
	if err := h.run(h.setupArgs(
		"--peertube-source-url", "postgres://readonly:pw@10.0.0.5:5432/peertube_prod?sslmode=require",
		"--peertube-source-storage", "s3",
		"--peertube-source-s3-endpoint", "s3.eu-central-003.backblazeb2.com",
		"--peertube-source-s3-region", "eu-central-003",
		"--peertube-source-s3-bucket", "peertube-source-media",
		"--peertube-source-s3-access-key", "003SOURCEACCESSKEY",
		"--peertube-source-s3-secret-key", "source-secret",
		"--peertube-media-mode", "copy",
		"--peertube-conflict-policy", "rename",
	)...); err != nil {
		t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
	}
	got := h.readOutput(t)
	// The gate goes on WITHOUT --peertube beside the answers: nobody passes a
	// source DSN meaning to leave the import surface closed. Same inference
	// --database-url gets with no --database external.
	for _, want := range []string{
		"PEERTUBE_IMPORT_ENABLED=true",
		"PEERTUBE_SOURCE_DATABASE_URL=postgres://readonly:pw@10.0.0.5:5432/peertube_prod?sslmode=require",
		"PEERTUBE_SOURCE_STORAGE_BACKEND=s3",
		"PEERTUBE_SOURCE_S3_ENDPOINT=s3.eu-central-003.backblazeb2.com",
		"PEERTUBE_SOURCE_S3_BUCKET=peertube-source-media",
		"PEERTUBE_IMPORT_CONFLICT_POLICY=rename",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated file is missing %q", want)
		}
	}
	// `--check` can now see them, which it could not while the keys were in no
	// template at all: that is the other half of the same bug.
	if err := h.run("setup", "--check", h.output); err != nil {
		t.Fatalf("setup --check on the generated file: %v (stdout: %s)", err, h.out.String())
	}
}

// The handoff. `vidra setup` runs before the stack exists — install.sh stops
// here on purpose and never runs `docker compose up` — so all it can do about a
// migration is write the source down and say where the import is launched from.
func TestSetupReportsThePeerTubeImportHandoff(t *testing.T) {
	h := peerTubeHarness(t)
	// The local root rides along because the shipped defaults are local + copy,
	// and a copy-mode run with nowhere to read the source media from no longer
	// generates: it would fail every entity one at a time instead.
	if err := h.run(h.setupArgs(
		"--peertube-source-url", "postgres://readonly:pw@10.0.0.5:5432/peertube_prod?sslmode=require",
		"--peertube-source-local-root", "/srv/peertube-source/storage",
	)...); err != nil {
		t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
	}
	if !strings.Contains(h.out.String(), "https://video.example.org/admin/import-peertube") {
		t.Errorf("the report does not say where the import is run from:\n%s", h.out.String())
	}

	// And it is silent for the overwhelming majority of installs, which are not
	// migrating anything.
	h2 := peerTubeHarness(t)
	if err := h2.run(h2.setupArgs()...); err != nil {
		t.Fatalf("setup: %v (stderr: %s)", err, h2.err.String())
	}
	if strings.Contains(h2.out.String(), "import-peertube") {
		t.Errorf("an install with no PeerTube source was told about the import screen:\n%s", h2.out.String())
	}
}

// A source DSN and the source object store's secret key are secrets, so neither
// has to appear in argv — where `ps aux` and the shell history are — and neither
// may be printed by the report.
func TestSetupPeerTubeSecretsStayOutOfArgvAndOutOfTheReport(t *testing.T) {
	h := peerTubeHarness(t)
	const dsn = "postgres://readonly:sup3r-s3cret@10.0.0.5:5432/peertube_prod?sslmode=require"
	dsnFile := filepath.Join(h.dir, "source.dsn")
	if err := os.WriteFile(dsnFile, []byte(dsn+"\n"), 0o600); err != nil {
		t.Fatalf("write dsn file: %v", err)
	}
	t.Setenv("VIDRA_SETUP_PEERTUBE_SOURCE_S3_SECRET_KEY", "env-source-secret")
	if err := h.run(h.setupArgs(
		"--peertube-source-url", "@"+dsnFile,
		"--peertube-source-storage", "s3",
		"--peertube-source-s3-endpoint", "s3.example.net",
		"--peertube-source-s3-bucket", "peertube-source-media",
	)...); err != nil {
		t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
	}
	got := h.readOutput(t)
	if !strings.Contains(got, "PEERTUBE_SOURCE_DATABASE_URL="+dsn) {
		t.Errorf("the @file DSN did not land (trailing newline stripped?):\n%s", got)
	}
	if !strings.Contains(got, "PEERTUBE_SOURCE_S3_SECRET_KEY=env-source-secret") {
		t.Errorf("the $VIDRA_SETUP_* source secret did not land:\n%s", got)
	}
	if strings.Contains(h.out.String(), "sup3r-s3cret") || strings.Contains(h.out.String(), "env-source-secret") {
		t.Errorf("the report printed a secret:\n%s", h.out.String())
	}
}

// An unusable source is refused BEFORE anything is written, by the api's own
// rule — and the refusal must not repeat the DSN, which carries a password.
func TestSetupRefusesAnUnusablePeerTubeSource(t *testing.T) {
	h := peerTubeHarness(t)
	err := h.run(h.setupArgs("--peertube-source-url", "peertube-db.internal:5432")...)
	if err == nil {
		t.Fatal("a value that is not a connection string was accepted")
	}
	stderr := h.err.String()
	if !strings.Contains(stderr, "PEERTUBE_SOURCE_DATABASE_URL") {
		t.Errorf("the refusal does not name the variable to fix:\n%s", stderr)
	}
	if strings.Contains(stderr, "peertube-db.internal") {
		t.Errorf("the refusal echoes the DSN, which is a secret:\n%s", stderr)
	}
	if _, statErr := os.Stat(h.output); statErr == nil {
		t.Error("a refused run still wrote the env file")
	}
}

// THE GATE. Migrating is the minority install, so declining asks nothing else —
// nine questions about somebody else's database in the middle of a first install
// are nine questions almost everybody presses enter through.
func TestSetupPeerTubeInterviewIsOneQuestionWhenDeclined(t *testing.T) {
	h := peerTubeHarness(t)
	h.script = []promptAnswer{
		{match: "Migrate from an existing PeerTube instance", answer: "n"},
		{answer: ""},
	}
	if err := h.run(h.interviewArgs()...); err != nil {
		t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
	}
	for _, unwanted := range []string{"Source PeerTube database DSN", "Where the source instance's media lives", "Media handling for an import"} {
		for _, asked := range h.asked {
			if strings.Contains(asked, unwanted) {
				t.Errorf("declining the migration still asked %q", asked)
			}
		}
	}
	if !strings.Contains(h.readOutput(t), "PEERTUBE_IMPORT_ENABLED=false") {
		t.Error("the declined answer was not written")
	}
}

// Accepting it asks the whole block, and every answer goes through the ENGINE's
// validator AT THE PROMPT rather than being discovered by a refused file after
// every other question has been answered.
func TestSetupPeerTubeInterviewRejectsABadAnswerAtThePrompt(t *testing.T) {
	h := peerTubeHarness(t)
	h.script = []promptAnswer{
		{match: "Migrate from an existing PeerTube instance", answer: "y"},
		// The first DSN is not one. It costs a line, not the interview.
		{match: "Source PeerTube database DSN", answer: "peertube-db.internal", once: true},
		{match: "Source PeerTube database DSN", answer: "postgres://readonly:pw@10.0.0.5:5432/peertube_prod"},
		{match: "Where the source instance's media lives", answer: "local"},
		{match: "Path the source instance's media tree is mounted at", answer: "/srv/peertube-source/storage"},
		{match: "Media handling for an import", answer: "copy"},
		{match: "Collision handling for an import", answer: "skip"},
		{answer: ""},
	}
	if err := h.run(h.interviewArgs()...); err != nil {
		t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
	}
	transcript := h.out.String()
	if !strings.Contains(transcript, "PEERTUBE_SOURCE_DATABASE_URL is neither a URL nor a keyword/value connection string") {
		t.Errorf("the bad answer was not rejected at the prompt with the engine's own message:\n%s", transcript)
	}
	if strings.Contains(transcript, "peertube-db.internal") {
		t.Errorf("the prompt echoed the rejected DSN, which is handled as a secret:\n%s", transcript)
	}
	got := h.readOutput(t)
	for _, want := range []string{
		"PEERTUBE_IMPORT_ENABLED=true",
		"PEERTUBE_SOURCE_DATABASE_URL=postgres://readonly:pw@10.0.0.5:5432/peertube_prod",
		"PEERTUBE_SOURCE_STORAGE_BACKEND=local",
		"PEERTUBE_SOURCE_STORAGE_LOCAL_ROOT=/srv/peertube-source/storage",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated file is missing %q", want)
		}
	}
}

// A re-run that says nothing about the migration leaves it alone. This is the
// everyday case — `vidra setup --domain …` — and the failure it guards is a
// re-deploy that closed the import surface halfway through a migration.
func TestSetupPeerTubeSourceSurvivesAReRunThatDoesNotMentionIt(t *testing.T) {
	h := peerTubeHarness(t)
	const dsn = "postgres://readonly:pw@10.0.0.5:5432/peertube_prod?sslmode=require"
	if err := h.run(h.setupArgs("--peertube-source-url", dsn,
		"--peertube-source-local-root", "/srv/peertube-source/storage")...); err != nil {
		t.Fatalf("first setup: %v (stderr: %s)", err, h.err.String())
	}
	if err := h.run(h.setupArgs("--yes")...); err != nil {
		t.Fatalf("re-run: %v (stderr: %s)", err, h.err.String())
	}
	got := h.readOutput(t)
	if !strings.Contains(got, "PEERTUBE_IMPORT_ENABLED=true") || !strings.Contains(got, "PEERTUBE_SOURCE_DATABASE_URL="+dsn) {
		t.Errorf("a re-run that never mentioned the migration disturbed it:\n%s", got)
	}

	// …and --peertube=false is how it is closed on purpose, without losing a
	// source that exists nowhere else.
	if err := h.run(h.setupArgs("--yes", "--peertube=false")...); err != nil {
		t.Fatalf("disable run: %v (stderr: %s)", err, h.err.String())
	}
	got = h.readOutput(t)
	if !strings.Contains(got, "PEERTUBE_IMPORT_ENABLED=false") {
		t.Error("--peertube=false did not close the import surface")
	}
	if !strings.Contains(got, "PEERTUBE_SOURCE_DATABASE_URL="+dsn) {
		t.Error("closing the import surface erased the source DSN")
	}
}

// The answers file is the unattended interface, and every line in it is
// validated whether or not argv overrides it. A migration configured from one
// has to reach the same place a command line does.
func TestSetupPeerTubeAnswersFile(t *testing.T) {
	h := peerTubeHarness(t)
	answers := filepath.Join(h.dir, "answers.txt")
	if err := os.WriteFile(answers, []byte(`# a migration, configured from a file
peertube-source-url = postgres://readonly:pw@10.0.0.5:5432/peertube_prod
peertube-source-storage = local
peertube-source-local-root = /srv/peertube-source/storage
peertube-media-mode = copy
`), 0o600); err != nil {
		t.Fatalf("write answers file: %v", err)
	}
	if err := h.run(h.setupArgs("--answers", answers)...); err != nil {
		t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
	}
	got := h.readOutput(t)
	for _, want := range []string{
		"PEERTUBE_IMPORT_ENABLED=true",
		"PEERTUBE_SOURCE_STORAGE_LOCAL_ROOT=/srv/peertube-source/storage",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated file is missing %q", want)
		}
	}
}
