package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A minimal template in the deployment format: comments, a blank secret, a
// <generate: ...> placeholder, a commented-out optional key. The FORMAT is
// pinned by internal/setup's fixture and golden; this one only has to exercise
// the command's plumbing.
const cliTemplate = `# ==============================================================================
# VIDRA — PRODUCTION ENVIRONMENT TEMPLATE (cli test fixture)
# ==============================================================================

VIDRA_ENV=production
VIDRA_CORE_TAG=v0.1.0
VIDRA_USER_TAG=v0.1.0
VIDRA_SEARCH_TAG=v0.1.0
PUBLIC_BASE_URL=https://example.com

#   generate: openssl rand -base64 48
JWT_SECRET=
# DESTRUCTIVE TO ROTATE.
#   generate: openssl rand -base64 32
MFA_KEY_KEK=<generate: openssl rand -base64 32>
#   generate: openssl rand -hex 32
POSTGRES_PASSWORD=<generate: openssl rand -hex 32>
REDIS_PASSWORD=

STORAGE_BACKEND=local
MAIL_ENABLED=false
REGISTRATION_ENABLED=false
REGISTRATION_REQUIRE_APPROVAL=false
# FEDERATION_KEY_KEK=
`

type harness struct {
	dir      string
	template string
	output   string
	out      bytes.Buffer
	err      bytes.Buffer
	stdin    string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{dir: t.TempDir()}
	h.template = filepath.Join(h.dir, "production.env.example")
	h.output = filepath.Join(h.dir, "production.env")
	if err := os.WriteFile(h.template, []byte(cliTemplate), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	return h
}

func (h *harness) run(args ...string) error {
	h.out.Reset()
	h.err.Reset()
	return run(streams{in: strings.NewReader(h.stdin), out: &h.out, err: &h.err}, args)
}

func (h *harness) setupArgs(extra ...string) []string {
	return append([]string{
		"setup", "--template", h.template, "--non-interactive",
		"--domain", "video.example.org", "--release-tag", "v0.1.1", "--storage", "local",
	}, extra...)
}

func (h *harness) readOutput(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(h.output)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	return string(b)
}

func TestSetupWritesAPrivateFileAndPrintsTheRenderCheck(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
	}

	info, err := os.Stat(h.output)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}

	got := h.readOutput(t)
	for _, want := range []string{"PUBLIC_BASE_URL=https://video.example.org", "VIDRA_CORE_TAG=v0.1.1"} {
		if !strings.Contains(got, want) {
			t.Errorf("generated file is missing %q", want)
		}
	}
	if strings.Contains(got, "<generate:") {
		t.Error("a placeholder survived into the generated file")
	}

	stdout := h.out.String()
	if !strings.Contains(stdout, "docker compose -f docker-compose.yml -f docker-compose.prod.yml") || !strings.Contains(stdout, "config -q") {
		t.Errorf("the render-check command was not printed:\n%s", stdout)
	}
	// The summary names secrets; it must never print one.
	if !strings.Contains(stdout, "generated secrets") {
		t.Errorf("no secret summary:\n%s", stdout)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "generated secrets") && strings.Contains(line, "=") {
			t.Errorf("a secret value may have been printed: %q", line)
		}
	}
}

// Overwriting an existing env file without --from would mint new KEKs behind the
// operator's back, so the command refuses and says how to merge.
func TestSetupRefusesToOverwriteWithoutFrom(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	err := h.run(h.setupArgs()...)
	if err == nil || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("err = %v, want a refusal pointing at --from", err)
	}
}

func TestSetupMergeIsByteIdentical(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	first := h.readOutput(t)

	if err := h.run(h.setupArgs("--from", h.output)...); err != nil {
		t.Fatalf("merge setup: %v (stderr: %s)", err, h.err.String())
	}
	if got := h.readOutput(t); got != first {
		t.Errorf("re-running setup changed the file\n--- got ---\n%s--- want ---\n%s", got, first)
	}
	if !strings.Contains(h.out.String(), "preserved secrets") {
		t.Errorf("the merge did not report preserved secrets:\n%s", h.out.String())
	}
}

func TestSetupRotateKEKRequiresYes(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	before := h.readOutput(t)

	err := h.run(h.setupArgs("--from", h.output, "--rotate", "MFA_KEY_KEK")...)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v, want the destructive-rotation refusal", err)
	}
	if h.readOutput(t) != before {
		t.Fatal("the refused rotation still rewrote the file")
	}

	if err := h.run(h.setupArgs("--from", h.output, "--rotate", "MFA_KEY_KEK", "--yes")...); err != nil {
		t.Fatalf("confirmed rotation: %v (stderr: %s)", err, h.err.String())
	}
	after := h.readOutput(t)
	if kek(t, after) == kek(t, before) {
		t.Error("MFA_KEY_KEK was not rotated")
	}
	if jwt(t, after) != jwt(t, before) {
		t.Error("JWT_SECRET changed while only MFA_KEY_KEK was rotated")
	}
}

// --check is the doctor precursor: it names every variable an operator has to
// fix and exits non-zero, and it never writes anything.
func TestSetupCheckReportsBadVariablesByName(t *testing.T) {
	h := newHarness(t)
	bad := filepath.Join(h.dir, "broken.env")
	if err := os.WriteFile(bad, []byte(strings.Join([]string{
		"VIDRA_ENV=production",
		"HTTP_PORT=not-a-number",
		"HTTP_REQUEST_TIMEOUT=soon",
		"STORAGE_S3_ACCESS_KEY=<your Spaces access key>",
		"VIDRA_CORE_TAG=v0.1.1",
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := h.run("setup", "--check", bad)
	if err == nil {
		t.Fatal("--check exited zero on a broken file")
	}
	out := h.out.String()
	for _, want := range []string{"HTTP_PORT", "HTTP_REQUEST_TIMEOUT", "STORAGE_S3_ACCESS_KEY"} {
		if !strings.Contains(out, want) {
			t.Errorf("--check did not name %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "VIDRA_CORE_TAG") {
		t.Errorf("--check complained about a compose-only key:\n%s", out)
	}

	// And a file this command generated passes.
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := h.run("setup", "--check", h.output); err != nil {
		t.Fatalf("--check on a generated file: %v (%s)", err, h.out.String())
	}
	if !strings.Contains(h.out.String(), "no problems") {
		t.Errorf("unexpected --check output:\n%s", h.out.String())
	}
}

// The generated file is refused, not written, when it would not boot.
func TestSetupRefusesToWriteAnInvalidFile(t *testing.T) {
	h := newHarness(t)
	err := h.run("setup", "--template", h.template, "--non-interactive",
		"--domain", "video.example.org", "--storage", "s3")
	if err == nil {
		t.Fatal("an s3 backend with no credentials was accepted")
	}
	if _, statErr := os.Stat(h.output); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("a file was written despite the validation failure")
	}
	if !strings.Contains(h.err.String(), "STORAGE_S3_ENDPOINT") && !strings.Contains(h.err.String(), "STORAGE_S3_BUCKET") {
		t.Errorf("the failure did not name the missing S3 variables:\n%s", h.err.String())
	}
}

func TestSetupInteractiveAnswersTheMinimalQuestions(t *testing.T) {
	h := newHarness(t)
	// domain, release tag, storage, SMTP?, open registration?
	h.stdin = "video.example.org\nv0.1.1\nlocal\nn\nn\n"
	if err := h.run("setup", "--template", h.template); err != nil {
		t.Fatalf("interactive setup: %v (stderr: %s)", err, h.err.String())
	}
	got := h.readOutput(t)
	for _, want := range []string{"PUBLIC_BASE_URL=https://video.example.org", "VIDRA_CORE_TAG=v0.1.1", "STORAGE_BACKEND=local", "REGISTRATION_ENABLED=false"} {
		if !strings.Contains(got, want) {
			t.Errorf("generated file is missing %q", want)
		}
	}
	if !strings.Contains(h.out.String(), "Public domain of this instance") {
		t.Errorf("the domain was not asked for:\n%s", h.out.String())
	}
}

// An unattended install must fail loudly rather than hang or invent an answer.
func TestSetupNonInteractiveNeedsADomain(t *testing.T) {
	h := newHarness(t)
	err := h.run("setup", "--template", h.template, "--non-interactive", "--release-tag", "v0.1.1")
	if err == nil || !strings.Contains(err.Error(), "domain is required") {
		t.Fatalf("err = %v, want the domain requirement", err)
	}
}

func TestOutputPathDefault(t *testing.T) {
	got, err := outputPath("env/production.env.example", "")
	if err != nil || got != "env/production.env" {
		t.Errorf("outputPath = %q, %v", got, err)
	}
	if got, err := outputPath("env/production.env.example", "/tmp/other.env"); err != nil || got != "/tmp/other.env" {
		t.Errorf("explicit --output = %q, %v", got, err)
	}
	if _, err := outputPath("env/whatever", ""); err == nil {
		t.Error("a template without a .example suffix should require --output")
	}
}

func TestUnknownCommandPrintsUsage(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := run(streams{in: strings.NewReader(""), out: &out, err: &errBuf}, []string{"doctor"})
	if !errors.Is(err, errReported) {
		t.Fatalf("err = %v, want errReported", err)
	}
	if !strings.Contains(errBuf.String(), "unknown command") || !strings.Contains(errBuf.String(), "setup") {
		t.Errorf("usage not printed:\n%s", errBuf.String())
	}
}

func kek(t *testing.T, file string) string { return valueOf(t, file, "MFA_KEY_KEK") }
func jwt(t *testing.T, file string) string { return valueOf(t, file, "JWT_SECRET") }

func valueOf(t *testing.T, file, key string) string {
	t.Helper()
	for _, line := range strings.Split(file, "\n") {
		if strings.HasPrefix(line, key+"=") {
			return strings.TrimPrefix(line, key+"=")
		}
	}
	t.Fatalf("%s not found in the generated file", key)
	return ""
}
